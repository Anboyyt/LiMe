package api

import (
	"sort"
	"strings"
	"time"
)

// filterOptions 是客户端筛选面板需要的固定选项。
type filterOptions struct {
	Genres          []string
	Years           []int
	OfficialRatings []string
}

const filterCacheTTL = 5 * time.Minute

type filterCacheEntry struct {
	opts filterOptions
	at   time.Time
}

// filterOptionsFor 按「可见库集合」缓存 Genres / Years / Ratings。
// 这三个查询都要扫 items，100 万条目时客户端每次打开库都会卡几秒；
// 缓存 5 分钟足够，扫描/刮削的新数据最多晚 5 分钟出现在筛选面板。
func (h *Handler) filterOptionsFor(libraryIDs []string) (filterOptions, error) {
	key := filterCacheKey(libraryIDs)

	h.filterMu.Lock()
	if e, ok := h.filterCache[key]; ok {
		if time.Since(e.at) < filterCacheTTL {
			h.filterMu.Unlock()
			return e.opts, nil
		}
		// 过期后先返回旧结果，后台刷新；只有第一次请求需要等查询。
		if !h.filterInflight[key] {
			if h.filterInflight == nil {
				h.filterInflight = make(map[string]bool, 4)
			}
			h.filterInflight[key] = true
			ids := append([]string(nil), libraryIDs...)
			go h.refreshFilterOptions(key, ids)
		}
		h.filterMu.Unlock()
		return e.opts, nil
	}
	h.filterMu.Unlock()

	opts, err := h.loadFilterOptions(libraryIDs)
	if err != nil {
		return filterOptions{}, err
	}
	h.storeFilterOptions(key, opts)
	return opts, nil
}

func (h *Handler) loadFilterOptions(libraryIDs []string) (filterOptions, error) {
	genres, err := h.db.GenreNamesIn(libraryIDs)
	if err != nil {
		return filterOptions{}, err
	}
	years, err := h.db.ProductionYearsIn(libraryIDs)
	if err != nil {
		return filterOptions{}, err
	}
	ratings, err := h.db.OfficialRatingsIn(libraryIDs)
	if err != nil {
		return filterOptions{}, err
	}
	return filterOptions{Genres: genres, Years: years, OfficialRatings: ratings}, nil
}

func (h *Handler) refreshFilterOptions(key string, libraryIDs []string) {
	opts, err := h.loadFilterOptions(libraryIDs)
	h.filterMu.Lock()
	delete(h.filterInflight, key)
	if err == nil {
		h.storeFilterOptionsLocked(key, opts)
	}
	h.filterMu.Unlock()
}

func (h *Handler) storeFilterOptions(key string, opts filterOptions) {
	h.filterMu.Lock()
	h.storeFilterOptionsLocked(key, opts)
	h.filterMu.Unlock()
}

func (h *Handler) storeFilterOptionsLocked(key string, opts filterOptions) {
	if h.filterCache == nil || len(h.filterCache) > 64 {
		h.filterCache = make(map[string]filterCacheEntry, 8)
	}
	h.filterCache[key] = filterCacheEntry{opts: opts, at: time.Now()}
}

func filterCacheKey(libraryIDs []string) string {
	if len(libraryIDs) == 0 {
		return "all"
	}
	ids := append([]string(nil), libraryIDs...)
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// invalidateFilterCache 在扫描/刮削等改变条目集合的操作后清空缓存。
func (h *Handler) invalidateFilterCache() {
	h.filterMu.Lock()
	h.filterCache = nil
	h.filterMu.Unlock()
}
