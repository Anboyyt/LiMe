package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"lime/internal/geo"
)

// 访问控制：按国家/省份 + IP/CIDR 决定谁能访问。
//
// 规则存在 settings 表，保存后热替换，不重启。中间件在认证之前执行，
// 被拒的请求不会进业务逻辑。

const (
	accessControlKey = "access_control"

	// 管理员身份缓存：中间件在每个请求上都要判断「是不是管理员」，
	// 每次都查库代价太高。token 对应的身份 5 分钟内不会变。
	adminCacheTTL = 5 * time.Minute
	adminCacheMax = 256

	accessModeWhitelist = "whitelist"
	accessModeBlacklist = "blacklist"
)

// AccessControlConfig 是面板上可编辑的规则。
type AccessControlConfig struct {
	Enabled       bool     `json:"Enabled"`
	Mode          string   `json:"Mode"` // whitelist / blacklist
	Countries     []string `json:"Countries"`
	Provinces     []string `json:"Provinces"`
	IPAllow       []string `json:"IPAllow"`
	IPDeny        []string `json:"IPDeny"`
	AllowLAN      bool     `json:"AllowLAN"`
	AllowAdmins   bool     `json:"AllowAdmins"`
	UnknownPolicy string   `json:"UnknownPolicy"` // allow / deny：查不到归属地时怎么处理
}

func defaultAccessControl() AccessControlConfig {
	return AccessControlConfig{
		Enabled:       false,
		Mode:          accessModeWhitelist,
		Countries:     []string{"中国"},
		Provinces:     []string{},
		IPAllow:       []string{},
		IPDeny:        []string{},
		AllowLAN:      true,
		AllowAdmins:   true,
		UnknownPolicy: "allow",
	}
}

// normalize 补齐空值，避免面板收到 null。
func (c *AccessControlConfig) normalize() {
	if c.Mode != accessModeBlacklist {
		c.Mode = accessModeWhitelist
	}
	if c.UnknownPolicy != "deny" {
		c.UnknownPolicy = "allow"
	}
	if c.Countries == nil {
		c.Countries = []string{}
	}
	if c.Provinces == nil {
		c.Provinces = []string{}
	}
	if c.IPAllow == nil {
		c.IPAllow = []string{}
	}
	if c.IPDeny == nil {
		c.IPDeny = []string{}
	}
}

// accessRules 是编译后的规则，热路径只读。
type accessRules struct {
	cfg       AccessControlConfig
	allowNets []*net.IPNet
	denyNets  []*net.IPNet
	allowIPs  map[string]struct{}
	denyIPs   map[string]struct{}
	countries map[string]struct{}
	provinces map[string]struct{}
}

// compileAccess 解析 CIDR / 单 IP。解析失败的行收集到 bad 里返回给面板。
func compileAccess(cfg AccessControlConfig) (*accessRules, []string) {
	cfg.normalize()
	r := &accessRules{
		cfg:       cfg,
		allowIPs:  make(map[string]struct{}, len(cfg.IPAllow)),
		denyIPs:   make(map[string]struct{}, len(cfg.IPDeny)),
		countries: make(map[string]struct{}, len(cfg.Countries)),
		provinces: make(map[string]struct{}, len(cfg.Provinces)),
	}
	bad := make([]string, 0, 2)

	for _, c := range cfg.Countries {
		if v := strings.TrimSpace(c); v != "" {
			r.countries[v] = struct{}{}
		}
	}
	for _, p := range cfg.Provinces {
		if v := geo.NormalizeProvince(p); v != "" {
			r.provinces[v] = struct{}{}
		}
	}

	parse := func(raw string, precise map[string]struct{}, nets *[]*net.IPNet) {
		v := strings.TrimSpace(raw)
		if v == "" {
			return
		}
		if strings.Contains(v, "/") {
			if _, n, err := net.ParseCIDR(v); err == nil {
				*nets = append(*nets, n)
				return
			}
			bad = append(bad, v)
			return
		}
		if ip := net.ParseIP(v); ip != nil {
			precise[ip.String()] = struct{}{}
			return
		}
		bad = append(bad, v)
	}
	for _, v := range cfg.IPAllow {
		parse(v, r.allowIPs, &r.allowNets)
	}
	for _, v := range cfg.IPDeny {
		parse(v, r.denyIPs, &r.denyNets)
	}
	return r, bad
}

func (r *accessRules) contains(nets []*net.IPNet, ip net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (r *accessRules) provinceOf(ip net.IP) (geo.Region, bool) {
	if db := globalGeo.Load(); db != nil {
		return db.Lookup(ip)
	}
	return geo.Region{}, false
}

// check 返回是否放行 + 原因（原因用于日志和面板预览）。
func (r *accessRules) check(ip net.IP, isAdmin bool) (bool, string) {
	if !r.cfg.Enabled {
		return true, "规则未启用"
	}
	if ip == nil {
		return true, "取不到客户端 IP"
	}
	// 管理员放行要在内网判断之前：关掉「内网放行」时，
	// 管理员仍然要能从局域网进面板改规则，否则会把自己锁死。
	if isAdmin && r.cfg.AllowAdmins {
		return true, "管理员放行"
	}
	if isPrivateIP(ip) {
		if r.cfg.AllowLAN {
			return true, "内网地址放行"
		}
		return false, "内网地址被拒绝"
	}
	if _, ok := r.allowIPs[ip.String()]; ok {
		return true, "命中 IP 白名单"
	}
	if r.contains(r.allowNets, ip) {
		return true, "命中 IP 段白名单"
	}
	if _, ok := r.denyIPs[ip.String()]; ok {
		return false, "命中 IP 黑名单"
	}
	if r.contains(r.denyNets, ip) {
		return false, "命中 IP 段黑名单"
	}

	region, found := r.provinceOf(ip)
	if !found {
		if r.cfg.UnknownPolicy == "deny" {
			return false, "查不到归属地，按规则拒绝"
		}
		return true, "查不到归属地，按规则放行"
	}

	whitelist := r.cfg.Mode == accessModeWhitelist
	if len(r.countries) > 0 {
		_, in := r.countries[region.Country]
		if whitelist && !in {
			return false, "国家不在白名单：" + region.Country
		}
		if !whitelist && in {
			return false, "国家在黑名单：" + region.Country
		}
	}

	province := geo.NormalizeProvince(region.Province)
	if len(r.provinces) > 0 && province != "" {
		_, in := r.provinces[province]
		if whitelist && !in {
			return false, "省份不在白名单：" + province
		}
		if !whitelist && in {
			return false, "省份在黑名单：" + province
		}
	}

	if whitelist {
		return true, "白名单放行：" + province
	}
	return true, "不在黑名单：" + province
}

// globalGeo 是进程内共享的归属地库。下载完新库后整体替换。
var globalGeo atomic.Pointer[geo.DB]

// SetGeoDB 在启动和换库时调用。
func SetGeoDB(db *geo.DB) {
	old := globalGeo.Swap(db)
	if old != nil && old != db {
		go func() {
			// 等在途查询结束再关，避免刚替换就把别人的 searcher 关了。
			time.Sleep(2 * time.Second)
			_ = old.Close()
		}()
	}
}

// GeoDB 返回当前库，未加载时为 nil。
func GeoDB() *geo.DB { return globalGeo.Load() }

// ---------------------------------------------------------------- 中间件

// accessExemptPrefixes 是永远放行的路径。
// 健康检查不豁免会让容器 healthcheck 和 Cloudflare 探测直接失败。
var accessExemptPrefixes = []string{
	"/health",
	"/Branding/",
	"/web/assets/",
	"/favicon.ico",
}

// accessExemptPaths 是精确匹配的豁免路径。
// 登录接口必须放行：管理员在外地也要能登录拿 token，
// 拿到 token 之后才会被「管理员放行」认出来。
var accessExemptPaths = map[string]bool{
	"/web":  true,
	"/web/": true,
}

func accessExempt(path string) bool {
	if accessExemptPaths[path] {
		return true
	}
	// 客户端可能走 /emby 前缀，这里按后缀判断。
	if strings.HasSuffix(path, "/Users/AuthenticateByName") {
		return true
	}
	// 运维面板的静态资源放行，但 /web/api 下的接口不豁免。
	if strings.HasPrefix(path, "/web/") && !strings.HasPrefix(path, "/web/api/") {
		return true
	}
	for _, p := range accessExemptPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// remoteAddrIP 只从连接本身取地址，不信任任何转发头。
func remoteAddrIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

// accessClientIP 在 clientAddrIP 的基础上加了信任边界：
// 只有连接来自内网（说明前面有 cloudflared / nginx 反代）才采信转发头，
// 公网直连时一律用连接地址，防止伪造 CF-Connecting-IP 绕过地域限制。
func accessClientIP(r *http.Request) net.IP {
	remote := remoteAddrIP(r)
	if remote != nil && isPrivateIP(remote) {
		return clientAddrIP(r)
	}
	return remote
}

func (h *Handler) accessRulesSnapshot() *accessRules {
	if v := h.accessRules.Load(); v != nil {
		return v
	}
	return nil
}

// withAccessControl 在认证之前拦截。管理员判断会查一次 token（带缓存）。
func (h *Handler) withAccessControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rules := h.accessRulesSnapshot()
		if rules == nil || !rules.cfg.Enabled {
			next.ServeHTTP(w, r)
			return
		}
		if accessExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		ip := accessClientIP(r)
		admin := rules.cfg.AllowAdmins && h.requestIsAdmin(r)
		ok, reason := rules.check(ip, admin)
		if ok {
			next.ServeHTTP(w, r)
			return
		}

		h.log.Info("access denied", "ip", ipString(ip), "path", r.URL.Path,
			"reason", reason, "ua", r.UserAgent())
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("当前网络不允许访问本服务器"))
	})
}

func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}

// clientIPString 用于记录设备来源 IP。
func (h *Handler) clientIPString(r *http.Request) string {
	return ipString(accessClientIP(r))
}

type adminCacheEntry struct {
	admin bool
	at    time.Time
}

// requestIsAdmin 判断请求携带的 token 是否属于管理员。只认认证头，
// 不认 query 里的 api_key（那一类主要给脚本用，按普通请求处理更稳）。
func (h *Handler) requestIsAdmin(r *http.Request) bool {
	token := parseAuth(r).Token
	if token == "" {
		return false
	}

	h.adminMu.Lock()
	if e, ok := h.adminCache[token]; ok && time.Since(e.at) < adminCacheTTL {
		h.adminMu.Unlock()
		return e.admin
	}
	h.adminMu.Unlock()

	user, err := h.db.UserByToken(token)
	admin := err == nil && user != nil && user.IsAdmin

	h.adminMu.Lock()
	if len(h.adminCache) >= adminCacheMax {
		for k := range h.adminCache {
			delete(h.adminCache, k)
		}
	}
	h.adminCache[token] = adminCacheEntry{admin: admin, at: time.Now()}
	h.adminMu.Unlock()
	return admin
}

// invalidateAdminCache 在权限变更后调用，避免 5 分钟内旧值继续生效。
func (h *Handler) invalidateAdminCache() {
	h.adminMu.Lock()
	h.adminCache = make(map[string]adminCacheEntry, 8)
	h.adminMu.Unlock()
}

// ---------------------------------------------------------------- 规则读写

func (h *Handler) loadAccessControl() {
	cfg := defaultAccessControl()

	raw, err := h.db.GetSetting(accessControlKey)
	if err == nil && strings.TrimSpace(raw) != "" {
		// 解析失败时保留默认值（关闭），不能因为设置损坏把所有人挡外面。
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			h.log.Warn("parse access control setting", "err", err)
			cfg = defaultAccessControl()
		}
	}

	rules, bad := compileAccess(cfg)
	if len(bad) > 0 {
		h.log.Warn("ignored invalid access control entries", "entries", strings.Join(bad, ", "))
	}
	h.accessRules.Store(rules)
	if cfg.Enabled {
		h.log.Info("access control enabled", "mode", cfg.Mode,
			"provinces", strings.Join(cfg.Provinces, ","),
			"ip_allow", len(cfg.IPAllow), "ip_deny", len(cfg.IPDeny))
	}
}

// accessState 是面板 GET 的响应。
type accessState struct {
	Config       AccessControlConfig `json:"Config"`
	GeoReady     bool                `json:"GeoReady"`
	GeoDir       string              `json:"GeoDir"`
	GeoMissing   []string            `json:"GeoMissing"`
	ClientIP     string              `json:"ClientIP"`
	ClientRegion string              `json:"ClientRegion"`
	ClientAllow  bool                `json:"ClientAllow"`
	ClientReason string              `json:"ClientReason"`
	Invalid      []string            `json:"Invalid"`
	Download     geoDownloadState    `json:"Download"`
}

func (h *Handler) webAccessControl(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.writeAccessState(w, r)
	case http.MethodPut:
		h.saveAccessControl(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) accessStateFor(cfg AccessControlConfig, r *http.Request) accessState {
	rules, bad := compileAccess(cfg)
	ip := accessClientIP(r)

	db := GeoDB()
	st := accessState{
		Config:     rules.cfg,
		GeoReady:   db != nil && db.Ready(),
		GeoDir:     h.geoDir(),
		GeoMissing: h.geoMissing(),
		ClientIP:   ipString(ip),
		Invalid:    bad,
		Download:   geoDownloadSnapshot(),
	}
	if !st.GeoReady {
		st.GeoMissing = []string{geo.V4File, geo.V6File}
	}
	if region, ok := rules.provinceOf(ip); ok {
		parts := []string{region.Country, region.Province, region.City}
		st.ClientRegion = strings.Trim(strings.Join(parts, " "), " ")
	}
	st.ClientAllow, st.ClientReason = rules.check(ip, true)
	if st.ClientReason == "" {
		st.ClientReason = "—"
	}
	return st
}

func (h *Handler) writeAccessState(w http.ResponseWriter, r *http.Request) {
	cfg := defaultAccessControl()
	if rules := h.accessRulesSnapshot(); rules != nil {
		cfg = rules.cfg
	}
	writeJSON(w, http.StatusOK, h.accessStateFor(cfg, r))
}

func (h *Handler) geoDir() string {
	if h.cfg.GeoIPDir != "" {
		return h.cfg.GeoIPDir
	}
	// 容器里 DataDir=/data，宿主机映射成 ./data，地理库跟着数据走，
	// 换库不需要重新构建镜像。
	return filepath.Join(h.cfg.DataDir, "geoip")
}

func (h *Handler) geoMissing() []string {
	missing := make([]string, 0, 2)
	for _, name := range []string{geo.V4File, geo.V6File} {
		if !geo.FileExists(h.geoDir() + "/" + name) {
			missing = append(missing, name)
		}
	}
	return missing
}

func (h *Handler) saveAccessControl(w http.ResponseWriter, r *http.Request) {
	// DryRun 只算不存：面板保存前先确认「这条规则会不会把当前 IP 挡外面」。
	var req struct {
		AccessControlConfig
		DryRun bool `json:"DryRun"`
	}
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	cfg := req.AccessControlConfig
	cfg.normalize()

	rules, bad := compileAccess(cfg)
	if len(bad) > 0 {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("以下 IP/CIDR 无法解析：%s", strings.Join(bad, ", ")))
		return
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, h.accessStateFor(cfg, r))
		return
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.db.SetSetting(accessControlKey, string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	h.accessRules.Store(rules)
	h.log.Info("access control updated", "enabled", cfg.Enabled, "mode", cfg.Mode,
		"provinces", strings.Join(cfg.Provinces, ","))

	writeJSON(w, http.StatusOK, h.accessStateFor(cfg, r))
}
