// Package geo 封装 ip2region 离线库，用来判断客户端 IP 属于哪个省。
//
// 数据文件不内嵌：v4 约 11MB、v6 约 37MB，放进镜像太重。运行时从
// LIME_GEOIP_DIR 读取，缺失时降级为「未知地区」，由访问控制规则决定
// 未知地区放行还是拒绝。
package geo

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lionsoul2014/ip2region/binding/golang/service"
)

// Region 是 ip2region 返回的一条归属地信息。
type Region struct {
	Country  string
	Province string
	City     string
	ISP      string
}

// ProvinceName 返回去掉行政后缀的省份简称，如「广东省」→「广东」。
func (r Region) ProvinceName() string { return NormalizeProvince(r.Province) }

const (
	// V4File / V6File 是 LIME_GEOIP_DIR 下的固定文件名。
	V4File = "ip2region_v4.xdb"
	V6File = "ip2region_v6.xdb"

	cacheTTL = 10 * time.Minute
	cacheMax = 8192

	searchers = 8
)

type cacheEntry struct {
	region Region
	ok     bool
	at     time.Time
}

// DB 是并发安全的归属地查询器。svc 用 atomic 持有，方便换库后热更新。
type DB struct {
	svc atomic.Pointer[service.Ip2Region]

	mu    sync.Mutex
	cache map[string]cacheEntry
}

var (
	// ErrNoDatabase 表示两个数据文件都不存在。
	ErrNoDatabase = errors.New("geoip database not found")
)

// DefaultDir 返回默认数据目录。
func DefaultDir() string { return filepath.Join("data", "geoip") }

// Open 从目录加载 v4 / v6 两个数据文件。任意一个存在即可工作。
func Open(dir string) (*DB, error) {
	var v4, v6 *service.Config
	var missing []string

	if path := filepath.Join(dir, V4File); FileExists(path) {
		c, err := service.NewV4Config(service.VIndexCache, path, searchers)
		if err != nil {
			return nil, fmt.Errorf("geoip v4: %w", err)
		}
		v4 = c
	} else {
		missing = append(missing, V4File)
	}

	if path := filepath.Join(dir, V6File); FileExists(path) {
		c, err := service.NewV6Config(service.VIndexCache, path, searchers)
		if err != nil {
			return nil, fmt.Errorf("geoip v6: %w", err)
		}
		v6 = c
	} else {
		missing = append(missing, V6File)
	}

	if v4 == nil && v6 == nil {
		return nil, fmt.Errorf("%w: %s under %s", ErrNoDatabase, strings.Join(missing, ", "), dir)
	}

	svc, err := service.NewIp2Region(v4, v6)
	if err != nil {
		return nil, fmt.Errorf("geoip open: %w", err)
	}

	d := &DB{cache: make(map[string]cacheEntry, 256)}
	d.svc.Store(svc)
	return d, nil
}

func FileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Size() > 0
}

// Ready 表示至少有一个数据文件加载成功。
func (d *DB) Ready() bool { return d != nil && d.svc.Load() != nil }

// Close 释放查询器。调用后 DB 不可再用。
func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	if svc := d.svc.Swap(nil); svc != nil {
		svc.Close()
	}
	return nil
}

// Lookup 查询 IP 归属地。ok=false 表示库里没有这条 IP（内网、保留段等）。
func (d *DB) Lookup(ip net.IP) (Region, bool) {
	if d == nil || ip == nil || ip.IsLoopback() || ip.IsPrivate() {
		return Region{}, false
	}
	key := ip.String()

	d.mu.Lock()
	if e, ok := d.cache[key]; ok && time.Since(e.at) < cacheTTL {
		d.mu.Unlock()
		return e.region, e.ok
	}
	d.mu.Unlock()

	region, ok := d.search(key)

	d.mu.Lock()
	if len(d.cache) >= cacheMax {
		// 满了先清过期项；还满就整体丢弃，避免无限增长。
		now := time.Now()
		for k, v := range d.cache {
			if now.Sub(v.at) >= cacheTTL {
				delete(d.cache, k)
			}
		}
		if len(d.cache) >= cacheMax {
			d.cache = make(map[string]cacheEntry, 256)
		}
	}
	d.cache[key] = cacheEntry{region: region, ok: ok, at: time.Now()}
	d.mu.Unlock()

	return region, ok
}

func (d *DB) search(key string) (Region, bool) {
	svc := d.svc.Load()
	if svc == nil {
		return Region{}, false
	}
	raw, err := svc.Search(key)
	if err != nil || raw == "" {
		return Region{}, false
	}
	parts := strings.SplitN(raw, "|", 5)
	var r Region
	if len(parts) > 0 {
		r.Country = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		r.Province = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		r.City = strings.TrimSpace(parts[2])
	}
	if len(parts) > 3 {
		r.ISP = strings.TrimSpace(parts[3])
	}
	return r, r.Country != "" || r.Province != ""
}

// 省/自治区/直辖市后缀。顺序重要：长的先匹配。
var provinceSuffixes = []string{
	"特别行政区",
	"维吾尔自治区",
	"壮族自治区",
	"回族自治区",
	"自治区",
	"省",
	"市",
}

// NormalizeProvince 把「广东省」「广西壮族自治区」统一成「广东」「广西」。
// 面板里存的也是简称，两边比较前都过一遍。
func NormalizeProvince(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, suf := range provinceSuffixes {
		if strings.HasSuffix(s, suf) {
			return strings.TrimSuffix(s, suf)
		}
	}
	return s
}
