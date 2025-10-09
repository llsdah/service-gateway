package middleware

import (
	"net/http" // HTTP 미들웨어 체인에 편입하기 위해 사용
	"sync"     // 동시성 안전한 상태 전이를 위해 뮤텍스 사용
	"time"     // 실패 시점 기록 및 타임아웃 계산
)

/*
CircuitBreaker (서킷 브레이커) 미들웨어

WHY (왜 필요한가):
1. 연쇄 장애(불안정한 업스트림 서비스) 보호:
   - 특정 백엔드/업스트림이 지속적으로 5xx를 반환하면 그대로 모든 요청을 흘려보낼 경우
     재시도/대기/타임아웃 누적으로 시스템 전체 자원 고갈 위험이 있음.
2. 실패 폭발 방지(Backoff 역할):
   - Open 상태에서는 즉시 실패를 응답하여 불필요한 네트워크/CPU 소비를 줄임.
3. 자동 회복(Half-Open 프로빙):
   - 일정 시간(openTimeout) 후 제한된 수(여기서는 단일 요청)만 통과시켜
     서비스가 회복했는지 확인, 성공 시 Closed 복귀 → 가용성 빠른 회복.
4. 단순/경량 구현:
   - 외부 라이브러리 없이 기본 구조(State machine + 카운터 + 타이밍)로 유지하여
     디버깅/운영 이해도 향상.

상태 정의:
- Closed: 정상 상태, 모든 요청 통과. 연속 실패 카운트가 threshold 이상이면 Open 전환.
- Open: 차단 상태, 요청 즉시 503(Service Unavailable). openTimeout 경과 시 Half-Open 전환.
- Half-Open: 소량(여기서는 1개) 요청만 허용해 회복 여부 테스트. 성공 → Closed, 실패 → Open.

튜닝 포인트:
- threshold: 연속 허용 실패 횟수.
- openTimeout: Open 유지 기간 (휴지기).
- halfTimeout: (현재 구현에서는 사용 예시 확장 가능. 필요 시 Half-Open에서 최대 대기 시간 등 정책 확장에 활용)

확장 아이디어(필요 시 추가 가능):
- 실패 기준을 단순 status >= 500 대신 타임아웃/네트워크 에러 포함.
- 슬라이딩 윈도우(시간 기반 실패율) 도입.
- 다중 업스트림 키별(경로/호스트별) 서킷 브레이커 맵 관리.
*/

type cbState int // 내부 상태 머신(enum 유사)

const (
	stateClosed   cbState = iota // 0: 정상 통과 상태
	stateOpen                    // 1: 차단 상태
	stateHalfOpen                // 2: 제한적 프로브 상태
)

// CircuitBreaker 구조체: 상태 + 설정 + 동시성 보호
type CircuitBreaker struct {
	mu          sync.Mutex    // 상태/카운터 보호용 뮤텍스
	failures    int           // 연속 실패 횟수
	threshold   int           // Open 전환 연속 실패 임계치
	lastFailure time.Time     // 마지막 실패 시각 (Open 유지 판단)
	openTimeout time.Duration // Open 유지 시간 (경과 후 Half-Open 시도)
	halfTimeout time.Duration // (미사용 확장 필드) Half-Open 유지/만료 정책에 활용 가능
	state       cbState       // 현재 상태(Closed/Open/Half-Open)
	probing     bool          // Half-Open에서 이미 프로브 중인지 표시(동시 진입 차단)
}

// NewCircuitBreaker: 파라미터 기반 생성 (threshold <=0 시 안전 기본값 적용)
func NewCircuitBreaker(threshold int, openTimeout, halfTimeout time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 5 // 기본 임계치: 5회 연속 실패 후 Open
	}
	return &CircuitBreaker{
		threshold:   threshold,
		openTimeout: openTimeout,
		halfTimeout: halfTimeout,
		state:       stateClosed, // 초기 상태는 Closed
	}
}

// statusWriter: 다운스트림 핸들러의 실제 응답 status code 관찰을 위해 래퍼 작성
type statusWriter struct {
	http.ResponseWriter
	status int // 기록할 상태 코드 (기본 200으로 시작)
}

// WriteHeader 오버라이드: 상태 코드 저장 후 실제 헤더 전송
func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Middleware: 외부에서 cb.Middleware(next) 형태로 체인에 삽입
func (cb *CircuitBreaker) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// (1) 현재 상태가 요청을 허용하는지 검사 (Open 이면 즉시 503)
		if !cb.allow() {
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}

		// (2) 상태 코드 관찰을 위한 래퍼 생성 (기본값 200)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		// (3) 다음 핸들러 실행(실제 비즈니스/프록시 처리)
		next.ServeHTTP(sw, r)

		// (4) 실패 판정: status >= 500 을 실패로 간주 (추가 네트워크 에러는 별도 확장 가능)
		failed := sw.status >= 500

		// (5) 결과 반영: 성공/실패에 따라 상태 전이
		cb.onResult(!failed)
	})
}

// allow: 현재 상태에서 요청 허용 여부 판단 + Half-Open 전이 처리
func (cb *CircuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case stateOpen:
		// Open 유지 시간 경과 확인 → Half-Open 전환 시도
		if time.Since(cb.lastFailure) >= cb.openTimeout {
			cb.state = stateHalfOpen
			cb.probing = false // 새로운 프로브 기회 초기화
		} else {
			return false // 아직 Open 유지 → 차단
		}
		fallthrough // Half-Open 로직 흐름 계속
	case stateHalfOpen:
		// Half-Open: 단일 프로브만 허용 (동시 다중 요청 방지)
		if cb.probing {
			return false
		}
		cb.probing = true
		return true
	default: // stateClosed
		return true // Closed 는 항상 허용
	}
}

// onResult: 요청 처리 결과(success 여부)에 따라 상태/카운터 조정
func (cb *CircuitBreaker) onResult(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case stateHalfOpen:
		if success {
			// 프로브 성공 → 완전 회복 (Closed)
			cb.state = stateClosed
			cb.failures = 0
		} else {
			// 프로브 실패 → 다시 Open (차단 재개)
			cb.state = stateOpen
			cb.lastFailure = time.Now()
		}
		cb.probing = false // 프로브 종료
	case stateClosed:
		if success {
			// 성공이면 실패 카운터 리셋
			cb.failures = 0
		} else {
			// 실패 누적 증가
			cb.failures++
			cb.lastFailure = time.Now()
			// 임계치 도달 시 Open 전환 (차단 개시)
			if cb.failures >= cb.threshold {
				cb.state = stateOpen
			}
		}
	case stateOpen:
		// Open 상태에서 onResult 호출은 거의 없음(allow에서 이미 차단).
		// 확장: 회복 힌트 로직 추가 가능.
	}
}
