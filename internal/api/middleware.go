package api

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers",
				"Authorization, Content-Type, X-Emby-Authorization, X-Emby-Token, "+
					"X-MediaBrowser-Token, X-Emby-Client, X-Emby-Device-Name")
			w.Header().Set("Access-Control-Expose-Headers",
				"Content-Range, Content-Length, Accept-Ranges, Location, X-Emby-Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type ctxKey int

const embyCompatKey ctxKey = iota

// isEmbyCompat 表示请求走了 /emby/ 前缀，按 Emby 的口径应答。
func isEmbyCompat(r *http.Request) bool {
	v, _ := r.Context().Value(embyCompatKey).(bool)
	return v
}

// canonicalTop 把客户端大小写不一致的首段统一成路由注册的形式。
// 实测部分播放器会请求 /emby/videos/.../stream.mkv，大小写不匹配会 404。
var canonicalTop = map[string]string{
	"videos":       "Videos",
	"audio":        "Audio",
	"items":        "Items",
	"shows":        "Shows",
	"users":        "Users",
	"library":      "Library",
	"sessions":     "Sessions",
	"system":       "System",
	"branding":     "Branding",
	"quickconnect": "QuickConnect",
	"auth":         "Auth",
}

func canonicalAPIPath(path string) string {
	if path == "" || path == "/" {
		return path
	}
	rest := strings.TrimPrefix(path, "/")
	seg, tail, _ := strings.Cut(rest, "/")
	canon, ok := canonicalTop[strings.ToLower(seg)]
	if !ok {
		return path
	}
	if tail == "" {
		return "/" + canon
	}
	return "/" + canon + "/" + tail
}

// stripEmbyPrefix 兼容 Emby 风格的 /emby/... 前缀。
// Emby 客户端（以及 PanSave 这类按 Emby 实现的服务）会把 API 放在 /emby 下，
// 这里统一去掉前缀后再交给路由。
func stripEmbyPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		emby := false
		// 客户端地址里已经带 /emby 时可能拼成 /emby/emby/...，循环去掉所有重复前缀。
	loop:
		for {
			switch {
			case path == "/emby":
				path, emby = "/", true
			case strings.HasPrefix(path, "/emby/"):
				path, emby = strings.TrimPrefix(path, "/emby"), true
			default:
				break loop
			}
		}

		path = canonicalAPIPath(path)
		if !emby && path == r.URL.Path {
			next.ServeHTTP(w, r)
			return
		}

		ctx := r.Context()
		if emby {
			ctx = context.WithValue(ctx, embyCompatKey, true)
		}
		r2 := r.Clone(ctx)
		r2.URL.Path = path
		r2.URL.RawPath = ""
		next.ServeHTTP(w, r2)
	})
}

// noCache 让内嵌静态文件每次都回源校验，避免升级后仍加载旧 JS/CSS。
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func withLogging(log *slog.Logger, verbose bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		// /web/api/ 也记录：面板排障时能看到实际请求和状态码。
		if verbose && r.URL.Path != "/health" && !strings.HasPrefix(r.URL.Path, "/Items/") &&
			!strings.HasPrefix(r.URL.Path, "/app/") &&
			!strings.HasPrefix(r.URL.Path, "/web/style.css") &&
			!strings.HasPrefix(r.URL.Path, "/web/app.js") {
			log.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"query", loggableQuery(r),
				"status", sw.status,
				"dur", time.Since(start).String(),
				"ip", clientIP(r),
				"ua", r.UserAgent(),
			)
			return
		}
		log.Debug("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"dur", time.Since(start).String(),
		)
	})
}

// loggableQuery 只保留排障需要的查询参数，避免把 api_key / token 写进日志。
func loggableQuery(r *http.Request) string {
	keys := []string{
		"ParentId", "Recursive", "IncludeItemTypes", "ExcludeItemTypes",
		"Limit", "StartIndex", "SortBy", "SortOrder", "SearchTerm",
	}
	q := r.URL.Query()
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if v := q.Get(k); v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, "&")
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Hijack 让 WebSocket 升级能穿过日志中间件。
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack unsupported")
	}
	return h.Hijack()
}
