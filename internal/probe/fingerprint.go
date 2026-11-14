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
	"sync"
	"time"

	"lime/internal/store"
)

// StartSeasonFingerprint 异步分析某一季的片头片尾，返回是否成功启动。
// 同一时间只跑一个季，避免把 NAS 的 CPU 和网盘带宽打满。
func (p *Prober) StartSeasonFingerprint(seriesID string, season int) bool {
	if p == nil || !p.enabled || p.ffmpegBin == "" || seriesID == "" || !p.segmentsOn.Load() {
		return false
	}
	if !p.fpRunning.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer p.fpRunning.Store(false)
		if err := p.analyzeSeason(seriesID, season); err != nil {
			p.log.Warn("fingerprint season failed", "series", seriesID, "season", season, "err", err)
			return
		}
	}()
	return true
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
}

type fpSpan struct{ start, end float64 }

func (p *Prober) analyzeSeason(seriesID string, season int) error {
	srcs, err := p.db.SeasonSources(seriesID, season)
	if err != nil {
		return err
	}
	if len(srcs) < 2 {
		return fmt.Errorf("not enough episodes: %d", len(srcs))
	}
	if len(srcs) > 40 {
		srcs = srcs[:40]
	}

	eps := make([]*fpEpisode, len(srcs))
	sem := make(chan struct{}, 3)
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
		return fmt.Errorf("not enough analyzable episodes: %d", len(valid))
	}

	introCands := make([][]fpCandidate, len(valid))
	outroCands := make([][]fpCandidate, len(valid))
	for i := 0; i < len(valid); i++ {
		for j := i + 1; j < len(valid); j++ {
			if a, b, ok := findRepeat(valid[i].head, valid[j].head); ok && a.end-a.start >= 15 {
				introCands[i] = append(introCands[i], fpCandidate{partner: j, start: a.start, end: a.end})
				introCands[j] = append(introCands[j], fpCandidate{partner: i, start: b.start, end: b.end})
			}
			if a, b, ok := findRepeat(valid[i].tail, valid[j].tail); ok && a.end-a.start >= 15 {
				outroCands[i] = append(outroCands[i], fpCandidate{partner: j, start: a.start, end: a.end})
				outroCands[j] = append(outroCands[j], fpCandidate{partner: i, start: b.start, end: b.end})
			}
		}
	}

	saved := 0
	for i, ep := range valid {
		// 已有章节/手动片段就不覆盖，指纹只补空集。
		if existing, _ := p.db.MediaSegmentsForItem(ep.src.ItemID, nil); len(existing) > 0 {
			continue
		}
		segs := make([]store.MediaSegment, 0, 2)
		if s, ok := consensus(introCands[i], 0, ep.runtime, 15, 600, 0.5, false); ok {
			segs = append(segs, store.MediaSegment{
				ItemID: ep.src.ItemID, Type: store.SegmentIntro,
				StartTicks: secondsToTicks(s.start), EndTicks: secondsToTicks(s.end),
				Source: "fingerprint", Confidence: 0.7,
			})
		}
		if s, ok := consensus(outroCands[i], ep.tailStart, ep.runtime, 15, 900, 0.35, true); ok {
			segs = append(segs, store.MediaSegment{
				ItemID: ep.src.ItemID, Type: store.SegmentOutro,
				StartTicks: secondsToTicks(s.start), EndTicks: secondsToTicks(s.end),
				Source: "fingerprint", Confidence: 0.7,
			})
		}
		if len(segs) == 0 {
			continue
		}
		if err := p.db.ReplaceMediaSegments(ep.src.ItemID, segs, "fingerprint", ""); err != nil {
			p.log.Warn("save fingerprint segments failed", "item", ep.src.ItemID, "err", err)
			continue
		}
		saved++
		p.log.Info("fingerprint segments", "item", ep.src.ItemID,
			"segments", len(segs), "season", season)
	}
	p.log.Info("fingerprint season done", "series", seriesID, "season", season,
		"episodes", len(valid), "saved", saved)
	return nil
}

// consensus 把同一集来自不同对手集的候选按开始时间聚类，取支持数最多的簇。
func consensus(cands []fpCandidate, windowStart, runtime, minLen, maxLen, minStartRatio float64, isOutro bool) (fpSpan, bool) {
	if len(cands) < 2 {
		return fpSpan{}, false
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].start < cands[j].start })

	bestSupport := 0
	var best fpSpan
	for i := 0; i < len(cands); i++ {
		start, end := cands[i].start, cands[i].end
		support := 1
		partners := map[int]bool{cands[i].partner: true}
		for j := i + 1; j < len(cands); j++ {
			if math.Abs(cands[j].start-start) <= 6 {
				if !partners[cands[j].partner] {
					partners[cands[j].partner] = true
					support++
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
						best = fpSpan{start: absStart, end: math.Min(absEnd, runtime)}
					}
				} else if absStart < runtime*0.5 {
					bestSupport = support
					best = fpSpan{start: absStart, end: absEnd}
				}
			}
		}
	}
	if bestSupport < 2 {
		return fpSpan{}, false
	}
	return best, true
}

// findRepeat 返回两段包络里最长的一段重复区间（各自相对窗口起点，秒）。
func findRepeat(a, b []float64) (fpSpan, fpSpan, bool) {
	if len(a) < 60 || len(b) < 60 {
		return fpSpan{}, fpSpan{}, false
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
	var bestA, bestB fpSpan
	for k := 0; k < len(scores) && k < 8; k++ {
		if scores[k].score < 0.7 {
			break
		}
		sa, sb := longestRun(a, b, scores[k].lag)
		if sa.end-sa.start > bestLen {
			bestLen = sa.end - sa.start
			bestA, bestB = sa, sb
		}
	}
	if bestLen < 15 {
		return fpSpan{}, fpSpan{}, false
	}
	return bestA, bestB, true
}

func localLagScores(a, b []float64) []struct {
	lag   int
	score float64
} {
	const w = 60      // 30 秒
	const step = 15   // 7.5 秒
	const lagStep = 2 // 1 秒
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
	const w = 6 // 3 秒
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
