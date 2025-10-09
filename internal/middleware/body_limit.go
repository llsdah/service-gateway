package middleware

import (
	"net/http"
	"strconv"
)

/*
BodyLimit 미들웨어 (라인별 상세 주석판)

WHY (왜 이 미들웨어가 필요한가):
1. 방어선 이중화: 엣지(Envoy/Kong/Nginx 등) 또는 Ingress 에서 요청 크기 제한을 걸어도
   - 잘못된 설정 / 누락 / 우회 트래픽 이슈로 인해 백엔드 애플리케이션이 과도한 본문(payload)을 직접 읽어버릴 위험이 있음.
2. 메모리 보호: Go http.Server 는 기본적으로 요청 본문을 스트리밍하지만,
   - 애플리케이션 코드/미들웨어에서 body 전체를 읽어 검증(JSON validation 등)하는 순간 메모리 사용량이 입력 크기와 선형.
3. 조기 차단(Early Fail): (a) Content-Length 헤더가 존재하고 (b) 한도를 초과하면
   - 굳이 연결을 유지하며 데이터를 계속 받지 않고 즉시 413(Request Entity Too Large) 반환 → 자원 낭비 감소.
4. 스트리밍 초과 방지: Content-Length 가 없거나 신뢰할 수 없을 때도
   - http.MaxBytesReader 로 실제 Read 시점에 초과를 차단(읽기 중단 + 413 반환) → 서버 안정성 향상.

사용 방식:
  handler = middleware.BodyLimit(handler, 10<<20) // 10MiB
  - maxBytes <= 0 이면 (비활성화) 원본 핸들러 그대로 반환.

주의:
- MaxBytesReader 는 초과 읽기 시 413을 WriteHeader 하지만, 상위에서 이미 헤더/본문을 일부 쓴 경우 500 등으로 보일 수 있으니
  BodyLimit 을 체인의 앞부분(가능한 한 초기) 에 배치하는 것이 안전.
*/

// BodyLimit:
// next     → 다음(실제 비즈니스/프록시) 핸들러
// maxBytes → 허용 최대 본문 크기(바이트). Content-Length 및 실제 읽기 모두에 적용.
func BodyLimit(next http.Handler, maxBytes int64) http.Handler {
	// (1) 비활성 조건: maxBytes <= 0 인 경우 제한 로직을 전혀 적용하지 않고 바로 next 반환.
	if maxBytes <= 0 {
		return next
	}

	// (2) 활성 시 HTTP 핸들러 래핑
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// --- Content-Length 사전 검사 단계 ---
		// WHY: 요청이 시작되기도 전에(본문 스트리밍 되기 전에) 한도 초과를 즉시 판단 가능하면
		//      커넥션 / 네트워크 / 메모리 낭비를 최소화.
		if cl := r.Header.Get("Content-Length"); cl != "" { // 헤더가 제공된 경우에만 검사
			if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n > maxBytes { // 숫자 파싱 성공 && 한도 초과
				http.Error(w, "request entity too large", http.StatusRequestEntityTooLarge) // 413 응답
				return                                                                      // 조기 종료(본문 읽지 않음)
			}
			// 파싱 실패(err != nil) 또는 n <= maxBytes 인 경우에는 계속 진행 (실제 바디 검증은 아래 MaxBytesReader에서 2차 처리)
		}

		// --- 실제 읽기(read) 제한 래핑 ---
		// WHY: (a) Content-Length 가 누락되었거나 (b) 클라이언트가 헤더를 속인 경우
		//      실제 Read 시점에서 스트림을 감시하여 maxBytes 초과 시 조기 중단.
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

		// --- 다음 핸들러 실행 ---
		// 여기까지 오면 사전(Content-Length) 검사 통과 → 실제 처리에서 필요한 만큼 body를 Read.
		// 상위 핸들러(예: JSON 파서)가 초과 바디를 읽으면 MaxBytesReader 가 413을 셋업.
		next.ServeHTTP(w, r)
	})
}
