// Package nfo 解析 Kodi / Emby 风格的本地 .nfo 元数据文件。
//
// 媒体目录里常见的布局是：
//
//	作品目录/
//	  xxx.strm
//	  xxx.nfo
//	  poster.jpg
//	  fanart.jpg
//
// 有 .nfo 时优先级高于文件名解析：标题、简介、年份、演职员、海报都从本地取，
// 不再依赖 TMDB 在线刮削（这类内容通常也没有 TMDB ID）。
package nfo

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ticksPerSecond：Jellyfin 的时间单位是 100 纳秒。
const ticksPerSecond = 10_000_000

// Actor 是 NFO 里一个演员/导演条目。
type Actor struct {
	Name string
	Role string
	Type string // Actor / Director
}

// Metadata 是本地 .nfo 里对本项目有用的字段集合。
type Metadata struct {
	Title         string
	OriginalTitle string
	SortTitle     string
	Plot          string
	Outline       string
	Tagline       string
	Year          int
	Premiered     string
	RuntimeMin    int
	Rating        float64
	OfficialRate  string
	Genres        []string
	Tags          []string
	Actors        []Actor
	Directors     []string
	Studios       []string
	TmdbID        string
	ImdbID        string
	Poster        string // NFO 里的 <poster> 文件名
	Thumb         string // <thumb>
	Fanart        string // <fanart>
}

type rawActor struct {
	Name string `xml:"name"`
	Role string `xml:"role"`
	Type string `xml:"type"`
}

type rawUniqueID struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type rawRating struct {
	Name  string  `xml:"name,attr"`
	Value float64 `xml:"value"`
}

// rawNFO 不限定根节点名：movie / episodedetails / tvshow 都能解。
type rawNFO struct {
	Title         string `xml:"title"`
	OriginalTitle string `xml:"originaltitle"`
	SortTitle     string `xml:"sorttitle"`
	Plot          string `xml:"plot"`
	OriginalPlot  string `xml:"originalplot"`
	Outline       string `xml:"outline"`
	Tagline       string `xml:"tagline"`
	Year          int    `xml:"year"`
	Premiered     string `xml:"premiered"`
	ReleaseDate   string `xml:"releasedate"`
	Release       string `xml:"release"`
	Runtime       int    `xml:"runtime"`
	Rating        float64 `xml:"rating"`
	MPAA          string  `xml:"mpaa"`
	CustomRating  string  `xml:"customrating"`
	Genre         []string `xml:"genre"`
	Tag           []string `xml:"tag"`
	Studio        []string `xml:"studio"`
	Maker         string   `xml:"maker"`
	Publisher     string   `xml:"publisher"`
	Director      []string `xml:"director"`
	Actor         []rawActor `xml:"actor"`
	Poster        string     `xml:"poster"`
	Thumb         string     `xml:"thumb"`
	Fanart        string     `xml:"fanart"`
	TmdbIDTag     string     `xml:"tmdbid"`
	UniqueID      []rawUniqueID `xml:"uniqueid"`
	Ratings       []rawRating   `xml:"ratings>rating"`
}

// ParseFile 读取并解析一个 .nfo 文件。
func ParseFile(path string) (*Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse 解析 NFO 内容。解析失败（XML 损坏）时返回错误，由调用方决定是否忽略。
func Parse(data []byte) (*Metadata, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	var raw rawNFO
	dec := xml.NewDecoder(bytes.NewReader(data))
	// 本地 NFO 偶尔带非严格 XML（未转义的 &、大小写混乱的标签），放宽校验。
	dec.Strict = false
	// 不认识的 encoding 声明直接按原字节读取；本项目遇到的 NFO 基本都是 UTF-8。
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}

	m := &Metadata{
		Title:         strings.TrimSpace(raw.Title),
		OriginalTitle: strings.TrimSpace(raw.OriginalTitle),
		SortTitle:     strings.TrimSpace(raw.SortTitle),
		Plot:          strings.TrimSpace(raw.Plot),
		Outline:       strings.TrimSpace(raw.Outline),
		Tagline:       strings.TrimSpace(raw.Tagline),
		Year:          raw.Year,
		Premiered:     firstNonEmpty(raw.Premiered, raw.ReleaseDate, raw.Release),
		RuntimeMin:    raw.Runtime,
		Rating:        raw.Rating,
		OfficialRate:  firstNonEmpty(raw.MPAA, raw.CustomRating),
		Poster:        strings.TrimSpace(raw.Poster),
		Thumb:         strings.TrimSpace(raw.Thumb),
		Fanart:        strings.TrimSpace(raw.Fanart),
	}

	// 简介优先 plot，其次 originalplot，最后 outline。
	if m.Plot == "" {
		m.Plot = strings.TrimSpace(raw.OriginalPlot)
	}
	if m.Plot == "" {
		m.Plot = m.Outline
	}

	m.Genres = cleanGenres(raw.Genre)
	if len(m.Genres) == 0 {
		m.Genres = cleanGenres(raw.Tag)
	}
	if len(m.Genres) > 20 {
		m.Genres = m.Genres[:20]
	}

	if m.Rating == 0 && len(raw.Ratings) > 0 {
		m.Rating = raw.Ratings[0].Value
	}

	for _, a := range raw.Actor {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		typ := strings.TrimSpace(a.Type)
		if typ == "" {
			typ = "Actor"
		}
		m.Actors = append(m.Actors, Actor{
			Name: name,
			Role: strings.TrimSpace(a.Role),
			Type: typ,
		})
	}
	for _, d := range raw.Director {
		if d = strings.TrimSpace(d); d != "" {
			m.Directors = append(m.Directors, d)
		}
	}
	for _, s := range append(raw.Studio, raw.Maker, raw.Publisher) {
		if s = strings.TrimSpace(s); s != "" {
			m.Studios = appendUnique(m.Studios, s)
		}
	}

	for _, uid := range raw.UniqueID {
		switch strings.ToLower(strings.TrimSpace(uid.Type)) {
		case "tmdb":
			m.TmdbID = strings.TrimSpace(uid.Value)
		case "imdb":
			m.ImdbID = strings.TrimSpace(uid.Value)
		}
	}
	if m.TmdbID == "" {
		m.TmdbID = strings.TrimSpace(raw.TmdbIDTag)
	}

	return m, nil
}

// Load 在 dir 里找 stem.nfo 或 movie.nfo。找不到返回 nil。
func Load(dir, stem string) *Metadata {
	for _, name := range []string{stem + ".nfo", "movie.nfo"} {
		p := filepath.Join(dir, name)
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			continue
		}
		m, err := ParseFile(p)
		if err != nil {
			return nil
		}
		return m
	}
	return nil
}

// Overview 返回最合适的简介文本。
func (m *Metadata) Overview() string {
	if m == nil {
		return ""
	}
	if m.Plot != "" {
		return m.Plot
	}
	return m.Outline
}

// PremiereDate 返回首播/发行日期。
func (m *Metadata) PremiereDate() string {
	if m == nil {
		return ""
	}
	return m.Premiered
}

// OfficialRating 返回分级。
func (m *Metadata) OfficialRating() string {
	if m == nil {
		return ""
	}
	return m.OfficialRate
}

// RuntimeTicks 把 NFO 里的分钟数换成 Jellyfin 的 100ns ticks。
func (m *Metadata) RuntimeTicks() int64 {
	if m == nil || m.RuntimeMin <= 0 {
		return 0
	}
	return int64(m.RuntimeMin) * 60 * ticksPerSecond
}

// GenresJSON 输出 items.genres 需要的 JSON 数组。
func (m *Metadata) GenresJSON() string {
	if m == nil || len(m.Genres) == 0 {
		return ""
	}
	b, err := json.Marshal(m.Genres)
	if err != nil {
		return ""
	}
	return string(b)
}

type personJSON struct {
	Name     string `json:"Name"`
	Role     string `json:"Role,omitempty"`
	Type     string `json:"Type"`
	ImageTag string `json:"ImageTag,omitempty"`
}

// PeopleJSON 输出 items.people 需要的 JSON 数组（演员 + 导演）。
func (m *Metadata) PeopleJSON() string {
	if m == nil {
		return ""
	}
	people := make([]personJSON, 0, len(m.Actors)+len(m.Directors))
	for _, a := range m.Actors {
		if len(people) >= 20 {
			break
		}
		people = append(people, personJSON{Name: a.Name, Role: a.Role, Type: "Actor"})
	}
	for _, d := range m.Directors {
		if len(people) >= 20 {
			break
		}
		people = append(people, personJSON{Name: d, Type: "Director"})
	}
	if len(people) == 0 {
		return ""
	}
	b, err := json.Marshal(people)
	if err != nil {
		return ""
	}
	return string(b)
}

// PosterFile 返回本地海报的绝对路径：优先 NFO 的 poster/thumb，
// 其次常见的约定文件名。没有则返回空串。
func (m *Metadata) PosterFile(dir, stem string) string {
	if m != nil {
		if p := localFile(dir, m.Poster); p != "" {
			return p
		}
		if p := localFile(dir, m.Thumb); p != "" {
			return p
		}
	}
	return firstFile(dir,
		stem+"-poster.jpg", stem+"-poster.png",
		"poster.jpg", "poster.png",
		"folder.jpg", "folder.png",
		"cover.jpg", "cover.png",
		stem+".jpg", stem+".png",
		"thumb.jpg", "thumb.png",
	)
}

// BackdropFile 返回本地背景图的绝对路径。
func (m *Metadata) BackdropFile(dir, stem string) string {
	if m != nil {
		if p := localFile(dir, m.Fanart); p != "" {
			return p
		}
	}
	return firstFile(dir,
		"fanart.jpg", "fanart.png",
		"backdrop.jpg", "backdrop.png",
		stem+"-fanart.jpg", stem+"-fanart.png",
		"background.jpg", "background.png",
	)
}

// localFile 把 NFO 里的相对文件名解析成 dir 下的真实文件；URL 忽略。
func localFile(dir, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "://") || strings.HasPrefix(name, "http") {
		return ""
	}
	p := name
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
		return p
	}
	return ""
}

func firstFile(dir string, names ...string) string {
	for _, name := range names {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
			return p
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// cleanGenres 去掉明显的非类型标签（片商/发行），并去重。
func cleanGenres(in []string) []string {
	out := make([]string, 0, len(in))
	for _, g := range in {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if strings.HasPrefix(g, "片商:") || strings.HasPrefix(g, "发行:") {
			continue
		}
		out = appendUnique(out, g)
	}
	return out
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
