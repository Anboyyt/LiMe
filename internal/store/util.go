package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// randomHex 生成 n 字节随机数的十六进制串（2n 个字符）。
// Jellyfin 的 ID / Token 都是 32 位无横线 hex。
func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func newID() (string, error) {
	return randomHex(16)
}

func mustID() string {
	id, err := randomHex(16)
	if err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return id
}

func randRead(b []byte) (int, error) {
	return rand.Read(b)
}

// MustRandomHex 供上层生成 SessionId 等标识。
func MustRandomHex(n int) string {
	v, err := randomHex(n)
	if err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return v
}
