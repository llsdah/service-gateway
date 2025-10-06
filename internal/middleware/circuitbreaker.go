package middleware

import (
	"net/http"
	"sync"
	"time"
)

type cbState int

const (
	stateClosed cbState = iota
	stateOpen
	stateHalfOpen
)

type CircuitBreaker struct {
	mu          sync.Mutex
	failures    int
	threshold   int
	lastFailure time.Time
	openTimeout time.Duration
	halfTimeout time.Duration
	state       cbState
	probing     bool // half-open에서 동시 프로브 방지
}

func NewCircuitBreaker(threshold int, openTimeout, halfTimeout time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 5
	}
	return &CircuitBreaker{
		threshold:   threshold,
		openTimeout: openTimeout,
		halfTimeout: halfTimeout,
		state:       stateClosed,
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (cb *CircuitBreaker) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cb.allow() {
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		failed := sw.status >= 500
		cb.onResult(!failed)
	})
}

func (cb *CircuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case stateOpen:
		// openTimeout 경과 시 half-open으로 전환
		if time.Since(cb.lastFailure) >= cb.openTimeout {
			cb.state = stateHalfOpen
			cb.probing = false
		} else {
			return false
		}
		fallthrough
	case stateHalfOpen:
		// half-open은 단일 프로브만 허용
		if cb.probing {
			return false
		}
		cb.probing = true
		return true
	default: // stateClosed
		return true
	}
}

func (cb *CircuitBreaker) onResult(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case stateHalfOpen:
		if success {
			// 성공 시 회복
			cb.state = stateClosed
			cb.failures = 0
		} else {
			// 실패 시 즉시 오픈
			cb.state = stateOpen
			cb.lastFailure = time.Now()
		}
		cb.probing = false
	case stateClosed:
		if success {
			cb.failures = 0
		} else {
			cb.failures++
			cb.lastFailure = time.Now()
			if cb.failures >= cb.threshold {
				cb.state = stateOpen
			}
		}
	case stateOpen:
		// open 상태에서는 allow()가 차단하므로 여기 거의 안 옴
	}
}
