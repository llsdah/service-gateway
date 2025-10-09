package middleware

import (
	"net/http" // WHY: HTTP 미들웨어 체인 구현을 위해 표준 net/http 사용

	"service-gateway/internal/header" // WHY: X-Fw-Header(TCID 등) 파싱/직렬화/증분 유틸 재사용
)

/*
FwHeaderTrace 미들웨어

WHY (왜 필요한가):
1. 단일 상관관계 ID 정책: 외부 X-Request-Id 대신 사내 규격 X-Fw-Header 내 TCID를
   요청 추적(로그/메트릭/분석) 상관관계의 유일 ID로 사용.
2. 추적 정보 일원화: 여러 헤더(X-Request-Id, Trace-Id 등) 난립을 방지하고, 운영/분석 파이프라인 단순화.
3. Hop 증가 기록: 응답 시 TCIDSRNO 값을 +1 하여 이 게이트웨이가 몇 번째 hop/응답자인지 타임라인 복원 용이.
4. 안전한 보강: 들어온 요청에 TCID가 이미 있으면 보존(상관관계 유지), 없으면 최초 생성.
5. 서버 메타 삽입: BizSrvcCd(서비스 코드), BizSrvcIp(현재 노드 Host) 자동 주입으로 운영/장애 분석 시 출처 명확화.

동작 요약:
- 요청 수신:
  a) X-Fw-Header 파싱 → map
  b) TCID 없으면 생성, TCIDSRNO 없으면 "0001"
  c) BizSrvcCd/BizSrvcIp 채움
  d) 재직렬화 후 r.Header 갱신
- 요청 처리(next.ServeHTTP)
- 응답 직전:
  e) TCIDSRNO +1 (현재 게이트웨이 hop 반영)
  f) 최종 X-Fw-Header 응답 헤더에 셋

확장 아이디어(추후 필요 시):
- Hop 증가 위치를 요청 직후로 바꾸는 모드(ingress vs egress 차별)
- TCIDSRNO 포맷 가변 자릿수 / overflow 정책
*/

// FwHeaderTrace:
// bizCode → BizSrvcCd로 기록할 식별자(환경/설정에서 주입)
// next    → 다음 미들웨어 또는 실제 핸들러
func FwHeaderTrace(bizCode string, next http.Handler) http.Handler {
	// http.HandlerFunc로 래핑해 체인 요소로 반환
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// (1) 원본 헤더 문자열 추출 (없으면 빈 문자열)
		raw := r.Header.Get("X-Fw-Header")

		// (2) TCID/TCIDSRNO/BizSrvcCd/BizSrvcIp 보강 (TCID 존재 시 보존)
		enhanced := header.EnsureTCIDForRequest(raw, bizCode, r.Host)

		// (3) 요청 헤더 갱신: 이후 핸들러/프록시 호출 시 동일 값 전파
		r.Header.Set("X-Fw-Header", enhanced)

		// (4) 응답 시 SRNO 증가 처리를 위해 커스텀 writer 준비
		sw := &fwHeaderWriter{
			ResponseWriter: w,
			fwHeader:       enhanced, // 현재 단계까지의 헤더 스냅샷
		}

		// (5) 다음 핸들러 실행
		next.ServeHTTP(sw, r)
	})
}

// fwHeaderWriter: 응답 직전(TCIDSRNO +1) 주입을 위해 ResponseWriter 래핑
type fwHeaderWriter struct {
	http.ResponseWriter        // 내장 위임 (기본 기능 그대로)
	fwHeader            string // 요청 단계에서 강화된 X-Fw-Header 문자열
}

// WriteHeader:
// WHY: 응답 status 코드를 내려보내기 “직전” 시점이므로
//
//	최종 hop 정보(TCIDSRNO 증가)를 반영하기 적절.
func (w *fwHeaderWriter) WriteHeader(code int) {
	// (a) SRNO +1 (예: 0001 → 0002)
	final := header.BumpTCIDSRNO(w.fwHeader)
	// (b) 실제 응답 헤더에 최종 값 주입
	w.Header().Set("X-Fw-Header", final)
	// (c) 원래 WriteHeader 호출로 진행
	w.ResponseWriter.WriteHeader(code)
}
