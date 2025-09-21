package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	config "service-gateway/internal/configs"
	"service-gateway/internal/header"
	"service-gateway/internal/httpx"
	"service-gateway/internal/model"
	"service-gateway/internal/store"
	"time"
)

type DynamicGateway struct {
	Repo   store.Repository
	Client *http.Client
}

type requestBody struct {
	URL  string          `json:"url"`
	Data json.RawMessage `json:"data"`
}

func NewDynamicGateway(repo store.Repository, timeout time.Duration) *DynamicGateway {
	return &DynamicGateway{
		Repo:   repo,
		Client: &http.Client{Timeout: timeout},
	}
}

func (h *DynamicGateway) Post(w http.ResponseWriter, r *http.Request) {

	coniglog := config.AppConfig.Application.Log

	inboundlogRequest, err := h.Repo.ExistConfig(r.Context(), coniglog.Inbound.Request)

	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.NewError("failed to log config", err))
	}

	if inboundlogRequest {

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("failed to read request body: %v", err)
			// 필요하다면 에러 처리
		}
		defer r.Body.Close()

		// body를 다시 사용할 수 있게 복원
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		headerJson, _ := json.Marshal(r.Header)

		log.Printf("source request log : body=%s, header=%s", string(bodyBytes), string(headerJson))

	}

	bodyBytes, err := io.ReadAll(r.Body)

	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.NewError("failed to read body", err))
	}
	defer r.Body.Close()

	var in requestBody

	if err := json.Unmarshal(bodyBytes, &in); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.NewError("invalid JSON", err))
		return
	}
	var requestData model.RequestData
	requestData.RequestURL = in.URL
	// DB 확인
	requestData, err = h.Repo.FindRequestData(r.Context(), requestData)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("findRequestData db error", err))

		return
	}
	flag, err := h.Repo.ExistAPIGroup(r.Context(), requestData)

	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("existAPIGroup db error", err))
		return
	}

	if !flag {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.NewError("URL not allowed", err))
		return
	}

	// 업스트림 바디 준비: 메서드별 처리
	method := r.Method
	var outBody io.Reader
	var hasBody bool

	// 보통 GET/HEAD/DELETE는 바디가 없지만, DELETE/PATCH/PUT은 종종 바디 사용.
	// 규칙: in.Data가 있으면 보냄, 없으면 바디 없음.
	if len(in.Data) > 0 && string(in.Data) != "null" {
		outBody = bytes.NewReader(in.Data)
		hasBody = true
	}

	// 아래 로직
	/**
	// data 원형 바디
	outBody := []byte{}
	if len(in.Data) > 0 && string(in.Data) != "null" {
		outBody = in.Data
	}
	*/

	ctx, cancel := context.WithTimeout(r.Context(), h.Client.Timeout)
	defer cancel()

	// 1) 서비스코드로 백엔드 호스트 조회
	host := config.AppConfig.Hosts[requestData.ApiGroupCode]
	upstreamURL := host + in.URL

	log.Printf("host : %s, URL : %s, apiGroupCode : %s", host, in.URL, requestData.ApiGroupCode)

	// 2) 업스트림 요청 생성 (메서드 그대로 사용)
	reqUp, err := http.NewRequestWithContext(ctx, method, upstreamURL, outBody)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("build outbound request failed", err))
		return
	}

	/**
	// 대상 POST
	reqUp, err := http.NewRequestWithContext(ctx, http.MethodPost, host+in.URL, bytes.NewReader(outBody))
	if err != nil {

		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("build outbound request failed", err))
		return
	}
	*/

	// ✅ FW Header 생성 (Host 기반)
	// 1) 클라이언트가 보낸 X-Fw-Header 파싱
	inFwRaw := r.Header.Get("X-Fw-Header")
	inFw := header.Parse(inFwRaw)
	if ua := r.Header.Get("X-Fw-Header"); ua != "" {
		reqUp.Header.Set("X-Fw-Header", ua)
	}

	// 3) 서버 강제 필드 덮어쓰기 (TCID 등) + BizSrvc
	// Cd는 설정값 사용
	bizCode := "SAMPLE" // or config.AppConfig.BizServiceCode
	merged := header.ApplyServerSideFields(inFw, bizCode, r.Host)

	// 4) 다시 문자열로 직렬화하여 업스트림에 전달
	outFw := header.Serialize(merged)
	r.Header.Set("X-Fw-Header", outFw)
	// 헤더 복사 (필요시 hop-by-hop 필터링 추가 가능)
	copyHeaders(reqUp.Header, r.Header)
	if hasBody && reqUp.Header.Get("Content-Type") == "" {
		reqUp.Header.Set("Content-Type", "application/json")
	}

	for k, v := range reqUp.Header {
		log.Printf("/Gateway reqUp Header: %s=%s", k, v)
	}

	resp, err := h.Client.Do(reqUp)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, httpx.NewError("upstream request failed", err))
		return
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		log.Printf("/Gateway resp Header: %s=%s", k, v)
	}

	fwHeader := resp.Header.Get("X-Fw-Header")
	bumped := header.BumpTCIDSRNO(fwHeader)

	copyHeaders(w.Header(), resp.Header)
	w.Header().Set("X-Fw-Header", bumped)
	w.WriteHeader(resp.StatusCode)

	_, _ = io.Copy(w, resp.Body)

}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
