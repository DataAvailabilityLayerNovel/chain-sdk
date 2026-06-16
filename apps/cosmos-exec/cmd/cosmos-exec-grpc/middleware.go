package main

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// SecurityConfig holds HTTP hardening settings.
//
// VI: cấu hình "gia cố" HTTP — gom các tuỳ chọn bảo mật bật/tắt qua ENV ở main.go.
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
//
// VI: middleware bọc handler chính, chạy TRƯỚC mọi request theo thứ tự: CORS →
// chặn POST nếu read-only → kiểm token → rate limit → giới hạn body, rồi mới
// gọi next. Trả về một http.Handler mới.
func securityMiddleware(next http.Handler, cfg SecurityConfig) http.Handler {
	// Chỉ dựng limiter khi có bật giới hạn (RPS > 0).
	var limiter *ipRateLimiter
	if cfg.RateLimitRPS > 0 {
		limiter = newIPRateLimiter(cfg.RateLimitRPS)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS headers. Trống → "*" (dev); production set origin cụ thể.
		origin := cfg.CORSAllowOrigin
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// Preflight CORS (OPTIONS): trả 204 ngay, không chạy tiếp.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Read-only mode: reject all POST requests.
		// VI: node chỉ-đọc → chặn mọi POST (chỉ cho query). Hữu ích cho node
		// public phục vụ explorer mà không cho gửi tx.
		if cfg.ReadOnlyMode && r.Method == http.MethodPost {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "node is in read-only mode"})
			return
		}

		// Auth token check on mutating endpoints (POST).
		// VI: chỉ bảo vệ endpoint ghi (POST) bằng Bearer token; GET vẫn mở.
		if cfg.AuthToken != "" && r.Method == http.MethodPost {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != cfg.AuthToken {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}

		// Rate limiting. Vượt hạn mức token-bucket theo IP → 429.
		if limiter != nil {
			ip := extractIP(r)
			if !limiter.allow(ip) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
				return
			}
		}

		// Body size limit on POST. MaxBytesReader tự trả lỗi nếu body vượt ngưỡng.
		if cfg.MaxRequestBodyBytes > 0 && r.Method == http.MethodPost && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxRequestBodyBytes)
		}

		// Qua hết các chốt → chuyển cho handler thật xử lý.
		next.ServeHTTP(w, r)
	})
}

// extractIP rút IP client để rate-limit. Ưu tiên X-Forwarded-For (khi đứng sau
// reverse proxy), nếu không thì lấy phần IP của RemoteAddr (dạng ip:port).
func extractIP(r *http.Request) string {
	// Prefer X-Forwarded-For if behind reverse proxy.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// XFF có thể là "client, proxy1, proxy2" → lấy phần đầu (client gốc).
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
//
// VI: bộ giới hạn tốc độ kiểu "token bucket" theo từng IP. Mỗi IP có một xô
// token; mỗi request tiêu 1 token; token tự hồi theo thời gian với tốc độ rps.
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

// allow trả true nếu IP còn token để phục vụ request này (và trừ 1 token),
// false nếu hết (đã vượt tốc độ).
func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	// Lần đầu thấy IP → tạo xô đầy token.
	b, ok := l.buckets[ip]
	if !ok {
		b = &tokenBucket{tokens: float64(l.rps), lastTime: now}
		l.buckets[ip] = b
	}

	// Refill tokens: hồi token theo thời gian trôi qua kể từ lần trước, chặn
	// trần ở mức rps (không tích luỹ vô hạn).
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * float64(l.rps)
	if b.tokens > float64(l.rps) {
		b.tokens = float64(l.rps)
	}
	b.lastTime = now

	// Chưa đủ 1 token → từ chối.
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
