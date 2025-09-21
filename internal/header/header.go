package header

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 랜덤 8자리 (숫자+소문자)
func random8() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}

// Host 헤더를 기반으로 8자리 hostname 추출 (공백 패딩 금지)
func shortHostFromHeader(host string) string {
	// "example.com:8080" → "example.com"
	h := host
	if idx := strings.Index(h, ":"); idx > 0 {
		h = h[:idx]
	}
	if len(h) >= 8 {
		return h[:8]
	}
	// 공백 대신 '0'으로 패딩 (TCID에 공백 방지)
	return h + strings.Repeat("0", 8-len(h))
}

// Host 헤더 그대로 IP 필드에 저장
func hostIPFromHeader(host string) string {
	// host: "example.com:8080" → "example.com"
	if idx := strings.Index(host, ":"); idx > 0 {
		return host[:idx]
	}
	return host
}

// TCID 생성 (Host 기반)
func generateTCID(host string) string {
	now := time.Now()
	date := now.Format("20060102")
	tm := now.Format("150405") + "00"
	return date + shortHostFromHeader(host) + tm + random8()
}

// 최종 헤더 문자열 생성
func MakeFwHeader(bizCode, idemKey, fwAuth, host string) string {
	h := map[string]string{
		"TCID":            generateTCID(host),
		"TCIDSRNO":        "0001",
		"BizSrvcCd":       bizCode,
		"BizSrvcIp":       hostIPFromHeader(host),
		"IdempotencyKey":  idemKey, // 존재 시만 체크
		"FwAuthorization": fwAuth,  // 존재 시만 인증
	}

	var parts []string
	for k, v := range h {
		if v != "" { // 값이 없는 건 제외
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return strings.Join(parts, ";")
}

// Parse: "K=V;K2=V2" → map[K]V  (공백 trim, 빈 항목 무시)
func Parse(headerVal string) map[string]string {
	m := make(map[string]string)
	if headerVal == "" {
		return m
	}
	parts := strings.Split(headerVal, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		kv := strings.SplitN(p, "=", 2)
		k := strings.TrimSpace(kv[0])
		v := ""
		if len(kv) > 1 {
			v = strings.TrimSpace(kv[1])
		}
		if k != "" {
			m[k] = v
		}
	}
	return m
}

// Serialize: map → "K=V;K2=V2" (값이 빈 것은 제외)
func Serialize(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(out, ";")
}

// 서버 기준 필드 적용(덮어쓰기): TCID/TCIDSRNO/BizSrvcCd/BizSrvcIp
func ApplyServerSideFields(in map[string]string, bizCode, host string) map[string]string {
	if in == nil {
		in = make(map[string]string)
	}
	in["TCID"] = generateTCID(host)
	in["TCIDSRNO"] = "0001"
	in["BizSrvcCd"] = bizCode
	in["BizSrvcIp"] = hostIPFromHeader(host)
	return in
}

// 정규식: "TCIDSRNO=00001" 같은 부분 찾기 (대소문자 무시)
var reTCIDSRNO = regexp.MustCompile(`(?i)(\bTCIDSRNO=)(\d+)`)

// 응답 헤더 문자열에서 TCIDSRNO 값만 +1
func BumpTCIDSRNO(raw string) string {
	if raw == "" {
		return ""
	}
	return reTCIDSRNO.ReplaceAllStringFunc(raw, func(m string) string {
		sub := reTCIDSRNO.FindStringSubmatch(m)
		if len(sub) != 3 {
			return m
		}
		prefix, digits := sub[1], sub[2]
		n, err := strconv.Atoi(digits)
		if err != nil {
			return m
		}
		n++
		newDigits := strconv.Itoa(n)
		// 원래 자리수 유지 (예: "00001" → "00002")
		if len(newDigits) < len(digits) {
			newDigits = strings.Repeat("0", len(digits)-len(newDigits)) + newDigits
		}
		return prefix + newDigits
	})
}
