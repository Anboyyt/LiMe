package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 容器 healthcheck 用这个端点：数据库能应答才算健康，同时带一份运行时快照。
func TestDeepHealth(t *testing.T) {
	f := newUserFixture(t)
	rr := f.do(http.MethodGet, "/health/deep", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var res struct {
		Status string `json:"status"`
		DB     struct {
			OK        bool  `json:"ok"`
			LatencyMs int64 `json:"latency_ms"`
		} `json:"db"`
		Goroutines int `json:"goroutines"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "ok" || !res.DB.OK {
		t.Fatalf("deep health = %+v", res)
	}
	if res.Goroutines <= 0 {
		t.Fatalf("goroutines = %d", res.Goroutines)
	}
}
