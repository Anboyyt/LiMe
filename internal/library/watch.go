package library

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	dirs := 0
	for _, root := range roots {
		dirs += w.addTree(fsw, root)
	}
	w.log.Info("realtime monitor watching", "roots", len(roots), "dirs", dirs)

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
			for _, root := range w.libraryRoots() {
				if _, seen := watchedRoots[root]; seen {
					continue
				}
				watchedRoots[root] = struct{}{}
				w.addTree(fsw, root)
			}
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
			if len(deferred) > 0 {
				paths := mapKeys(deferred)
				if w.refresh(paths) {
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
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
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

// refresh 返回 false 表示扫描正在跑，调用方应保留路径等下一轮。
// 解析失败时返回 true，避免坏文件无限重试。
func (w *Watcher) refresh(paths []string) bool {
	if w.scanner.Status().Running {
		return false
	}
	n, ids, err := w.scanner.RefreshFilesWithIDs(paths)
	if err != nil {
		w.log.Error("realtime ingest failed", "paths", len(paths), "err", err)
		return true
	}
	if n <= 0 {
		return true
	}
	w.log.Info("realtime ingest", "paths", len(paths), "changed", n)
	if w.onIngest != nil && len(ids) > 0 {
		w.onIngest(ids)
	}
	return true
}

func isSTRM(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".strm")
}

func skipDirName(name string) bool {
	return strings.HasPrefix(name, ".") || name == "@eaDir"
}
