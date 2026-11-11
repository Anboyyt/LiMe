// Package tmdb 是 TMDB v3 API 的最小客户端，只做刮削需要的几个端点。
package tmdb

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	PosterSize   = "w780" // 卡片用不到这么大，但详情页/客户端按 maxWidth 请求，服务端会缩放并缓存
	BackdropSize = "original" // 详情页大图按原始分辨率取，w780 会被放大得发糊
	StillSize    = "w185"
	ProfileSize  = "w185"
	LogoSize     = "w500"
)

type Client struct {
	keys      []string
	keyMu     sync.Mutex
	keyIdx    int
	language  string
	fallback  []string
	baseURL   string
	imageBase string
	http      *http.Client
	// img 专门下载图片：HTTP/1.1 + 独立连接池，长跑后不会被 HTTP/2 连接卡死。
	img *http.Client
	log *slog.Logger
}

func New(apiKeys []string, language, baseURL, imageBase, proxy string, log *slog.Logger) *Client {
	var proxyURL *url.URL
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err == nil {
			proxyURL = u
		} else if log != nil {
			log.Warn("invalid tmdb proxy", "proxy", proxy, "err", err)
		}
	}
	tr := tmdbTransport(proxyURL)
	imgTr := tmdbTransport(proxyURL)
	imgTr.MaxIdleConnsPerHost = 16
	imgTr.IdleConnTimeout = 30 * time.Second

	return &Client{
		keys:      sanitizeKeys(apiKeys),
		language:  language,
		fallback:  fallbackLanguages(language),
		baseURL:   strings.TrimRight(baseURL, "/"),
		imageBase: strings.TrimRight(imageBase, "/"),
		http:      &http.Client{Timeout: 25 * time.Second, Transport: tr},
		img:       &http.Client{Timeout: 40 * time.Second, Transport: imgTr},
		log:       log,
	}
}

// fallbackLanguages 返回主语言加备用语言。TMDB 上 zh-CN 的条目经常缺简介
// 或标题，繁体通常有、英文则基本都有。只在这些字段为空时才发备用请求，
// 不会额外增加正常条目的请求数。
func fallbackLanguages(primary string) []string {
	primary = strings.TrimSpace(primary)
	if primary == "" {
		primary = "zh-CN"
	}
	out := []string{primary}
	for _, l := range []string{"zh-TW", "en-US"} {
		if !strings.EqualFold(l, primary) {
			out = append(out, l)
		}
	}
	return out
}

// tmdbTransport 显式构造一个不走 HTTP/2 的 Transport。
//
// 不能用 http.DefaultTransport.Clone()：Clone 会把已经初始化好的 TLSNextProto
// （h2 处理器）一起复制过来，之后再把 ForceAttemptHTTP2 设成 false 也不生效，
// 连接仍然会协商 h2。家里的 mihomo 走负载均衡节点时 h2 不稳，会把 HTTP/2 帧
// 直接回给 HTTP/1.1 客户端，报 "malformed HTTP response \x00\x00\x12\x04..."。
// 空 map（不是 nil）才会真正关掉 h2，nil 会让 Transport 用默认值重新打开。
func tmdbTransport(proxyURL *url.URL) *http.Transport {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	if proxyURL != nil {
		tr.Proxy = http.ProxyURL(proxyURL)
	}
	return tr
}

func sanitizeKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

func (c *Client) Enabled() bool { return c != nil && len(c.keys) > 0 }

// nextKey 轮询取下一个 key，调用方不需要关心数量。
func (c *Client) nextKey() string {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	if len(c.keys) == 0 {
		return ""
	}
	key := c.keys[c.keyIdx%len(c.keys)]
	c.keyIdx++
	return key
}

// KeyCount 返回当前配置的 key 数量。
func (c *Client) KeyCount() int {
	if c == nil {
		return 0
	}
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	return len(c.keys)
}

type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Credit struct {
	Name        string `json:"name"`
	Character   string `json:"character"`
	Order       int    `json:"order"`
	ProfilePath string `json:"profile_path"`
}

type Credits struct {
	Cast []Credit `json:"cast"`
}

// 分级优先按这些地区取：大陆/港台 > 美国 > 英国 > 日本 > 韩国。
// TMDB 上多数影片没有 CN 分级，最终通常落到 US 的 MPAA/TV 分级。
var RatingRegions = []string{"CN", "HK", "TW", "US", "GB", "JP", "KR"}

type CountryRating struct {
	ISO3166 string `json:"iso_3166_1"`
	Rating  string `json:"rating"`
}

type RatingList struct {
	Results []CountryRating `json:"results"`
}

type ReleaseDate struct {
	Certification string `json:"certification"`
}

type CountryRelease struct {
	ISO3166      string        `json:"iso_3166_1"`
	ReleaseDates []ReleaseDate `json:"release_dates"`
}

type ReleaseDateList struct {
	Results []CountryRelease `json:"results"`
}

type Movie struct {
	ID            int             `json:"id"`
	Title         string          `json:"title"`
	OriginalTitle string          `json:"original_title"`
	Overview      string          `json:"overview"`
	ReleaseDate   string          `json:"release_date"`
	VoteAverage   float64         `json:"vote_average"`
	PosterPath    string          `json:"poster_path"`
	BackdropPath  string          `json:"backdrop_path"`
	Runtime       int             `json:"runtime"`
	Genres        []Genre         `json:"genres"`
	Credits       Credits         `json:"credits"`
	ReleaseDates  ReleaseDateList `json:"release_dates"`
	Images        ImageSet        `json:"images"`
}

type TV struct {
	ID             int        `json:"id"`
	Name           string     `json:"name"`
	OriginalName   string     `json:"original_name"`
	Overview       string     `json:"overview"`
	FirstAirDate   string     `json:"first_air_date"`
	VoteAverage    float64    `json:"vote_average"`
	PosterPath     string     `json:"poster_path"`
	BackdropPath   string     `json:"backdrop_path"`
	EpisodeRunTime []int      `json:"episode_run_time"`
	Genres         []Genre    `json:"genres"`
	Credits        Credits    `json:"credits"`
	ContentRatings RatingList `json:"content_ratings"`
	Images         ImageSet   `json:"images"`
}

// Image 是 TMDB images 接口返回的图片条目，目前只用到透明标题图（logo）。
type Image struct {
	FilePath    string  `json:"file_path"`
	ISO6391     string  `json:"iso_639_1"`
	VoteAverage float64 `json:"vote_average"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
}

type ImageSet struct {
	Logos     []Image `json:"logos"`
	Backdrops []Image `json:"backdrops"`
}

// PickBackdrop 选分辨率最高的一张背景图（并列时看评分）。
// 背景图回填要的是清晰度，不按语言挑。
func PickBackdrop(images []Image) string {
	best := ""
	bestW, bestVote := -1, -1.0
	for _, img := range images {
		if img.FilePath == "" {
			continue
		}
		if img.Width > bestW || (img.Width == bestW && img.VoteAverage > bestVote) {
			best = img.FilePath
			bestW = img.Width
			bestVote = img.VoteAverage
		}
	}
	return best
}

// PickLogo 从 TMDB 的 logo 列表里挑一张：优先当前刮削语言（中文），
// 其次英文，再次无语言版本，最后退回任意一张。TMDB 按评分倒序返回。
func PickLogo(logos []Image, language string) string {
	if len(logos) == 0 {
		return ""
	}
	primary := strings.ToLower(language)
	if i := strings.IndexByte(primary, '-'); i > 0 {
		primary = primary[:i]
	}
	wants := make([]string, 0, 3)
	if primary != "" {
		wants = append(wants, primary)
	}
	wants = append(wants, "en", "")

	for _, want := range wants {
		for _, img := range logos {
			if img.FilePath == "" {
				continue
			}
			if strings.ToLower(img.ISO6391) == want {
				return img.FilePath
			}
		}
	}
	for _, img := range logos {
		if img.FilePath != "" {
			return img.FilePath
		}
	}
	return ""
}

// Certification 返回电影分级，按 RatingRegions 优先级挑第一个非空值。
func (m *Movie) Certification() string {
	for _, want := range RatingRegions {
		for _, c := range m.ReleaseDates.Results {
			if c.ISO3166 != want {
				continue
			}
			for _, rd := range c.ReleaseDates {
				if v := strings.TrimSpace(rd.Certification); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// ContentRating 返回剧集分级，逻辑同电影。
func (t *TV) ContentRating() string {
	for _, want := range RatingRegions {
		for _, r := range t.ContentRatings.Results {
			if r.ISO3166 == want {
				if v := strings.TrimSpace(r.Rating); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

type Season struct {
	SeasonNumber int       `json:"season_number"`
	Name         string    `json:"name"`
	Overview     string    `json:"overview"`
	PosterPath   string    `json:"poster_path"`
	AirDate      string    `json:"air_date"`
	Episodes     []Episode `json:"episodes"`
}

type Episode struct {
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	AirDate       string `json:"air_date"`
	StillPath     string `json:"still_path"`
	Runtime       int    `json:"runtime"`
}

func (c *Client) Movie(ctx context.Context, id string) (*Movie, error) {
	var out Movie
	if err := c.get(ctx, "/movie/"+url.PathEscape(id), movieParams, &out); err != nil {
		return nil, err
	}
	c.fillMovie(ctx, id, &out)
	return &out, nil
}

func (c *Client) TVShow(ctx context.Context, id string) (*TV, error) {
	var out TV
	if err := c.get(ctx, "/tv/"+url.PathEscape(id), tvParams, &out); err != nil {
		return nil, err
	}
	c.fillTV(ctx, id, &out)
	return &out, nil
}

// fillMovie 用备用语言补空标题和空简介。主语言字段齐全时直接返回，
// 不产生额外请求。备用请求不带 append_to_response，响应更小。
func (c *Client) fillMovie(ctx context.Context, id string, m *Movie) {
	if m.Title != "" && m.Overview != "" {
		return
	}
	path := "/movie/" + url.PathEscape(id)
	for _, lang := range c.fallback[1:] {
		var alt Movie
		if err := c.getLang(ctx, path, nil, &alt, lang); err != nil {
			continue
		}
		if m.Title == "" && alt.Title != "" {
			m.Title = alt.Title
		}
		if m.Overview == "" && alt.Overview != "" {
			m.Overview = alt.Overview
		}
		if m.Title != "" && m.Overview != "" {
			return
		}
	}
}

// fillTV 同 fillMovie，字段名是 Name。
func (c *Client) fillTV(ctx context.Context, id string, t *TV) {
	if t.Name != "" && t.Overview != "" {
		return
	}
	path := "/tv/" + url.PathEscape(id)
	for _, lang := range c.fallback[1:] {
		var alt TV
		if err := c.getLang(ctx, path, nil, &alt, lang); err != nil {
			continue
		}
		if t.Name == "" && alt.Name != "" {
			t.Name = alt.Name
		}
		if t.Overview == "" && alt.Overview != "" {
			t.Overview = alt.Overview
		}
		if t.Name != "" && t.Overview != "" {
			return
		}
	}
}

// fillSeason 补季名、季简介和缺失的单集名。单集名在 TMDB 上常常是
// "第 N 集" 的空壳，备用语言里有时能拿到真正的小标题。
func (c *Client) fillSeason(ctx context.Context, tvID string, season int, s *Season) {
	if s.Name != "" && s.Overview != "" {
		return
	}
	path := fmt.Sprintf("/tv/%s/season/%d", url.PathEscape(tvID), season)
	for _, lang := range c.fallback[1:] {
		var alt Season
		if err := c.getLang(ctx, path, nil, &alt, lang); err != nil {
			continue
		}
		if s.Name == "" && alt.Name != "" {
			s.Name = alt.Name
		}
		if s.Overview == "" && alt.Overview != "" {
			s.Overview = alt.Overview
		}
		if s.Name != "" && s.Overview != "" {
			return
		}
	}
}

var (
	movieParams = url.Values{
		"append_to_response":     {"credits,release_dates,images"},
		"include_image_language": {"zh,en,null"},
	}
	tvParams = url.Values{
		"append_to_response":     {"credits,content_ratings,images"},
		"include_image_language": {"zh,en,null"},
	}
	imageParams = url.Values{"include_image_language": {"zh,en,null"}}
)

// MovieImages/TVImages 只拉图片列表，给老条目补 logo 时用，避免重复取 credits。
func (c *Client) MovieImages(ctx context.Context, id string) ([]Image, error) {
	set, err := c.MovieImageSet(ctx, id)
	if err != nil {
		return nil, err
	}
	return set.Logos, nil
}

func (c *Client) TVImages(ctx context.Context, id string) ([]Image, error) {
	set, err := c.TVImageSet(ctx, id)
	if err != nil {
		return nil, err
	}
	return set.Logos, nil
}

// MovieImageSet/TVImageSet 返回完整图片集（logo + backdrop），背景图回填用。
func (c *Client) MovieImageSet(ctx context.Context, id string) (*ImageSet, error) {
	var out ImageSet
	if err := c.get(ctx, "/movie/"+url.PathEscape(id)+"/images", imageParams, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) TVImageSet(ctx context.Context, id string) (*ImageSet, error) {
	var out ImageSet
	if err := c.get(ctx, "/tv/"+url.PathEscape(id)+"/images", imageParams, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MoviePoster/TVPoster 只取详情里的 poster_path，用来把老海报按当前语言
// 重新下一张大图。不带 append_to_response，响应只有几 KB。
func (c *Client) MoviePoster(ctx context.Context, id string) (string, error) {
	var out struct {
		PosterPath string `json:"poster_path"`
	}
	if err := c.get(ctx, "/movie/"+url.PathEscape(id), nil, &out); err != nil {
		return "", err
	}
	return out.PosterPath, nil
}

func (c *Client) TVPoster(ctx context.Context, id string) (string, error) {
	var out struct {
		PosterPath string `json:"poster_path"`
	}
	if err := c.get(ctx, "/tv/"+url.PathEscape(id), nil, &out); err != nil {
		return "", err
	}
	return out.PosterPath, nil
}

func (c *Client) SeasonDetail(ctx context.Context, tvID string, season int) (*Season, error) {
	var out Season
	path := fmt.Sprintf("/tv/%s/season/%d", url.PathEscape(tvID), season)
	if err := c.get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	c.fillSeason(ctx, tvID, season, &out)
	return &out, nil
}

func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	return c.getLang(ctx, path, params, out, c.language)
}

// getLang 用指定语言请求。备用语言只补空字段，正常路径始终走 c.language。
func (c *Client) getLang(ctx context.Context, path string, params url.Values, out any, language string) error {
	if !c.Enabled() {
		return errors.New("tmdb api key not configured")
	}

	var lastErr error
	// 轮询 key；429/401 时换下一个，最多试一圈。
	for attempt := 0; attempt < c.KeyCount(); attempt++ {
		q := url.Values{}
		for k, vs := range params {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		q.Set("api_key", c.nextKey())
		q.Set("language", language)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			c.baseURL+path+"?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return json.Unmarshal(body, out)
		}

		if resp.StatusCode == http.StatusNotFound {
			return &NotFoundError{Status: resp.Status, Body: strings.TrimSpace(firstN(string(body), 512))}
		}
		lastErr = fmt.Errorf("tmdb %s: %s", resp.Status, strings.TrimSpace(firstN(string(body), 512)))
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusUnauthorized {
			return lastErr
		}
		if c.log != nil {
			c.log.Warn("tmdb key rejected, trying next", "status", resp.StatusCode)
		}
	}
	return lastErr
}

// NotFoundError 表示 TMDB 返回 404：这个 ID / 季在 TMDB 上不存在。
// 刮削遇到它应该跳过并停止重试，而不是每天记录一条错误。
type NotFoundError struct {
	Status string
	Body   string
}

func (e *NotFoundError) Error() string { return "tmdb " + e.Status + ": " + e.Body }

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// DownloadImage 下载图片到 dir，返回相对路径（形如 images/xxx.jpg）。
// 同一张图已存在时直接复用。
func (c *Client) DownloadImage(ctx context.Context, imagePath, size, dir string) (string, error) {
	if imagePath == "" {
		return "", nil
	}

	name := imageName(size+imagePath, imageExt(imagePath))
	rel := filepath.ToSlash(filepath.Join("images", name))
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if st, err := os.Stat(full); err == nil && st.Size() > 0 {
		return rel, nil
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}

	u := c.imageBase + "/" + size + imagePath
	// 长跑后偶发连接卡死：图片单独用 HTTP/1.1 客户端，失败时丢连接重试。
	var resp *http.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if ctx.Err() != nil {
				break
			}
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if rerr != nil {
			return "", rerr
		}
		resp, err = c.img.Do(req)
		if err == nil {
			break
		}
		c.img.CloseIdleConnections()
	}
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tmdb image %s", resp.Status)
	}

	tmp, err := os.CreateTemp(dir, ".img-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 20<<20)); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, full); err != nil {
		return "", err
	}
	return rel, nil
}

// imageExt 保留 PNG/SVG 后缀：logo 依赖透明通道，不能存成 .jpg
// （http.ServeFile 按后缀给 Content-Type，JPEG 也会丢 alpha）。
func imageExt(imagePath string) string {
	switch strings.ToLower(filepath.Ext(imagePath)) {
	case ".png":
		return ".png"
	case ".svg":
		return ".svg"
	}
	return ".jpg"
}

func imageName(key, ext string) string {
	sum := sha1.Sum([]byte(key))
	return hex.EncodeToString(sum[:])[:24] + ext
}
