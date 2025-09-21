package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"service-gateway/internal/header"
	"service-gateway/internal/router"
	"strings"
	"time"
)

// 아웃바운드 HTTP 클라이언트(커넥션 풀/타임아웃 포함)
func NewClient(readTO, writeTO, idleTO time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 60 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		TLSHandshakeTimeout:   3 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   readTO + writeTO + 2*time.Second, // 상한선
	}
}

type ReverseProxy struct {
	Client *http.Client
}

// patch rewrite + proxy
func (p *ReverseProxy) Proxy(ctx context.Context, w http.ResponseWriter, r *http.Request, routeName, scheme, host, pathRewrite, method string, params map[string]string) {
	target := &url.URL{
		Scheme: scheme,
		Host:   host,
	}
	//	path := BuildUpstreamPath(rt, req.URL.Path, params)
	// scheme/host 조합해서 최종 target URL 구성

	outReq := r.Clone(ctx)
	outReq.URL = target.ResolveReference(&url.URL{Path: pathRewrite})
	outReq.RequestURI = "" // net/http requirement
	outReq.Method = method

	// 원본 바디를 읽음
	var bodyBytes []byte
	if r.Body != nil && r.Body != http.NoBody {
		bodyBytes, _ = io.ReadAll(r.Body)
	}
	defer r.Body.Close()

	/**
	// key가 있으면 수정
	if len(bodyBytes) > 0 {
		var bodyMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &bodyMap); err == nil {
			if keyVal, ok := bodyMap["key"].(string); ok {
				// 요청 헤더에서 X-Fw-Session-Id 읽기
				sessionID := r.Header.Get("X-Fw-Session-Id")
				if sessionID != "" {
					// key 값에 prefix 붙임
					bodyMap["key"] = sessionID + keyVal
				}
			}
			// 수정된 JSON 다시 직렬화
			bodyBytes, _ = json.Marshal(bodyMap)
		}
	}
	*/
	var sessionID string
	if routeName == "save-user" && len(bodyBytes) > 0 {
		var bodyMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &bodyMap); err == nil {
			if v, ok := bodyMap["key"].(string); ok && v != "" {
				// 요청 헤더에서 X-Fw-Session-Id 읽기
				sessionID = v
			}
			log.Printf("session ID : %s", sessionID)
			// 수정된 JSON 다시 직렬화
			//bodyBytes, _ = json.Marshal(bodyMap)
		}
	} else if routeName == "find-user-info" {
		if r.Header.Get("X-Fw-Session-Id") == "" {
			log.Printf("X - Session - id is null")
			http.Error(w, "X - Session - id is null ", http.StatusBadGateway)
			return
		}

		// 생성
		bodyMap := make(map[string]interface{})
		bodyMap["key"] = r.Header.Get("X-Fw-Session-Id")
		bodyMap["field"] = params["field"]

		bodyBytes, _ = json.Marshal(bodyMap)

	}

	// 새로운 body 세팅
	outReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	outReq.ContentLength = int64(len(bodyBytes))
	if len(bodyBytes) == 0 {
		outReq.Body = http.NoBody
		outReq.ContentLength = 0
		outReq.Header.Del("Content-Length")
	} else {
		outReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(bodyBytes)))
		outReq.Header.Del("Transfer-Encoding")
	}

	// Hop by hop 헤더 정리
	outReq.Header.Del("Connection")
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Keep-Alive")
	outReq.Header.Del("Transfer-Encoding")
	outReq.Header.Del("Upgrade")

	// DEBUG: 실제 전송 직전 덤프
	if dump, err := httputil.DumpRequestOut(outReq, true); err == nil {
		log.Printf("[OUTBOUND]\n%s", dump)
	}

	resp, err := p.Client.Do(outReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("upstream error : %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 클라가 보낸 원본

	for k, v := range resp.Header {
		log.Printf("resp. Header: %s=%s", k, v)
	}

	fwHeader := resp.Header.Get("X-Fw-Header")
	bumped := header.BumpTCIDSRNO(fwHeader)

	// 업스트림 응답 헤더를 먼저 복사
	copyHeader(w.Header(), resp.Header)

	// 세션 ID 발급 및 응답 헤더/쿠키에 추가
	// sessionID := getOrCreateSessionID(r)
	w.Header().Set("X-Fw-Session-Id", sessionID)
	w.Header().Set("X-Fw-Header", bumped)

	/**
	http.SetCookie(w, &http.Cookie{
		Name:     "JSESSIONID",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	*/

	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func Dump(req *http.Request) string {
	b, _ := httputil.DumpRequest(req, true)
	return string(b)
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// PathRewrite 템플릿에서 {var} 치환
func applyPathParams(path string, params map[string]string) string {
	if len(params) == 0 || !strings.Contains(path, "{") {
		return path
	}
	for k, v := range params {
		path = strings.ReplaceAll(path, "{"+k+"}", url.PathEscape(v))
	}
	return path
}

// 업스트림 URL path 결정
func BuildUpstreamPath(rt *router.Route, origPath string, params map[string]string) string {
	rewritten := rt.Backend.PathRewrite
	if strings.TrimSpace(rewritten) == "" {
		// path_rewrite 없으면 원본 유지
		return origPath
	}
	return applyPathParams(rewritten, params)
}
