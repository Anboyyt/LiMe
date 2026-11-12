package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lime/internal/store"
)

func TestLoginLimiterWindowAndReset(t *testing.T) {
	l := newLoginLimiter()
	keys := []loginKey{{"ip:1", loginIPLimit}, {"ip:1|u:a", loginUserLimit}}

	for i := 0; i < loginUserLimit; i++ {
		if _, blocked := l.blocked(keys); blocked {
			t.Fatalf("blocked too early at attempt %d", i)
		}
		l.fail(keys)
	}
	if _, blocked := l.blocked(keys); !blocked {
		t.Fatal("should be blocked after limit failures")
	}

	// 成功登录只清账号维度。
	l.reset("ip:1|u:a")
	if _, blocked := l.blocked(keys); blocked {
		t.Fatal("user reset should clear user bucket")
	}

	// 窗口过期后自动恢复。
	l.fail(keys)
	l.mu.Lock()
	for _, b := range l.buckets {
		b.reset = time.Now().Add(-time.Second)
	}
	l.mu.Unlock()
	if _, blocked := l.blocked(keys); blocked {
		t.Fatal("expired bucket should not block")
	}
}

func TestLoginRateLimitHTTP(t *testing.T) {
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	if _, err := env.db.CreateUser("viewer", hash, false); err != nil {
		t.Fatal(err)
	}

	login := func(password string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]string{"Username": "viewer", "Pw": password})
		req := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		env.handler.ServeHTTP(rr, req)
		return rr
	}

	for i := 0; i < loginUserLimit; i++ {
		if rr := login("wrong"); rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i, rr.Code)
		}
	}
	rr := login("pw")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("after limit = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
}
