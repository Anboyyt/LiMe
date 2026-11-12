package api

import (
	"sync"
	"time"
)

// 登录失败限制。两个维度同时计数：
//   - 单 IP：挡住撞库（换用户名猜密码）
//   - 单 IP + 用户名：挡住针对某个账号的暴力破解
//
// 只保存失败次数和窗口截止时间，不记录密码或 token。
const (
	loginFailWindow  = 15 * time.Minute
	loginIPLimit     = 20
	loginUserLimit   = 5
	loginCleanupSize = 1024
)

type loginBucket struct {
	fails int
	reset time.Time
}

type loginKey struct {
	key   string
	limit int
}

type loginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*loginBucket
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{buckets: make(map[string]*loginBucket)}
}

// blocked 返回还需等待多久；false 表示可以继续尝试。
func (l *loginLimiter) blocked(keys []loginKey) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	var wait time.Duration
	for _, k := range keys {
		b := l.buckets[k.key]
		if b == nil {
			continue
		}
		if !now.Before(b.reset) {
			delete(l.buckets, k.key)
			continue
		}
		if b.fails >= k.limit {
			if d := b.reset.Sub(now); d > wait {
				wait = d
			}
		}
	}
	return wait, wait > 0
}

func (l *loginLimiter) fail(keys []loginKey) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	for _, k := range keys {
		b := l.buckets[k.key]
		if b == nil || !now.Before(b.reset) {
			b = &loginBucket{reset: now.Add(loginFailWindow)}
			l.buckets[k.key] = b
		}
		b.fails++
	}
	l.cleanupLocked(now)
}

// reset 在登录成功后清掉该账号的失败计数；IP 维度保留，
// 避免攻击者用一个有效账号把整台 IP 的失败记录洗掉。
func (l *loginLimiter) reset(userKey string) {
	l.mu.Lock()
	delete(l.buckets, userKey)
	l.mu.Unlock()
}

// cleanupLocked 防止大量随机用户名把 map 撑爆。
func (l *loginLimiter) cleanupLocked(now time.Time) {
	if len(l.buckets) < loginCleanupSize {
		return
	}
	for k, b := range l.buckets {
		if !now.Before(b.reset) {
			delete(l.buckets, k)
		}
	}
}
