package probe

// 音频指纹：不依赖章节名，靠同一季多集重复出现的音频找片头/片尾。
// 只做「同一段音频在不同集重复」这件事：把每集开头/结尾的音频转成
// 0.5 秒 RMS 包络，两两做互相关找最佳偏移，再取最长连续匹配段。

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"lime/internal/store"
)

// StartSeasonFingerprint 异步分析某一季的片头片尾，返回是否成功启动。
// 同一时间只跑一个季，避免把 NAS 的 CPU 和网盘带宽打满。
func (p *Prober) StartSeasonFingerprint(seriesID string, season int) bool {
	return p.startSeasonFingerprint(seriesID, season, nil)
}

// startSeasonFingerprint 是带 focus 的版本：focus 里的单集会优先进入
// 采样窗口，保证刚入库的最新一集一定被分析到。
// 同一季 30 分钟内只跑一次，避免连续入库时反复扫同一季。
func (p *Prober) startSeasonFingerprint(seriesID string, season int, focus []string) bool {
	if p == nil || !p.enabled || p.ffmpegBin == "" || seriesID == "" || !p.segmentsOn.Load() {
		return false
	}
	key := fmt.Sprintf("%s|%d", seriesID, season)
	p.fpMu.Lock()
	if last, ok := p.fpSeasonLast[key]; ok && time.Since(last) < 30*time.Minute {
		// 上次已经覆盖了这批新集才跳过；有新集进来就放行。
		covered := len(focus) == 0
		if !covered {
			covered = true
			for _, id := range focus {
				if !p.fpSeasonFocus[key][id] {
					covered = false
					break
				}
			}
		}
		if covered {
			p.fpMu.Unlock()
			return false
		}
	}
	p.fpMu.Unlock()

	if !p.fpRunning.CompareAndSwap(false, true) {
		// 已有别的季在跑：新集入库触发的请求 5 分钟后再试一次，
		// 避免刚好撞上就永久漏掉；播放触发的请求交给下次播放/夜间预热。
		if len(focus) > 0 {
			time.AfterFunc(5*time.Minute, func() {
				p.startSeasonFingerprint(seriesID, season, focus)
			})
		}
		return false
	}
	p.fpMu.Lock()
	p.fpSeasonLast[key] = time.Now()
	if p.fpSeasonFocus[key] == nil {
		p.fpSeasonFocus[key] = make(map[string]bool, len(focus))
	}
	for _, id := range focus {
		p.fpSeasonFocus[key][id] = true
	}
	p.fpMu.Unlock()
	_ = p.db.MarkFingerprintRun(seriesID, season, "running", "")

	go func() {
		defer p.fpRunning.Store(false)
		saved, err := p.analyzeSeason(seriesID, season, focus)
		if err == nil && saved == 0 && len(focus) > 0 {
			// 新集入库时只采样了 6 集；如果它们没有共同的 OP/ED，
			// 再用整季样本重试一次，避免新剧因为样本太少被永久漏掉。
			p.log.Info("fingerprint focus empty; retry full season",
				"series", seriesID, "season", season)
			saved, err = p.analyzeSeason(seriesID, season, nil)
		}
		if err != nil {
			_ = p.db.MarkFingerprintRun(seriesID, season, "failed", firstLine(err.Error()))
			_ = p.db.SetSegmentTargetStatus(seriesID, season, "failed", firstLine(err.Error()))
			p.log.Warn("fingerprint season failed", "series", seriesID, "season", season, "err", err)
			return
		}
		_ = p.db.MarkFingerprintRun(seriesID, season, "done", fmt.Sprintf("saved=%d", saved))
		_ = p.db.SetSegmentTargetStatus(seriesID, season, "done", fmt.Sprintf("saved=%d", saved))
	}()
	return true
}

// WarmMissingFingerprints 给夜间回填用：找一季还没有 Intro/Outro 的剧跑指纹。
// 同时只跑一季（startSeasonFingerprint 内部有全局锁），返回实际启动的数量。
func (p *Prober) WarmMissingFingerprints(limit int) int {
	if p == nil || !p.enabled || p.ffmpegBin == "" || !p.segmentsOn.Load() {
		return 0
	}
	if limit <= 0 {
		limit = 1
	}
	if limit > 10 {
		limit = 10
	}
	cfg := p.warmupConfig()
	mode := strings.ToLower(cfg.SegmentScope)
	since := int64(0)
	if mode == "recent" {
		since = time.Now().Add(-7 * 24 * time.Hour).Unix()
	}
	// 失败或没有 OP/ED 的季 7 天内不重复扫；新集入库/播放仍会单独触发。
	retryAfter := time.Now().Add(-7 * 24 * time.Hour).Unix()
	cands, err := p.db.FingerprintCandidates(mode, since, retryAfter, limit)
	if err != nil {
		p.log.Warn("fingerprint backfill query failed", "err", err)
		return 0
	}
	started := 0
	for _, c := range cands {
		if p.startSeasonFingerprint(c.SeriesID, c.Season, nil) {
			started++
			break // 全局只能跑一季，剩下的下一轮再排
		}
	}
	if started > 0 {
		p.log.Info("fingerprint backfill started", "scope", mode, "candidates", len(cands))
	}
	return started
}

// FingerprintRunning 给面板显示当前是否在分析。
func (p *Prober) FingerprintRunning() bool { return p != nil && p.fpRunning.Load() }

type fpEpisode struct {
	src       store.SeasonEpisodeSource
	runtime   float64
	head      []float64
	tail      []float64
	tailStart float64
}

type fpCandidate struct {
	partner int
	start   float64
	end     float64
	score   float64
}

type fpSpan struct{ start, end float64 }

// focusFingerprintLimit 是「新集入库」触发的指纹采样上限。
// 只取新集和前后几集，避免为了一集去分析整季几十集音频。
const focusFingerprintLimit = 6

// fullFingerprintLimit 是夜间整季回填的采样上限。
// 40 集 × 头尾各 10 分钟音频在网络盘上要跑 20-30 分钟，一晚补不了几季；
// 16 集已经足够确认重复片段，单季回填时间能压到几分钟。
const fullFingerprintLimit = 16

// pickFingerprintSources 从整季里挑一小段采样。
// focus 非空时以 focus 为中心向两边扩展，保证刚入库的集一定在样本里；
// 没有 focus 时取前 16 集。
func pickFingerprintSources(srcs []store.SeasonEpisodeSource, focus []string, limit int) []store.SeasonEpisodeSource {
	if limit <= 0 {
		limit = 40
	}
	if len(srcs) <= limit {
		return srcs
	}
	if len(focus) == 0 {
		return srcs[:limit]
	}
	focusSet := make(map[string]bool, len(focus))
	for _, id := range focus {
		focusSet[id] = true
	}
	var positions []int
	for i := range srcs {
		if focusSet[srcs[i].ItemID] {
			positions = append(positions, i)
		}
	}
	if len(positions) == 0 {
		return srcs[:limit]
	}

	selected := make(map[int]bool, limit)
	for _, i := range positions {
		if len(selected) >= limit {
			break
		}
		selected[i] = true
	}
	for radius := 1; len(selected) < limit; radius++ {
		added := false
		for _, i := range positions {
			for _, j := range []int{i - radius, i + radius} {
				if j < 0 || j >= len(srcs) || selected[j] || len(selected) >= limit {
					continue
				}
				selected[j] = true
				added = true
			}
		}
		if !added {
			break
		}
	}
	out := make([]store.SeasonEpisodeSource, 0, len(selected))
	for i := range srcs {
		if selected[i] {
			out = append(out, srcs[i])
		}
	}
	return out
}

func (p *Prober) analyzeSeason(seriesID string, season int, focus []string) (int, error) {
	preferLow := p.segmentPreferLowBitrate()
	srcs, err := p.db.SeasonSources(seriesID, season, preferLow)
	if err != nil {
		return 0, err
	}
	if len(srcs) < 2 {
		return 0, fmt.Errorf("not enough episodes: %d", len(srcs))
	}
	limit := fullFingerprintLimit
	if len(focus) > 0 {
		limit = focusFingerprintLimit
	}
	srcs = pickFingerprintSources(srcs, focus, limit)

	eps := make([]*fpEpisode, len(srcs))
	concurrency := p.segmentConcurrency()
	p.log.Info("fingerprint season analyzing",
		"series", seriesID, "season", season, "episodes", len(srcs),
		"concurrency", concurrency, "low_bitrate", preferLow)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, src := range srcs {
		wg.Add(1)
		go func(i int, src store.SeasonEpisodeSource) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ep := &fpEpisode{src: src, runtime: float64(src.RunTimeTicks) / 10_000_000}
			if ep.runtime <= 0 {
				ep.runtime = p.probeRuntime(src.URL)
			}
			if ep.runtime <= 0 {
				return
			}
			headDur := math.Min(600, ep.runtime)
			ep.head, _ = p.audioEnvelope(src.URL, 0, headDur)
			tailDur := math.Min(600, ep.runtime)
			ep.tailStart = ep.runtime - tailDur
			ep.tail, _ = p.audioEnvelope(src.URL, ep.tailStart, tailDur)
			mu.Lock()
			eps[i] = ep
			mu.Unlock()
		}(i, src)
	}
	wg.Wait()

	valid := make([]*fpEpisode, 0, len(eps))
	for _, ep := range eps {
		if ep != nil && len(ep.head) > 60 && len(ep.tail) > 60 {
			valid = append(valid, ep)
		}
	}
	if len(valid) < 2 {
		return 0, fmt.Errorf("not enough analyzable episodes: %d", len(valid))
	}

	introCands := make([][]fpCandidate, len(valid))
	outroCands := make([][]fpCandidate, len(valid))
	for i := 0; i < len(valid); i++ {
		for j := i + 1; j < len(valid); j++ {
			if a, b, score, ok := findRepeat(valid[i].head, valid[j].head); ok && a.end-a.start >= 15 {
				introCands[i] = append(introCands[i], fpCandidate{partner: j, start: a.start, end: a.end, score: score})
				introCands[j] = append(introCands[j], fpCandidate{partner: i, start: b.start, end: b.end, score: score})
			}
			if a, b, score, ok := findRepeat(valid[i].tail, valid[j].tail); ok && a.end-a.start >= 15 {
				outroCands[i] = append(outroCands[i], fpCandidate{partner: j, start: a.start, end: a.end, score: score})
				outroCands[j] = append(outroCands[j], fpCandidate{partner: i, start: b.start, end: b.end, score: score})
			}
		}
	}

	saved := 0
	for i, ep := range valid {
		// 先算这次的结果，再决定哪些旧片段保留：
		// 章节是权威来源；旧的指纹结果可以被新边界替换；没有新结果时保留旧的。
		intro, hasIntro := consensus(introCands[i], 0, ep.runtime, 15, 600, 0.5, false)
		outro, hasOutro := consensus(outroCands[i], ep.tailStart, ep.runtime, 15, 900, 0.35, true)

		existing, _ := p.db.MediaSegmentsForItem(ep.src.ItemID, nil)
		manual := false
		preserve := make(map[string]bool, len(existing))
		merged := make([]store.MediaSegment, 0, len(existing)+2)
		for _, s := range existing {
			if s.Source == "manual" {
				manual = true
			}
			if s.Source == "fingerprint" {
				if (s.Type == store.SegmentIntro && hasIntro) ||
					(s.Type == store.SegmentOutro && hasOutro) {
					continue // 用这次算出的边界替换
				}
			} else {
				preserve[s.Type] = true
			}
			merged = append(merged, s)
		}
		if manual {
			continue
		}
		added := 0
		if hasIntro && !preserve[store.SegmentIntro] {
			merged = append(merged, store.MediaSegment{
				ItemID: ep.src.ItemID, Type: store.SegmentIntro,
				StartTicks: secondsToTicks(intro.start), EndTicks: secondsToTicks(intro.end),
				Source: "fingerprint", Confidence: 0.7,
			})
			added++
		}
		if hasOutro && !preserve[store.SegmentOutro] {
			merged = append(merged, store.MediaSegment{
				ItemID: ep.src.ItemID, Type: store.SegmentOutro,
				StartTicks: secondsToTicks(outro.start), EndTicks: secondsToTicks(outro.end),
				Source: "fingerprint", Confidence: 0.7,
			})
			added++
		}
		if added == 0 {
			continue
		}
		if err := p.db.ReplaceMediaSegments(ep.src.ItemID, merged, "fingerprint", ""); err != nil {
			p.log.Warn("save fingerprint segments failed", "item", ep.src.ItemID, "err", err)
			continue
		}
		saved++
		p.log.Info("fingerprint segments", "item", ep.src.ItemID,
			"added", added, "total", len(merged), "season", season)
	}
	p.log.Info("fingerprint season done", "series", seriesID, "season", season,
		"episodes", len(valid), "saved", saved)
	return saved, nil
}

// 季中换 OP/ED 时，新片段可能只出现两三集。单伙伴匹配只要
// 30 秒窗口相关度足够高、连续匹配段足够长，也接受。
const (
	singlePartnerMinScore = 0.80
	singlePartnerMinSpan  = 60.0
)

// consensus 把同一集来自不同对手集的候选按开始时间聚类，取支持数最多的簇。
func consensus(cands []fpCandidate, windowStart, runtime, minLen, maxLen, minStartRatio float64, isOutro bool) (fpSpan, bool) {
	if len(cands) == 0 {
		return fpSpan{}, false
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].start < cands[j].start })

	bestSupport := 0
	bestScore := 0.0
	var best fpSpan
	for i := 0; i < len(cands); i++ {
		start, end := cands[i].start, cands[i].end
		support := 1
		score := cands[i].score
		partners := map[int]bool{cands[i].partner: true}
		for j := i + 1; j < len(cands); j++ {
			if math.Abs(cands[j].start-start) <= 6 {
				if !partners[cands[j].partner] {
					partners[cands[j].partner] = true
					support++
				}
				if cands[j].score > score {
					score = cands[j].score
				}
				end = math.Max(end, cands[j].end)
			}
		}
		if support > bestSupport {
			absStart := windowStart + start
			absEnd := windowStart + end
			length := absEnd - absStart
			if length >= minLen && length <= maxLen {
				if isOutro {
					if absStart >= runtime*minStartRatio && absEnd <= runtime+2 {
						bestSupport = support
						bestScore = score
						best = fpSpan{start: absStart, end: math.Min(absEnd, runtime)}
					}
				} else if absStart < runtime*0.5 {
					bestSupport = support
					bestScore = score
					best = fpSpan{start: absStart, end: absEnd}
				}
			}
		}
	}
	if bestSupport >= 2 {
		return best, true
	}
	if bestSupport == 1 && bestScore >= singlePartnerMinScore &&
		best.end-best.start >= singlePartnerMinSpan {
		return best, true
	}
	return fpSpan{}, false
}

// findRepeat 返回两段包络里最长的一段重复区间（各自相对窗口起点，秒）
// 以及该区间所在 lag 的 30 秒窗口相关度。
func findRepeat(a, b []float64) (fpSpan, fpSpan, float64, bool) {
	if len(a) < 60 || len(b) < 60 {
		return fpSpan{}, fpSpan{}, 0, false
	}
	type lagScore struct {
		lag   int
		score float64
	}
	// 用 30 秒窗口的局部相关给每个 lag 打分。整段 600 秒直接算相关会被
	// 不相干的剧情稀释，90 秒的片头只剩 0.1~0.2，永远过不了阈值。
	scores := localLagScores(a, b)
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })

	bestLen := 0.0
	bestScore := 0.0
	bestLag := 0
	var bestA, bestB fpSpan
	for k := 0; k < len(scores) && k < 8; k++ {
		if scores[k].score < 0.7 {
			break
		}
		// 粗搜索步长是 0.5 秒的两帧，两集片尾经常正好差 0.5 秒；
		// 对每个高分 lag 再做 ±1 帧细化，否则会错过正确对齐。
		for _, lag := range []int{scores[k].lag - 1, scores[k].lag, scores[k].lag + 1} {
			sa, sb := longestRun(a, b, lag)
			if sa.end-sa.start > bestLen {
				bestLen = sa.end - sa.start
				bestScore = scores[k].score
				bestLag = lag
				bestA, bestB = sa, sb
			}
		}
	}
	if bestLen < 15 {
		return fpSpan{}, fpSpan{}, 0, false
	}
	// 用匹配段中间 15 秒再校一次 lag：粗匹配可能把半秒偏差带进来，
	// 导致边界回收时停在正片里。
	if bestLen >= 30 {
		lag := refineLag(a, b, bestLag, bestA)
		if sa, sb := longestRun(a, b, lag); sa.end-sa.start >= bestLen-8 {
			bestA, bestB = sa, sb
		}
	}
	return bestA, bestB, bestScore, true
}

// refineLag 在匹配段中心用 15 秒窗口找相关度最高的 lag。
func refineLag(a, b []float64, lag int, span fpSpan) int {
	center := int(span.start + span.end) // 秒换算成 0.5 秒帧后再取中点
	const half = 15                      // 15 秒
	bestLag, bestC := lag, -2.0
	for l := lag - 2; l <= lag+2; l++ {
		i, j := center-half, center-half+l
		if i < 0 || j < 0 || i+2*half > len(a) || j+2*half > len(b) {
			continue
		}
		if c := pearson(a[i:i+2*half], b[j:j+2*half]); c > bestC {
			bestC, bestLag = c, l
		}
	}
	return bestLag
}

func localLagScores(a, b []float64) []struct {
	lag   int
	score float64
} {
	const w = 60      // 30 秒
	const step = 15   // 7.5 秒
	const lagStep = 1 // 0.5 秒：片尾偏移经常是半秒，1 秒步长会整段错过
	out := make([]struct {
		lag   int
		score float64
	}, 0, (len(a)+len(b))/lagStep)
	for lag := -(len(b) - w); lag < len(a)-w; lag += lagStep {
		best := -2.0
		for pos := 0; pos+w <= len(a); pos += step {
			j := pos + lag
			if j < 0 || j+w > len(b) {
				continue
			}
			if c := pearson(a[pos:pos+w], b[j:j+w]); c > best {
				best = c
			}
		}
		if best > -2 {
			out = append(out, struct {
				lag   int
				score float64
			}{lag: lag, score: best})
		}
	}
	return out
}

func pearsonLag(a, b []float64, lag int) float64 {
	i0, j0 := 0, 0
	if lag < 0 {
		i0 = -lag
	} else {
		j0 = lag
	}
	n := len(a) - i0
	if m := len(b) - j0; m < n {
		n = m
	}
	if n < 20 {
		return 0
	}
	return pearson(a[i0:i0+n], b[j0:j0+n])
}

func pearson(x, y []float64) float64 {
	n := len(x)
	var sx, sy float64
	for i := 0; i < n; i++ {
		sx += x[i]
		sy += y[i]
	}
	mx, my := sx/float64(n), sy/float64(n)
	var num, dx, dy float64
	for i := 0; i < n; i++ {
		ax, by := x[i]-mx, y[i]-my
		num += ax * by
		dx += ax * ax
		dy += by * by
	}
	if dx == 0 || dy == 0 {
		return 0
	}
	return num / math.Sqrt(dx*dy)
}

// longestRun 在固定 lag 下找连续的高相关区间。
func longestRun(a, b []float64, lag int) (fpSpan, fpSpan) {
	i0, j0 := 0, 0
	if lag < 0 {
		i0 = -lag
	} else {
		j0 = lag
	}
	n := len(a) - i0
	if m := len(b) - j0; m < n {
		n = m
	}
	// 8 秒窗口。3 秒窗口对音乐太敏感，换气/和声段会掉到阈值以下，
	// 把 90 秒的片尾截成 30 多秒，单伙伴匹配就过不了长度门槛。
	const w = 16
	if n < w {
		return fpSpan{}, fpSpan{}
	}
	good := make([]bool, n)
	for k := 0; k+w <= n; k++ {
		good[k] = pearson(a[i0+k:i0+k+w], b[j0+k:j0+k+w]) > 0.55
	}
	bestStart, bestEnd, curStart := -1, -1, -1
	for k := 0; k < n; k++ {
		if good[k] {
			if curStart < 0 {
				curStart = k
			}
		} else if curStart >= 0 {
			if k-curStart > bestEnd-bestStart {
				bestStart, bestEnd = curStart, k
			}
			curStart = -1
		}
	}
	if curStart >= 0 && n-curStart > bestEnd-bestStart {
		bestStart, bestEnd = curStart, n
	}
	if bestStart < 0 || bestEnd-bestStart < 30 { // 至少 15 秒
		return fpSpan{}, fpSpan{}
	}
	// 向两侧扩展：片头前几秒可能比较安静，3 秒窗口相关会掉到阈值下，
	// 但整段音频仍然相同，不能把开头切掉。
	for bestStart >= 6 {
		if pearson(a[i0+bestStart-6:i0+bestStart], b[j0+bestStart-6:j0+bestStart]) < 0.5 {
			break
		}
		bestStart -= 6
	}
	for bestEnd+6 <= n {
		if pearson(a[i0+bestEnd:i0+bestEnd+6], b[j0+bestEnd:j0+bestEnd+6]) < 0.5 {
			break
		}
		bestEnd += 6
	}
	// 8 秒检测窗会让边界提前几秒。用 4 秒窗把两端收回来：
	// 起点处这 4 秒对不上就往后挪，终点处对不上就往前挪。
	// 否则片尾会从正片最后几秒就开始跳。
	const fw = 8 // 4 秒
	// 阈值要明显高于噪声：lag 有半秒偏差时，错位的 4 秒窗相关度
	// 还能到 0.5 左右，边界会被留在正片里。
	ok := func(k int) bool {
		return k >= 0 && k+fw <= n &&
			pearson(a[i0+k:i0+k+fw], b[j0+k:j0+k+fw]) >= 0.7
	}
	for moved := 0; moved < 32 && bestEnd-bestStart > 12; moved++ {
		if ok(bestStart) {
			break
		}
		bestStart++
	}
	for moved := 0; moved < 32 && bestEnd-bestStart > 12; moved++ {
		if ok(bestEnd - fw) {
			break
		}
		bestEnd--
	}
	return fpSpan{start: float64(i0+bestStart) * 0.5, end: float64(i0+bestEnd) * 0.5},
		fpSpan{start: float64(j0+bestStart) * 0.5, end: float64(j0+bestEnd) * 0.5}
}

// audioEnvelope 取一段音频，压成 0.5 秒 RMS 包络（z-score 归一化）。
func (p *Prober) audioEnvelope(rawURL string, start, dur float64) ([]float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.ffmpegBin,
		"-v", "error",
		"-ss", fmt.Sprintf("%.3f", start),
		"-t", fmt.Sprintf("%.3f", dur),
		"-i", rawURL,
		"-vn", "-ac", "1", "-ar", "8000",
		"-f", "s16le", "-",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	const step = 4000 // 8kHz * 0.5s
	n := len(out) / 2 / step
	if n < 2 {
		return nil, fmt.Errorf("audio too short")
	}
	env := make([]float64, n)
	for i := 0; i < n; i++ {
		var sum float64
		base := i * step * 2
		for j := 0; j < step; j++ {
			v := int16(binary.LittleEndian.Uint16(out[base+j*2:]))
			sum += float64(v) * float64(v)
		}
		env[i] = math.Sqrt(sum / step)
	}
	// z-score，消除音量差异。
	var mean float64
	for _, v := range env {
		mean += v
	}
	mean /= float64(len(env))
	var sd float64
	for _, v := range env {
		sd += (v - mean) * (v - mean)
	}
	sd = math.Sqrt(sd / float64(len(env)))
	if sd == 0 {
		return nil, fmt.Errorf("flat audio")
	}
	for i := range env {
		env[i] = (env[i] - mean) / sd
	}
	return env, nil
}

func (p *Prober) probeRuntime(rawURL string) float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p.bin, "-v", "error",
		"-show_entries", "format=duration", "-of", "default=nw=1:nk=1", rawURL).Output()
	if err != nil {
		return 0
	}
	return parseFloat(string(out))
}
