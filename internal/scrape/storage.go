package scrape

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// StorageStats 是图片目录和缩放缓存的占用情况。
type StorageStats struct {
	Images      int64 `json:"Images"`
	ImageBytes  int64 `json:"ImageBytes"`
	Orphans     int64 `json:"Orphans"`
	OrphanBytes int64 `json:"OrphanBytes"`
	Cache       int64 `json:"Cache"`
	CacheBytes  int64 `json:"CacheBytes"`
}

// CleanupResult 是一次清理的结果。
type CleanupResult struct {
	RemovedFiles int64 `json:"RemovedFiles"`
	RemovedBytes int64 `json:"RemovedBytes"`
	CacheFiles   int64 `json:"CacheFiles"`
	CacheBytes   int64 `json:"CacheBytes"`
}

// maintenanceBusy 判断是否有刮削/图片任务在跑；清理时避免删到正在写入的文件。
func (s *Scraper) maintenanceBusy() bool {
	s.mu.Lock()
	busy := s.running || s.quick > 0
	s.mu.Unlock()
	if busy {
		return true
	}
	s.backdropMu.Lock()
	defer s.backdropMu.Unlock()
	return s.backdrop.Running || s.poster.Running || s.still.Running
}

// StorageStats 统计 data/images 与 data/image-cache。孤儿文件是
// data/images 下没有被任何条目 image/backdrop/logo 引用的文件。
func (s *Scraper) StorageStats() (StorageStats, error) {
	s.storageMu.Lock()
	if !s.storageCacheAt.IsZero() && time.Since(s.storageCacheAt) < 60*time.Second {
		st := s.storageCache
		s.storageMu.Unlock()
		return st, nil
	}
	s.storageMu.Unlock()

	refs, err := s.db.ReferencedImagePaths()
	if err != nil {
		return StorageStats{}, err
	}
	var st StorageStats
	if err := walkStorage(filepath.Join(s.cfg.DataDir, "images"), "images", refs, true, &st, nil); err != nil {
		return st, err
	}
	if err := walkStorage(filepath.Join(s.cfg.DataDir, "image-cache"), "image-cache", nil, false, &st, nil); err != nil {
		return st, err
	}
	s.storageMu.Lock()
	s.storageCache = st
	s.storageCacheAt = time.Now()
	s.storageMu.Unlock()
	return st, nil
}

// CleanupStorage 删除无引用图片，并清掉缩放缓存。缓存只删 10 分钟前的，
// 避免和正在处理的图片请求抢同一个文件。
func (s *Scraper) CleanupStorage() (CleanupResult, error) {
	if s.maintenanceBusy() {
		return CleanupResult{}, errors.New("刮削或图片任务进行中")
	}
	refs, err := s.db.ReferencedImagePaths()
	if err != nil {
		return CleanupResult{}, err
	}
	var res CleanupResult
	var st StorageStats
	// images 目录：只删没有任何条目引用的文件。
	if err := walkStorage(filepath.Join(s.cfg.DataDir, "images"), "images", refs, true, &st, func(path string, info fs.FileInfo) {
		if err := os.Remove(path); err == nil {
			res.RemovedFiles++
			res.RemovedBytes += info.Size()
		}
	}); err != nil {
		return res, err
	}
	// image-cache：全是可再生成的缩放结果，留 10 分钟给正在处理的请求。
	cutoff := time.Now().Add(-10 * time.Minute)
	if err := walkStorage(filepath.Join(s.cfg.DataDir, "image-cache"), "image-cache", nil, false, &st, func(path string, info fs.FileInfo) {
		if info.ModTime().After(cutoff) {
			return
		}
		if err := os.Remove(path); err == nil {
			res.CacheFiles++
			res.CacheBytes += info.Size()
		}
	}); err != nil {
		return res, err
	}
	s.storageMu.Lock()
	s.storageCacheAt = time.Time{}
	s.storageMu.Unlock()
	s.log.Info("storage cleanup done",
		"images_removed", res.RemovedFiles, "images_mb", res.RemovedBytes>>20,
		"cache_removed", res.CacheFiles, "cache_mb", res.CacheBytes>>20)
	return res, nil
}

func walkStorage(root, prefix string, refs map[string]struct{}, checkOrphans bool, st *StorageStats, onFile func(string, fs.FileInfo)) error {
	if root == "" {
		return nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}

		orphan := false
		if prefix == "images" {
			st.Images++
			st.ImageBytes += info.Size()
			if checkOrphans {
				relPath, rerr := filepath.Rel(root, path)
				if rerr == nil {
					rel := "images/" + filepath.ToSlash(relPath)
					if _, ok := refs[rel]; !ok {
						orphan = true
						st.Orphans++
						st.OrphanBytes += info.Size()
					}
				}
			}
		} else {
			st.Cache++
			st.CacheBytes += info.Size()
		}

		if onFile != nil && (!checkOrphans || orphan) {
			onFile(path, info)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
