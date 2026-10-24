package gateway

import (
	"net/http"
	"strings"
	"time"
)

// statusRecorder captures the response status for access logs.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader implements http.ResponseWriter.
func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// withMiddleware wraps the mux with recover, access logging, auth and
// rate limiting, outermost first.
func (g *Gateway) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if p := recover(); p != nil {
				g.logger.Error("panic recovered", "path", r.URL.Path, "panic", p)
				if rec.status == http.StatusOK { // nothing written yet
					writeError(rec, http.StatusInternalServerError, errPanic)
				}
			}
			g.logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start).Round(time.Millisecond),
			)
		}()

		// Liveness probes cannot carry bearer tokens; /healthz stays open.
		if r.URL.Path != "/healthz" {
			if !g.authorized(r) {
				writeError(rec, http.StatusUnauthorized, errUnauthorized)
				return
			}
			if !g.limiter.Allow(clientKey(r)) {
				writeError(rec, http.StatusTooManyRequests, errRateLimited)
				return
			}
		}
		next.ServeHTTP(rec, r)
	})
}

// authorized checks the bearer token against configured keys. With no
// keys configured the gateway runs open, for local development.
func (g *Gateway) authorized(r *http.Request) bool {
	if len(g.keys) == 0 {
		return true
	}
	token, ok := cutBearer(r.Header.Get("Authorization"))
	return ok && g.keys[token]
}

// cutBearer extracts the token from an Authorization header.
func cutBearer(header string) (string, bool) {
	return strings.CutPrefix(header, "Bearer ")
}

type staticError string

func (e staticError) Error() string { return string(e) }

const (
	errUnauthorized = staticError("invalid or missing API key")
	errRateLimited  = staticError("rate limit exceeded")
	errPanic        = staticError("internal server error")
)
