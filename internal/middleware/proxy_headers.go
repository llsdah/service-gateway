package middleware

import (
	"net"
	"net/http"
	"strings"
)

/*
ProxyHeaders 미들웨어

WHY (왜 필요한가):
1. 실제 클라이언트 IP 보존: 프록시/게이트웨이 체인을 거치며 X-Forwarded-For(XFF)에
   원 IP를 누적(append) → 보안 감사 / 차단 정책 / 관측에서 원 IP 식별 가능.
2. 프로토콜/호스트 정보 전달: TLS 종료 혹은 중간 프록시 영향으로 r.TLS가 nil일 수 있어
   X-Forwarded-Proto, X-Forwarded-Host 를 명시적으로 보강.
3. 표준화: 이후 로그/추적 로직이 별도 파싱 없이 헤더만 신뢰하도록 통일.

정책:
- X-Forwarded-For:
  - 기존 값 존재 시 "기존, 새IP" 형태 append (중복 방지 위해 간단히 추가; 추가 필터가 필요하면 후속 단계에서 정규화 가능)
  - RemoteAddr 파싱 실패/빈 값이면 미추가
- X-Forwarded-Proto:
  - 없을 때만 세팅 (이미 상위 프록시가 넣은 값 우선)
  - TLS 존재 여부로 http / https
- X-Forwarded-Host:
  - 없고 r.Host 있으면 세팅

보안 주의:
- 외부(신뢰 안 되는) 직접 트래픽이 이 레이어까지 들어온다면,
  클라이언트가 임의로 조작한 X-Forwarded-* 헤더가 이미 존재할 수 있음.
  → “신뢰 경계” 앞단에서 초기화/삭제 후 전달하는 정책 권장.
*/

// ProxyHeaders: 위 설명대로 X-Forwarded-* 헤더 보강
func ProxyHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// (1) 클라이언트 IP 추출 (host:port → host)
		ip := clientIP(r.RemoteAddr)
		if ip != "" {
			// (2) 기존 XFF 존재 시 append, 없으면 신규 세팅
			if prior := r.Header.Get("X-Forwarded-For"); prior != "" {
				r.Header.Set("X-Forwarded-For", prior+", "+ip)
			} else {
				r.Header.Set("X-Forwarded-For", ip)
			}
		}

		// (3) 프로토콜 보강 (이미 있으면 상위 값을 존중)
		if r.Header.Get("X-Forwarded-Proto") == "" {
			if r.TLS != nil {
				r.Header.Set("X-Forwarded-Proto", "https")
			} else {
				r.Header.Set("X-Forwarded-Proto", "http")
			}
		}

		// (4) 호스트 보강 (이미 있으면 상위 프록시 값 유지)
		if r.Header.Get("X-Forwarded-Host") == "" && r.Host != "" {
			r.Header.Set("X-Forwarded-Host", r.Host)
		}

		// (5) 다음 핸들러로 위임
		next.ServeHTTP(w, r)
	})
}

// clientIP: "IP:Port" 형태 RemoteAddr → IP 부분만 추출
func clientIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// 콜론이 없는 순수 host만 들어온 경우 그대로 사용
		if strings.Count(remoteAddr, ":") == 0 {
			return remoteAddr
		}
		return ""
	}
	return host
}
