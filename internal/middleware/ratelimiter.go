package middleware

import (
	"context"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

/*
RateLimiter 미들웨어

WHY (왜 필요한가):
1. 스파이크 완충: 갑작스런 요청 폭주(버스트)를 흡수/차단하여 다운스트림 서비스 보호.
2. 공정 자원 분배: 과도한 단일 클라이언트 소비를 줄이고 평균 응답 안정화.
3. 간단한 보호 계층: 서킷브레이커/재시도 이전에 과도한 유입 자체를 줄임.

정책:
- 토큰 버킷(rate.NewLimiter): 초당 reqPerSec 토큰 재충전, 한 번에 burst 만큼 소비 허용.
- Allow() 모드: 즉시 통과/차단(대기 없음) → 짧은 지연 추구, 스로틀링 신호(429) 빠른 피드백.
- Wait() 모드(선택): 제한 초과 시 일정 시간(maxWait)까지 대기 → 처리율(throughput) 우선 시나리오.

구성 / 사용 예:
  rl := NewRateLimiter(200, 100) // 초당 200, 버스트 100
  handler = rl.Middleware(handler)
또는 대기 허용:
  handler = rl.WaitMiddleware(handler, 150*time.Millisecond)

Edge 게이트웨이(Envoy/Kong) 사용 시:
- 엣지에서 1차 글로벌 제한, 내부에서는 세밀한 경로·사용자별 제한 확장 가능.

주의:
- 현재 구현은 글로벌 하나의 limiter만 사용 (경로/사용자별 → map 키 관리 확장 가능).
- 초당 내부적으로 타임슬라이스 계산에 부동소수(rate.Limit) 사용 → 극단적 큰 값 튜닝 시 모니터링 필요.
*/

// RateLimiter: 단일(글로벌) 토큰 버킷 래퍼
type RateLimiter struct {
	limiter *rate.Limiter // 실제 토큰버킷 구현체
	enabled bool          // 비활성 시 오버헤드 제거를 위한 플래그
}

// NewRateLimiter:
// reqPerSec → 초당 허용 요청 수 (토큰 재충전 속도)
// burst     → 순간 폭발 허용량(버킷 용량)
// reqPerSec<=0 또는 burst<=0 이면 비활성 인스턴스 반환
func NewRateLimiter(reqPerSec float64, burst int) *RateLimiter {
	if reqPerSec <= 0 || burst <= 0 {
		return &RateLimiter{enabled: false}
	}
	// rate.NewLimiter(rate.Limit(reqPerSec), burst)
	// WHY: rate.Limit(float64) = 초당 토큰 재충전 속도, burst = 버킷 최대 용량
	lim := rate.NewLimiter(rate.Limit(reqPerSec), burst)
	return &RateLimiter{limiter: lim, enabled: true}
}

// Middleware: 비대기(Non-blocking) 즉시 판정 버전
// - 토큰 없으면 즉시 429 반환 (Retry-After 1초 힌트)
// - API 클라이언트는 백오프/재시도 전략 적용 가능
func (r *RateLimiter) Middleware(next http.Handler) http.Handler {
	// (1) 비활성/누락 상태 → 원본 그대로 통과
	if r == nil || !r.enabled || r.limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// (2) Allow() → 즉시 토큰 확인 (대기 X)
		if !r.limiter.Allow() {
			w.Header().Set("Retry-After", "1") // WHY: 간단한 재시도 지연 힌트(초 단위)
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		// (3) 토큰 소비 성공 → 다음 핸들러 실행
		next.ServeHTTP(w, req)
	})
}

// WaitMiddleware: 대기 허용 버전
// - 토큰이 없으면 maxWait 기간 내에서 토큰이 채워질 때까지 기다림
// - maxWait 초과 시 429
func (r *RateLimiter) WaitMiddleware(next http.Handler, maxWait time.Duration) http.Handler {
	// (1) 비활성 시 원본 그대로
	if r == nil || !r.enabled || r.limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// (2) 요청별 타임아웃 컨텍스트 생성 (대기 한도)
		ctx, cancel := context.WithTimeout(req.Context(), maxWait)
		defer cancel()

		// (3) Wait: 토큰 사용 가능한 시점까지 블록(또는 ctx timeout)
		if err := r.limiter.Wait(ctx); err != nil {
			// timeout 또는 context cancellation → 429
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		// (4) 토큰 확보 성공 → 핸들러 실행
		next.ServeHTTP(w, req)
	})
}
