package geo

import (
	"net"
	"os"
	"testing"
)

// 需要真实数据文件的集成测试，默认跳过：
//
//	LIME_GEOIP_TEST_DIR=/path/to/geoip go test ./internal/geo/ -run Real
func TestLookupRealDatabase(t *testing.T) {
	dir := os.Getenv("LIME_GEOIP_TEST_DIR")
	if dir == "" {
		t.Skip("set LIME_GEOIP_TEST_DIR to run")
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	region, ok := db.Lookup(net.ParseIP("114.114.114.114"))
	if !ok {
		t.Fatal("lookup failed")
	}
	if region.Country != "中国" {
		t.Fatalf("country = %q", region.Country)
	}
	if got := region.ProvinceName(); got == "" {
		t.Fatal("empty province")
	}

	// 私网地址不查库。
	if _, ok := db.Lookup(net.ParseIP("192.168.1.1")); ok {
		t.Fatal("private ip should not resolve")
	}

	// v6 库是可选的，装了就应该能查。
	if _, err := os.Stat(dir + string(os.PathSeparator) + V6File); err == nil {
		v6, ok := db.Lookup(net.ParseIP("240e:3b7:3272:d8d0:db09:c067:8d59:539e"))
		if !ok {
			t.Fatal("ipv6 lookup failed")
		}
		if v6.Country != "中国" || v6.ProvinceName() != "广东" {
			t.Fatalf("ipv6 region = %+v", v6)
		}
	}
}

func TestNormalizeProvince(t *testing.T) {
	cases := map[string]string{
		"广东省":      "广东",
		"广西壮族自治区":  "广西",
		"内蒙古自治区":   "内蒙古",
		"新疆维吾尔自治区": "新疆",
		"宁夏回族自治区":  "宁夏",
		"香港特别行政区":  "香港",
		"北京市":      "北京",
		"广东":       "广东",
		"":         "",
		"中国":       "中国",
	}
	for in, want := range cases {
		if got := NormalizeProvince(in); got != want {
			t.Errorf("NormalizeProvince(%q) = %q, want %q", in, got, want)
		}
	}
}

// 缺库时 Open 要返回明确错误，不能静默成功然后所有查询都落到「未知」。
func TestOpenWithoutDatabase(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("expected error when no xdb present")
	}
}
