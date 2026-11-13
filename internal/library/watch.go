package library

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"lime/internal/store"
)

// Watcher 模拟 Emby 的实时媒体库监控：目录里出现新的 .strm 就增量解析，
// 不依赖 PanSave 的入库通知，也不需要遍历整个库。
type Watcher struct {
	db       *store.DB
	scanner  *Scanner
	log      *slog.Logger
	debounce time.Duration
	onIngest func([]string)

	// roots 是当前库路径快照，用来判断事件是否落在库内：
	// 库路径的父目录也会被 watch，那里的其它目录不能整棵加进来。
	rootsMu sync.Mutex
	roots   []string

	// applyMu 让定时刷新和启动对账串行，避免同时写片源表。
	applyMu sync.Mutex

	warnedLimit bool
}

// NewWatcher 创建实时监控。debounce <= 0 时默认 2 秒；
// onIngest 在增量解析成功后回调，参数是受影响的条目 ID（用于精准刮削）。
func NewWatcher(db *store.DB, scanner *Scanner, log *slog.Logger, debounce time.Duration, onIngest func([]string)) *Watcher {
	if debounce <= 0 {
		debounce = 2 * time.Second
	}
	return &Watcher{
		db:       db,
		scanner:  scanner,
		log:      log,
		debounce: debounce,
		onIngest: onIngest,
	}
}

// Run 阻塞运行，直到 ctx 取消。启动时递归给所有库目录加 watch，
// 之后按 debounce 窗口批量解析变化的 .strm。
func (w *Watcher) Run(ctx context.Context) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		w.log.Error("realtime monitor init failed", "err", err)
		return
	}
	defer fsw.Close()

	roots := w.libraryRoots()
	w.setRoots(roots)
	dirs := 0
	for _, root := range roots {
		dirs += w.addTree(fsw, root)
	}
	// 库路径的父目录也要 watch：库整体被删除/改名时，只有父目录能收到事件。
	w.addParentWatches(fsw, roots)
	w.log.Info("realtime monitor watching", "roots", len(roots), "dirs", dirs)

	// 启动后补一次对账，把停机期间漏掉的增删补上。
	go w.reconcile(ctx)

	// 新库可能是运行中从面板加的，定期补 watch。
	reload := time.NewTicker(time.Minute)
	defer reload.Stop()

	ticker := time.NewTicker(w.debounce)
	defer ticker.Stop()

	pending := make(map[string]struct{})
	removed := make(map[string]struct{})
	// 扫描进行中时的事件不丢，攒到扫描结束后再处理。
	deferred := make(map[string]struct{})
	deferredRemoved := make(map[string]struct{})
	watchedRoots := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		watchedRoots[root] = struct{}{}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(fsw, ev, pending, removed)
		case err, ok := <-fsw.Errors:
			if ok {
				w.log.Warn("realtime monitor error", "err", err)
			}
		case <-reload.C:
			current := w.libraryRoots()
			w.setRoots(current)
			for _, root := range current {
				if _, seen := watchedRoots[root]; seen {
					continue
				}
				watchedRoots[root] = struct{}{}
				w.addTree(fsw, root)
			}
			w.addParentWatches(fsw, current)
		case <-ticker.C:
			for p := range pending {
				deferred[p] = struct{}{}
			}
			clear(pending)
			for p := range removed {
				deferredRemoved[p] = struct{}{}
			}
			clear(removed)

			if w.scanner.Status().Running {
				if len(deferred)+len(deferredRemoved) > 0 {
					w.log.Debug("realtime events deferred: scan running",
						"ingest", len(deferred), "removed", len(deferredRemoved))
				}
				continue
			}
			w.applyMu.Lock()
			if len(deferred) > 0 {
				paths := mapKeys(deferred)
				if _, ok := w.refresh(paths); ok {
					clear(deferred)
				}
			}
			if len(deferredRemoved) > 0 {
				n, err := w.scanner.RemoveMissing(mapKeys(deferredRemoved))
				if err != nil {
					w.log.Warn("realtime remove failed", "err", err)
				} else if n > 0 {
					w.log.Info("realtime removed", "paths", len(deferredRemoved), "sources", n)
				}
				clear(deferredRemoved)
			}
			w.applyMu.Unlock()
		}
	}
}

func mapKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (w *Watcher) libraryRoots() []string {
	libs, err := w.db.ListLibraries()
	if err != nil {
		w.log.Error("realtime monitor: list libraries", "err", err)
		return nil
	}
	seen := make(map[string]struct{}, len(libs))
	roots := make([]string, 0, len(libs))
	for _, lib := range libs {
		paths := lib.Paths
		if len(paths) == 0 && lib.Path != "" {
			paths = []string{lib.Path}
		}
		for _, p := range paths {
			p = filepath.Clean(p)
			if p == "" || p == "." {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			roots = append(roots, p)
		}
	}
	return roots
}

func (w *Watcher) setRoots(roots []string) {
	cp := append([]string(nil), roots...)
	w.rootsMu.Lock()
	w.roots = cp
	w.rootsMu.Unlock()
}

// inLibrary 判断路径是否落在某个库路径内。父目录里与库无关的新目录
// 不能整棵加 watch，否则会白收一大堆事件、还会把库外文件扫一遍。
func (w *Watcher) inLibrary(path string) bool {
	p := filepath.ToSlash(filepath.Clean(path))
	w.rootsMu.Lock()
	roots := w.roots
	w.rootsMu.Unlock()
	for _, root := range roots {
		r := strings.TrimRight(filepath.ToSlash(filepath.Clean(root)), "/")
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// addParentWatches 给每个库路径的父目录加 watch。库路径被整体删除或
// 改名时，只有父目录能收到事件；库内部的子目录本来就在递归 watch 里。
func (w *Watcher) addParentWatches(fsw *fsnotify.Watcher, roots []string) {
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		parent := filepath.Dir(filepath.Clean(root))
		if parent == root || parent == "." || parent == string(filepath.Separator) {
			continue
		}
		if _, ok := seen[parent]; ok {
			continue
		}
		seen[parent] = struct{}{}
		if st, err := os.Stat(parent); err != nil || !st.IsDir() {
			continue
		}
		if err := fsw.Add(parent); err != nil {
			w.warnWatchLimit(err)
		}
	}
}

// reconcile 在启动后对账一次，补齐容器停机期间新增/删除的 STRM；
// 停机期间的 inotify 事件是收不到的，只能靠这次比对。
//
// 关键保护：库根目录不存在时直接跳过。媒体盘没挂上时不能把整库片源清空。
func (w *Watcher) reconcile(ctx context.Context) {
	if !reconcileEnabled() {
		w.log.Info("realtime reconcile disabled")
		return
	}
	// 启动扫描期间先等它结束，避免两边同时写片源表。
	for i := 0; w.scanner.Status().Running; i++ {
		if i >= 120 {
			w.log.Info("realtime reconcile skipped: initial scan still running")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}

	start := time.Now()
	roots := w.libraryRoots()
	var files, removed, added int
	for _, root := range roots {
		if ctx.Err() != nil {
			return
		}
		st, err := os.Stat(root)
		if err != nil || !st.IsDir() {
			w.log.Warn("realtime reconcile: library root unavailable, skipped", "root", root)
			continue
		}

		disk := make(map[string]struct{}, 4096)
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != root && skipDirName(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if isSTRM(path) {
				disk[path] = struct{}{}
			}
			return nil
		}); err != nil {
			w.log.Warn("realtime reconcile: walk failed", "root", root, "err", err)
			continue
		}
		files += len(disk)

		dbPaths, err := w.db.SourcePathsUnder(root)
		if err != nil {
			w.log.Warn("realtime reconcile: query failed", "root", root, "err", err)
			continue
		}
		if len(dbPaths) == 0 {
			// 空库交给启动扫描，这里不重复入库。
			continue
		}

		known := make(map[string]struct{}, len(dbPaths))
		gone := make([]string, 0)
		for _, p := range dbPaths {
			known[p] = struct{}{}
			if _, ok := disk[p]; !ok {
				gone = append(gone, p)
			}
		}
		fresh := make([]string, 0)
		for p := range disk {
			if _, ok := known[p]; !ok {
				fresh = append(fresh, p)
			}
		}

		for i := 0; i < len(gone); i += 500 {
			if ctx.Err() != nil {
				return
			}
			end := min(i+500, len(gone))
			w.applyMu.Lock()
			n, err := w.scanner.RemoveMissing(gone[i:end])
			w.applyMu.Unlock()
			if err != nil {
				w.log.Warn("realtime reconcile: remove failed", "root", root, "err", err)
			}
			removed += n
		}
		for i := 0; i < len(fresh); i += 500 {
			if ctx.Err() != nil {
				return
			}
			end := min(i+500, len(fresh))
			w.applyMu.Lock()
			n, ok := w.refresh(fresh[i:end])
			w.applyMu.Unlock()
			if !ok {
				w.log.Info("realtime reconcile paused: scan running")
				break
			}
			added += n
		}
	}
	w.log.Info("realtime reconcile done", "roots", len(roots), "files", files,
		"removed", removed, "added", added, "dur", time.Since(start).String())
}

func reconcileEnabled() bool {
	v := strings.TrimSpace(os.Getenv("LIME_REALTIME_RECONCILE"))
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return true
	}
	return b
}

// addTree 递归给目录树加 watch，返回成功添加的目录数。
func (w *Watcher) addTree(fsw *fsnotify.Watcher, root string) int {
	dirs := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && skipDirName(d.Name()) {
			return filepath.SkipDir
		}
		if err := fsw.Add(path); err != nil {
			w.warnWatchLimit(err)
			return nil
		}
		dirs++
		if dirs%5000 == 0 {
			w.log.Info("realtime monitor progress", "root", root, "dirs", dirs)
		}
		return nil
	})
	return dirs
}

func (w *Watcher) warnWatchLimit(err error) {
	if w.warnedLimit {
		return
	}
	w.warnedLimit = true
	w.log.Warn("realtime monitor: cannot watch directory; falling back to scheduled scans",
		"err", err, "hint", "raise fs.inotify.max_user_watches if this is ENOSPC")
}

func (w *Watcher) handleEvent(fsw *fsnotify.Watcher, ev fsnotify.Event, pending, removed map[string]struct{}) {
	if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		// 文件或目录被删/改名：稍后确认路径真的不在了再清理。
		// Rename 同时可能对应新路径的 Create，新路径走下面的逻辑。
		removed[ev.Name] = struct{}{}
	}

	// 新建目录：补 watch，并把已经写进去的 .strm 抓出来。
	// 目录本身不参与解析。
	if ev.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() && w.inLibrary(ev.Name) {
			w.addTree(fsw, ev.Name)
			w.queueTree(ev.Name, pending)
			return
		}
	}

	if !isSTRM(ev.Name) {
		return
	}
	// Chmod 也收：部分同步工具只改 mtime（touch/utimensat），
	// 不处理的话实时监控会漏掉这类更新。
	if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Chmod) != 0 {
		pending[ev.Name] = struct{}{}
	}
}

func (w *Watcher) queueTree(root string, pending map[string]struct{}) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && skipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isSTRM(path) {
			pending[path] = struct{}{}
		}
		return nil
	})
}

// refresh 返回成功处理的条数和 ok。ok 为 false 表示扫描正在跑，
// 调用方应保留路径等下一轮；解析失败也返回 ok，避免坏文件无限重试。
func (w *Watcher) refresh(paths []string) (int, bool) {
	if w.scanner.Status().Running {
		return 0, false
	}
	n, ids, err := w.scanner.RefreshFilesWithIDs(paths)
	if err != nil {
		w.log.Error("realtime ingest failed", "paths", len(paths), "err", err)
		return 0, true
	}
	if n <= 0 {
		return 0, true
	}
	w.log.Info("realtime ingest", "paths", len(paths), "changed", n)
	if w.onIngest != nil && len(ids) > 0 {
		w.onIngest(ids)
	}
	return n, true
}

func isSTRM(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".strm")
}

func skipDirName(name string) bool {
	return strings.HasPrefix(name, ".") || name == "@eaDir"
}
