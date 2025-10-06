package middleware

import (
	"context"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

type RateLimiter struct {
	limiter *rate.Limiter
	enabled bool
}

func NewRateLimiter(reqPerSec float64, burst int) *RateLimiter {
	if reqPerSec <= 0 || burst <= 0 {
		return &RateLimiter{enabled: false}
	}
	// rate.NewLimiter는 초당 reqPerSec, 버스트 burst
	lim := rate.NewLimiter(rate.Limit(reqPerSec), burst)
	return &RateLimiter{limiter: lim, enabled: true}
}

func (r *RateLimiter) Middleware(next http.Handler) http.Handler {
	if r == nil || !r.enabled || r.limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// 즉시 거부(대기 없음). 필요 시 Wait 이용해 대기 허용 가능.
		if !r.limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// 선택: 대기하는 버전
func (r *RateLimiter) WaitMiddleware(next http.Handler, maxWait time.Duration) http.Handler {
	if r == nil || !r.enabled || r.limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), maxWait)
		defer cancel()
		if err := r.limiter.Wait(ctx); err != nil {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, req)
	})
}
