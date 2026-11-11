package api

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// gzipWriter 按 Content-Type 决定是否压缩，图片等二进制直接透传。
type gzipWriter struct {
	http.ResponseWriter
	gz       *gzip.Writer
	compress bool
	wrote    bool
}

func (w *gzipWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true

	h := w.Header()
	ct := strings.ToLower(h.Get("Content-Type"))
	compressible := strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "json") ||
		strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "css") ||
		strings.Contains(ct, "svg")

	if compressible && code != http.StatusNoContent && code != http.StatusNotModified &&
		h.Get("Content-Encoding") == "" {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		w.compress = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.compress {
		if w.gz == nil {
			w.gz = gzip.NewWriter(w.ResponseWriter)
		}
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *gzipWriter) Flush() {
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *gzipWriter) Close() error {
	if w.gz != nil {
		return w.gz.Close()
	}
	return nil
}

// withGzip 对文本响应做压缩。Range 请求不压缩，避免破坏 206 语义。
func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
			r.Header.Get("Range") != "" ||
			r.Header.Get("Upgrade") != "" {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}
