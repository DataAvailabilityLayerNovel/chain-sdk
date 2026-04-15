package main

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// SecurityConfig holds HTTP hardening settings.
type SecurityConfig struct {
	// MaxRequestBodyBytes limits POST body size (0 = no limit).
	MaxRequestBodyBytes int64
	// AuthToken, if set, requires Authorization: Bearer <token> on mutating endpoints.
	AuthToken string
	// CORSAllowOrigin sets Access-Control-Allow-Origin header ("*" for dev, specific origin for prod).
	CORSAllowOrigin string
	// RateLimitRPS sets the max requests per second per IP (0 = no limit).
	RateLimitRPS int
	// ReadOnlyMode disables all POST endpoints (useful for public query nodes).
	ReadOnlyMode bool
}

// securityMiddleware wraps an http.Handler with CORS, auth, body size limits, rate limiting.
func securityMiddleware(next http.Handler, cfg SecurityConfig) http.Handler {
	var limiter *ipRateLimiter
	if cfg.RateLimitRPS > 0 {
		limiter = newIPRateLimiter(cfg.RateLimitRPS)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS headers.
		origin := cfg.CORSAllowOrigin
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Read-only mode: reject all POST requests.
		if cfg.ReadOnlyMode && r.Method == http.MethodPost {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "node is in read-only mode"})
			return
		}

		// Auth token check on mutating endpoints (POST).
		if cfg.AuthToken != "" && r.Method == http.MethodPost {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != cfg.AuthToken {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}

		// Rate limiting.
		if limiter != nil {
			ip := extractIP(r)
			if !limiter.allow(ip) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
				return
			}
		}

		// Body size limit on POST.
		if cfg.MaxRequestBodyBytes > 0 && r.Method == http.MethodPost && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxRequestBodyBytes)
		}

		next.ServeHTTP(w, r)
	})
}

func extractIP(r *http.Request) string {
	// Prefer X-Forwarded-For if behind reverse proxy.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	// Fall back to RemoteAddr (ip:port).
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx != -1 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
}

// ipRateLimiter is a simple token-bucket rate limiter per IP.
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rps     int
}

type tokenBucket struct {
	tokens   float64
	lastTime time.Time
}

func newIPRateLimiter(rps int) *ipRateLimiter {
	return &ipRateLimiter{
		buckets: make(map[string]*tokenBucket),
		rps:     rps,
	}
}

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok {
		b = &tokenBucket{tokens: float64(l.rps), lastTime: now}
		l.buckets[ip] = b
	}

	// Refill tokens.
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * float64(l.rps)
	if b.tokens > float64(l.rps) {
		b.tokens = float64(l.rps)
	}
	b.lastTime = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
