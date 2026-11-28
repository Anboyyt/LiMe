package api

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"
)

// 测试期间不加载真实地理库，避免依赖数据文件。
func isolateGeo(t *testing.T) {
	t.Helper()
	globalGeo.Store(nil)
	t.Cleanup(func() { globalGeo.Store(nil) })
}

func TestCompileAccessRejectsInvalidEntries(t *testing.T) {
	cfg := defaultAccessControl()
	cfg.IPAllow = []string{"1.2.3.4", "10.0.0.0/8", "not-an-ip"}
	cfg.IPDeny = []string{"5.6.7.8"}

	rules, bad := compileAccess(cfg)
	if len(bad) != 1 || bad[0] != "not-an-ip" {
		t.Fatalf("bad = %v, want [not-an-ip]", bad)
	}
	if len(rules.allowNets) != 1 || len(rules.allowIPs) != 1 {
		t.Fatalf("allow parsed: nets=%d ips=%d", len(rules.allowNets), len(rules.allowIPs))
	}
	if len(rules.denyIPs) != 1 {
		t.Fatalf("deny ips = %d", len(rules.denyIPs))
	}
}

func TestAccessRulesCheck(t *testing.T) {
	isolateGeo(t)

	base := defaultAccessControl()
	base.Enabled = true
	base.IPAllow = []string{"1.2.3.4", "10.10.0.0/16"}
	base.IPDeny = []string{"5.6.7.8", "9.9.9.0/24"}

	rules, _ := compileAccess(base)

	cases := []struct {
		name    string
		ip      string
		want    bool
		unknown string
	}{
		{"private allowed", "192.168.1.9", true, ""},
		{"loopback allowed", "127.0.0.1", true, ""},
		{"exact allow", "1.2.3.4", true, ""},
		{"cidr allow", "10.10.20.30", true, ""},
		{"exact deny", "5.6.7.8", false, ""},
		{"cidr deny", "9.9.9.9", false, ""},
		{"unknown defaults allow", "8.8.8.8", true, "allow"},
		{"unknown defaults deny", "8.8.8.8", false, "deny"},
	}

	for _, tc := range cases {
		cfg := base
		if tc.unknown != "" {
			cfg.UnknownPolicy = tc.unknown
		}
		r, _ := compileAccess(cfg)
		got, reason := r.check(net.ParseIP(tc.ip), false)
		if got != tc.want {
			t.Errorf("%s: check(%s) = %v (%s), want %v", tc.name, tc.ip, got, reason, tc.want)
		}
	}

	// 关闭规则时一切都放行。
	rules.cfg.Enabled = false
	if ok, _ := rules.check(net.ParseIP("5.6.7.8"), false); !ok {
		t.Fatal("disabled rules should allow everything")
	}
}

// 内网放行关掉之后，反代来的私网地址也要被拦。
func TestAccessRulesLANDisabled(t *testing.T) {
	isolateGeo(t)
	cfg := defaultAccessControl()
	cfg.Enabled = true
	cfg.AllowLAN = false
	rules, _ := compileAccess(cfg)
	if ok, reason := rules.check(net.ParseIP("192.168.1.9"), false); ok {
		t.Fatalf("expected deny, got allow (%s)", reason)
	}
}

// 管理员开关只在非内网地址上起作用，用来防止把自己锁在门外。
func TestAccessRulesAdminBypass(t *testing.T) {
	isolateGeo(t)
	cfg := defaultAccessControl()
	cfg.Enabled = true
	cfg.UnknownPolicy = "deny"
	cfg.AllowAdmins = true

	rules, _ := compileAccess(cfg)
	if ok, _ := rules.check(net.ParseIP("8.8.8.8"), true); !ok {
		t.Fatal("admin should bypass")
	}
	if ok, _ := rules.check(net.ParseIP("8.8.8.8"), false); ok {
		t.Fatal("non-admin should be denied by unknown policy")
	}
}

// 伪造的 CF-Connecting-IP 不能在公网直连时生效。
func TestAccessClientIPIgnoresSpoofedHeader(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	r.Header.Set("CF-Connecting-IP", "10.0.0.1")
	if got := accessClientIP(r); got == nil || got.String() != "203.0.113.9" {
		t.Fatalf("accessClientIP = %v, want 203.0.113.9", got)
	}

	// 反代场景（连接来自内网）才信任转发头。
	r.RemoteAddr = "172.17.0.1:1234"
	r.Header.Set("CF-Connecting-IP", "203.0.113.9")
	if got := accessClientIP(r); got == nil || got.String() != "203.0.113.9" {
		t.Fatalf("accessClientIP via proxy = %v, want 203.0.113.9", got)
	}
}

func TestWebAccessControlDryRunDoesNotPersist(t *testing.T) {
	isolateGeo(t)
	f := newUserFixture(t)

	body := map[string]any{
		"Enabled":       true,
		"Mode":          "whitelist",
		"Provinces":     []string{"广东"},
		"Countries":     []string{"中国"},
		"IPAllow":       []string{},
		"IPDeny":        []string{},
		"AllowLAN":      true,
		"AllowAdmins":   true,
		"UnknownPolicy": "allow",
		"DryRun":        true,
	}
	rr := f.do(http.MethodPut, "/web/api/access-control", body, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("dry run status = %d: %s", rr.Code, rr.Body.String())
	}

	var st accessState
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	// DryRun 的响应是「如果保存会是什么样」，库里的值必须还是关闭。
	rr = f.do(http.MethodGet, "/web/api/access-control", nil, f.adminTok)
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Config.Enabled {
		t.Fatal("dry run should not persist enabled state")
	}

	// 真保存之后 GET 能读到。
	delete(body, "DryRun")
	if rr := f.do(http.MethodPut, "/web/api/access-control", body, f.adminTok); rr.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rr.Code, rr.Body.String())
	}
	rr = f.do(http.MethodGet, "/web/api/access-control", nil, f.adminTok)
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Config.Enabled || len(st.Config.Provinces) != 1 {
		t.Fatalf("saved config = %+v", st.Config)
	}
}

func TestAccessControlRejectsBadCIDR(t *testing.T) {
	isolateGeo(t)
	f := newUserFixture(t)
	body := map[string]any{"Enabled": true, "IPAllow": []string{"10.0.0.0/33"}}
	rr := f.do(http.MethodPut, "/web/api/access-control", body, f.adminTok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}
