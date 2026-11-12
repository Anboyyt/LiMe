package library

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lime/internal/store"
	"lime/internal/strm"
)

// 没有 TMDB ID 的电影按目录归并：同一目录里的多个 .strm 是同一部片的不同文件，
// 不能因为文件名不同就各成一个条目。
func TestMovieIdentityKeyMergesSameDir(t *testing.T) {
	same1 := movieIdentityKey(strm.Metadata{}, filepath.Join("艺术", "演员", "YUJ-057", "YUJ-057-C.strm"), "yuj-057-c")
	same2 := movieIdentityKey(strm.Metadata{}, filepath.Join("艺术", "演员", "YUJ-057", "YUJ-057.strm"), "yuj-057")
	if same1 != same2 {
		t.Fatalf("同目录应归并: %q != %q", same1, same2)
	}
	other := movieIdentityKey(strm.Metadata{}, filepath.Join("艺术", "演员", "YUJ-058", "YUJ-058.strm"), "yuj-058")
	if same1 == other {
		t.Fatalf("不同目录不应归并: %q", same1)
	}
	// 有 TMDB ID 时仍以它为准，多版本目录照样合并。
	withTmdb := movieIdentityKey(strm.Metadata{TmdbID: "123"}, filepath.Join("电影", "A (2020) {tmdb-123}", "1080p", "a.strm"), "a")
	if withTmdb != "tmdb:123" {
		t.Fatalf("tmdb key = %q", withTmdb)
	}
	// 库根目录下的孤立文件没有目录可用，退回标题#年份。
	if got := movieIdentityKey(strm.Metadata{Year: 2020}, "x.strm", "x"); got != "x#2020" {
		t.Fatalf("root fallback = %q", got)
	}
}

func TestVersionRankPrefersBest(t *testing.T) {
	order := []strm.Metadata{
		{Height: 2160, VideoRange: "DoVi"},
		{Height: 2160, VideoRange: "HDR10+"},
		{Height: 2160, VideoRange: "HDR10"},
		{Height: 2160, VideoRange: "HDR"},
		{Height: 2160, VideoRange: "SDR"},
		{Height: 1080, VideoRange: "HDR"},
		{Height: 1080, VideoRange: "SDR"},
		{Height: 720, VideoRange: "SDR"},
	}
	for i := 1; i < len(order); i++ {
		if versionRank(order[i-1]) >= versionRank(order[i]) {
			t.Fatalf("rank order broken at %d: %d >= %d",
				i, versionRank(order[i-1]), versionRank(order[i]))
		}
	}
}

func TestScanSmallLibrary(t *testing.T) {
	root := t.TempDir()

	writeStrm(t, filepath.Join(root, "电影", "逐爱天堂 Angel (2007) {tmdb-11801}",
		"逐爱天堂 Angel (2007) {tmdb-11801} - 1080p",
		"逐爱天堂.Angel.2007.1080p.25fps.SDR.H.265.8bit.AAC2.0.strm"),
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=111&size=1000&file_name=a.mp4")

	writeStrm(t, filepath.Join(root, "电影", "逐爱天堂 Angel (2007) {tmdb-11801}",
		"逐爱天堂 Angel (2007) {tmdb-11801} - 2160p",
		"逐爱天堂.Angel.2007.2160p.25fps.SDR.H.265.8bit.AAC2.0.strm"),
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=222&size=2000&file_name=b.mp4")

	writeStrm(t, filepath.Join(root, "剧集", "二炮手 (2014) {tmdb-95750}", "Season 1",
		"二炮手.2014.S01E03.1080p.25fps.SDR.H.264.8bit.AAC2.0.strm"),
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=333&size=3000&file_name=c.mp4")

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertLibrary("test", root, "auto"); err != nil {
		t.Fatal(err)
	}

	sc := New(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	count, err := sc.ScanAll()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 3 {
		t.Fatalf("scanned %d, want 3", count)
	}

	libs, _ := db.ListLibraries()
	items, total, err := db.QueryItems(store.ItemQuery{LibraryID: libs[0].ID, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	// 期望：1 部电影（两个版本合并）+ 1 部剧 + 1 季 + 1 集 = 4
	if total != 4 {
		for _, it := range items {
			t.Logf("item: type=%s name=%s", it.Type, it.Name)
		}
		t.Fatalf("total = %d, want 4", total)
	}

	for _, it := range items {
		if it.Type == store.TypeMovie {
			sources, err := db.MediaSourcesByItem(it.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(sources) != 2 {
				t.Fatalf("movie sources = %d, want 2", len(sources))
			}
			if sources[0].Container != "mp4" {
				t.Fatalf("container = %q, want mp4", sources[0].Container)
			}
			if !strings.Contains(sources[0].VersionName, "2160p") ||
				!strings.Contains(sources[1].VersionName, "1080p") {
				t.Fatalf("version names = %q / %q", sources[0].VersionName, sources[1].VersionName)
			}
			if sources[0].SortRank >= sources[1].SortRank {
				t.Fatalf("2160p rank %d should be before 1080p rank %d",
					sources[0].SortRank, sources[1].SortRank)
			}
		}
	}
}

// 入库时间要用 STRM 文件的真实修改时间，不能用扫描时刻，
// 否则整库扫描的条目会挤在同一秒，首页「最近添加」等于随机顺序。
func TestScanUsesSTRMMtimeForCreatedAt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "电影", "片名 (2020) {tmdb-1}", "片名.2020.strm")
	writeStrm(t, path,
		"http://192.168.1.155:16868/strm/123/drive?drive_id=6&file_id=1&size=10&file_name=a.mp4")

	mtime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.Local)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertLibrary("test", root, "auto"); err != nil {
		t.Fatal(err)
	}

	sc := New(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if _, err := sc.ScanAll(); err != nil {
		t.Fatal(err)
	}

	libs, _ := db.ListLibraries()
	items, _, err := db.QueryItems(store.ItemQuery{LibraryID: libs[0].ID, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, it := range items {
		if it.Type != store.TypeMovie {
			continue
		}
		found = true
		if got := it.CreatedAt.Unix(); got != mtime.Unix() {
			t.Fatalf("created_at = %d, want %d (strm mtime)", got, mtime.Unix())
		}
	}
	if !found {
		t.Fatal("movie item not found")
	}
}

// 同目录 .nfo 应覆盖文件名解析：标题、简介、时长、类型、演员和本地海报都从 NFO 取。
func TestScanUsesLocalNFO(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "艺术", "瀬戸環奈", "SONE-846")
	writeStrm(t, filepath.Join(dir, "SONE-846.strm"),
		"http://192.168.1.155:16868/strm/115/drive?drive_id=1&file_id=9&size=1000&file_name=x.mp4")

	nfoBody := `<?xml version="1.0" encoding="UTF-8" ?>
<movie>
  <title>SONE-846 本地标题</title>
  <originaltitle>SONE-846 Original</originaltitle>
  <plot>本地简介</plot>
  <year>2025</year>
  <runtime>172</runtime>
  <mpaa>JP-18+</mpaa>
  <genre>剧情</genre>
  <actor><name>瀬戸環奈</name><type>Actor</type></actor>
  <poster>poster.jpg</poster>
  <fanart>fanart.jpg</fanart>
  <premiered>2025-09-08</premiered>
</movie>`
	if err := os.WriteFile(filepath.Join(dir, "SONE-846.nfo"), []byte(nfoBody), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"poster.jpg", "fanart.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("img"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertLibrary("test", root, "movies"); err != nil {
		t.Fatal(err)
	}

	sc := New(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if _, err := sc.ScanAll(); err != nil {
		t.Fatal(err)
	}

	libs, _ := db.ListLibraries()
	items, _, err := db.QueryItems(store.ItemQuery{LibraryID: libs[0].ID, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	var movie *store.Item
	for _, it := range items {
		if it.Type == store.TypeMovie {
			movie = it
		}
	}
	if movie == nil {
		t.Fatal("movie item not found")
	}
	if movie.Name != "SONE-846 本地标题" {
		t.Fatalf("name = %q", movie.Name)
	}
	if movie.OriginalTitle != "SONE-846 Original" {
		t.Fatalf("original title = %q", movie.OriginalTitle)
	}
	if movie.Overview != "本地简介" || movie.ProductionYear != 2025 {
		t.Fatalf("overview/year = %q %d", movie.Overview, movie.ProductionYear)
	}
	if movie.RunTimeTicks != 172*60*10_000_000 {
		t.Fatalf("runtime ticks = %d", movie.RunTimeTicks)
	}
	if movie.OfficialRating != "JP-18+" {
		t.Fatalf("official rating = %q", movie.OfficialRating)
	}
	if !strings.Contains(movie.Genres, "剧情") || !strings.Contains(movie.People, "瀬戸環奈") {
		t.Fatalf("genres/people = %q / %q", movie.Genres, movie.People)
	}
	if !strings.HasPrefix(movie.ImagePath, "local:") || !strings.HasSuffix(movie.ImagePath, "poster.jpg") {
		t.Fatalf("image path = %q", movie.ImagePath)
	}
	if !strings.HasPrefix(movie.BackdropPath, "local:") || !strings.HasSuffix(movie.BackdropPath, "fanart.jpg") {
		t.Fatalf("backdrop path = %q", movie.BackdropPath)
	}
	if movie.ScrapedAt == 0 {
		t.Fatal("scraped_at should be set for local metadata")
	}
}

// 没有 NFO 也要识别目录里的 poster.jpg/fanart.jpg。
func TestScanLocalImagesWithoutNFO(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "作品", "MIDA-213")
	writeStrm(t, filepath.Join(dir, "MIDA-213.strm"),
		"http://192.168.1.155:16868/strm/115/drive?drive_id=1&file_id=7&size=100&file_name=x.mp4")
	for _, name := range []string{"poster.jpg", "fanart.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("img"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertLibrary("test", root, "movies"); err != nil {
		t.Fatal(err)
	}
	sc := New(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if _, err := sc.ScanAll(); err != nil {
		t.Fatal(err)
	}

	libs, _ := db.ListLibraries()
	items, _, err := db.QueryItems(store.ItemQuery{LibraryID: libs[0].ID, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Type != store.TypeMovie {
			continue
		}
		if !strings.HasSuffix(it.ImagePath, "poster.jpg") || !strings.HasSuffix(it.BackdropPath, "fanart.jpg") {
			t.Fatalf("image/backdrop = %q / %q", it.ImagePath, it.BackdropPath)
		}
		return
	}
	t.Fatal("movie item not found")
}

// 同一目录里文件名不同（-C 后缀 / cd1、cd2）的两个 .strm 应归并成一个条目、两个片源。
// 修复前它们会各成一个条目，但共用同目录的 poster.jpg，UI 上就是两张一模一样的卡片。
func TestScanMergesSameFolderSTRMs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "艺术", "演员", "YUJ-057")
	writeStrm(t, filepath.Join(dir, "YUJ-057-C.strm"),
		"http://192.168.1.155:16868/strm/115/drive?drive_id=1&file_id=11&size=1000&file_name=a.mp4")
	writeStrm(t, filepath.Join(dir, "YUJ-057.strm"),
		"http://192.168.1.155:16868/strm/115/drive?drive_id=1&file_id=12&size=1000&file_name=a.mp4")
	if err := os.WriteFile(filepath.Join(dir, "poster.jpg"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertLibrary("test", root, "movies"); err != nil {
		t.Fatal(err)
	}

	sc := New(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if _, err := sc.ScanAll(); err != nil {
		t.Fatal(err)
	}

	libs, _ := db.ListLibraries()
	items, _, err := db.QueryItems(store.ItemQuery{LibraryID: libs[0].ID, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	var movies []*store.Item
	for _, it := range items {
		if it.Type == store.TypeMovie {
			movies = append(movies, it)
		}
	}
	if len(movies) != 1 {
		t.Fatalf("movies = %d, want 1", len(movies))
	}
	n, err := db.CountSourcesForItem(movies[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("sources = %d, want 2", n)
	}
}

func writeStrm(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
