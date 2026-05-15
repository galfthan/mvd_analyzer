package main

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// responseRecorder captures status + bytes written for the access log.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	if rr.status == 0 {
		rr.status = http.StatusOK
	}
	n, err := rr.ResponseWriter.Write(b)
	rr.bytes += n
	return n, err
}

// accessLogMiddleware emits one structured line per request. The
// optional Bearer label (or ?label= query param) is captured for
// request-source analytics — it's not a secret and is never validated.
func accessLogMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rr := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rr, r)

		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rr.status,
			"bytes", rr.bytes,
			"latency_ms", time.Since(start).Milliseconds(),
			"remote", clientIP(r),
			"label", requestLabel(r),
			"cache", w.Header().Get("X-Cache"),
		)
	})
}

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		if comma := strings.IndexByte(xf, ','); comma > 0 {
			return strings.TrimSpace(xf[:comma])
		}
		return strings.TrimSpace(xf)
	}
	return r.RemoteAddr
}

// requestLabel extracts the non-secret traffic-source label from
// Authorization: Bearer <label> or ?label=<label>. Returns "" when
// neither is set.
func requestLabel(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return r.URL.Query().Get("label")
}
