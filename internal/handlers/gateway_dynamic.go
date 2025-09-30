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
	"service-gateway/internal/kafkax"
	"service-gateway/internal/model"
	"service-gateway/internal/store"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
)

type DynamicGateway struct {
	Repo   store.Repository
	Client *http.Client
	Log    kafkax.Publisher // kafka
}

type requestBody struct {
	URL  string          `json:"url"`
	Data json.RawMessage `json:"data"`
}

// kafka
type gwLog struct {
	Ts           string `json:"timeStamp"`
	Tcid         string `json:"tcId"`
	TcidSrno     string `json:"tcIdSrno"`
	TcidCreMabd  string `json:"tcIdCreMabd"`
	BizSrvcCd    string `json:"bizSrvcCd"`
	BizSrvcIp    string `json:"bizSrvcIp"`
	RasTyp       string `json:"rasTyp"`
	NmlYn        string `json:"nmlYn"`
	ApiPath      string `json:"apiPath"`
	CmnSrvcCd    string `json:"cmnSrvcCd"`
	CmnBizSrvcCd string `json:"cmnBizSrvcCd"`
	ApiGroupCd   string `json:"apiGroupCd"`
	ApiCd        string `json:"apiCd"`
	Guid         string `json:"guid"`
	InterId      string `json:"interId"`
	MessageCd    string `json:"messageCd"`
	HostName     string `json:"hostName"`
	Body         string `json:"data,omitempty"`
	//Phase     string                 `json:"phase"` // inbound_request, outbound_request, outbound_response, inbound_response
	//Method    string                 `json:"method"`
	//URL       string                 `json:"url"`
	//Status    int                    `json:"status,omitempty"`
	//LatencyMs int64                  `json:"latency_ms,omitempty"`
	//Headers   map[string][]string    `json:"headers"`
	//Body      string                 `json:"body,omitempty"`
	GroupCode string `json:"group_code,omitempty"`
	ApiCode   string `json:"api_code,omitempty"`
	//Upstream  string                 `json:"upstream,omitempty"`
	//Meta      map[string]interface{} `json:"meta,omitempty"`
}

func truncate(s []byte, n int) string {
	if len(s) <= n {
		return string(s)
	}
	return string(s[:n])
}

func NewDynamicGateway(repo store.Repository, timeout time.Duration, log kafkax.Publisher) *DynamicGateway {
	return &DynamicGateway{
		Repo:   repo,
		Client: &http.Client{Timeout: timeout},
		Log:    log,
	}
}

func (h *DynamicGateway) Post(w http.ResponseWriter, r *http.Request) {

	// trace background 작업
	tracer := otel.Tracer(config.AppConfig.Application.Name)
	_, span := tracer.Start(r.Context(), "Gateway")
	defer span.End()

	// log 적재를 위한
	configlog := config.AppConfig.Application.Log
	// ✅ FW Header 생성 (Host 기반)
	// 1) 클라이언트가 보낸 X-Fw-Header 파싱
	inFwRaw := r.Header.Get("X-Fw-Header")
	inFw := header.Parse(inFwRaw)

	// 3) BizSrvcCd 결정: 헤더 > 바디 > 기본값(SMP)
	bizCode := "SMP"
	if v, ok := inFw["BizSrvcCd"]; ok && v != "" {
		bizCode = v
	} else {
		// body에서 BizSrvcCd 추출
		bodyBytes, _ := io.ReadAll(r.Body)
		defer r.Body.Close()
		var bodyMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &bodyMap); err == nil {
			if v, ok := bodyMap["BizSrvcCd"]; ok {
				if s, ok := v.(string); ok && s != "" {
					bizCode = s
				}
			}
		}
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 복원
	}
	merged := header.ApplyServerSideFields(inFw, bizCode, r.Host)

	// ==== inbound request log ====
	if ok, _ := h.Repo.ExistConfig(r.Context(), configlog.Inbound.Request); ok {
		var bodyBytes []byte
		if r.Method == http.MethodGet {
			bodyBytes = []byte{}
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 빈 body로 복원
		} else {
			bodyBytes, _ = io.ReadAll(r.Body)
			defer r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 복원
		}
		ev := gwLog{
			Ts:           time.Now().Format("20060102150405000"),
			Tcid:         merged["TCID"],
			TcidSrno:     merged["TCIDSRNO"],
			TcidCreMabd:  "00",
			BizSrvcCd:    merged["BizSrvcCd"],
			BizSrvcIp:    merged["BizSrvcIp"],
			RasTyp:       "11", // 11 요청, 12 응답, 21 : 송신 22 수신
			NmlYn:        "Y",
			ApiPath:      r.URL.String(),
			CmnSrvcCd:    "",
			CmnBizSrvcCd: "",
			ApiGroupCd:   config.AppConfig.Application.GroupCode,
			ApiCd:        "00001",
			Guid:         "",
			InterId:      "",
			MessageCd:    "",
			HostName:     "",
			Body:         truncate(bodyBytes, 4*1024), // 4KiB 제한
			//Meta:         map[string]interface{}{"remote_addr": r.RemoteAddr},
		}
		buf, _ := json.Marshal(ev)
		// 키: TCID 우선, 없으면 빈키
		key := []byte(header.Parse(r.Header.Get("X-Fw-Header"))["TCID"])
		// kafka 송신
		h.Log.Publish(key, buf)
	}

	var in requestBody
	if r.Method == http.MethodGet {
		// GET 방식일 때는 /gateway/* 전체 경로에서 /gateway prefix를 제거하여 in.URL에 넣어줌
		in.URL = strings.TrimPrefix(r.URL.Path, "/gateway")
		in.Data = nil
	} else {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			returnlog(r, h, merged, []byte("failed to read body"))
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.NewError("failed to read body", err))
			return
		}
		defer r.Body.Close()
		if err := json.Unmarshal(bodyBytes, &in); err != nil {
			returnlog(r, h, merged, []byte("invalid JSON"))
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.NewError("invalid JSON", err))
			return
		}
	}

	var requestData model.RequestData
	// DB 체크용: 쿼리스트링 없는 path만 사용
	urlOnly := in.URL
	if idx := strings.Index(urlOnly, "?"); idx != -1 {
		urlOnly = urlOnly[:idx]
	}
	requestData.RequestURL = urlOnly
	requestData.BizServiceCode = bizCode

	// DB 확인
	var err error
	requestData, err = h.Repo.FindRequestData(r.Context(), requestData)
	if err != nil {
		returnlog(r, h, merged, []byte("Request Api error"))
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("Request Api error", err))

		return
	}

	// Roll check
	existUseApiFlag, err := h.Repo.ExistUseAPIList(r.Context(), requestData)

	if err != nil {
		returnlog(r, h, merged, []byte("Using Api error"))
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("Using Api error", err))
		return
	}

	if !existUseApiFlag {
		returnlog(r, h, merged, []byte("Access not allowed by use API policy"))
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("Access not allowed by use API policy", err))
		return
	}

	// API Group 체크
	existApiGroupFlag, err := h.Repo.ExistAPIGroup(r.Context(), requestData)

	if err != nil {
		returnlog(r, h, merged, []byte("To use API Group error"))
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("To use API Group error", err))
		return
	}

	if !existApiGroupFlag {
		returnlog(r, h, merged, []byte("Api Group URL not allowed"))
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("Api Group URL not allowed", err))
		return
	}

	// API 체크
	existApiFlag, err := h.Repo.ExistAPI(r.Context(), requestData)

	if err != nil {
		returnlog(r, h, merged, []byte("To use API error"))
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("To use API error", err))
		return
	}

	if !existApiFlag {
		returnlog(r, h, merged, []byte("Api URL not allowed"))
		httpx.WriteJSON(w, http.StatusNotFound, httpx.NewError("Api URL not allowed", err))
		return
	}

	// 업스트림 바디 및 URL 준비: 메서드별 처리
	method := r.Method
	var outBody io.Reader
	var hasBody bool
	ctx, cancel := context.WithTimeout(r.Context(), h.Client.Timeout)
	defer cancel()

	// 1) 서비스코드로 백엔드 호스트 조회
	host := ""
	if requestData.RequestHost != "" {
		host = requestData.RequestHost
	} else {
		host = config.AppConfig.Hosts[requestData.ApiGroupCode]
	}
	// host가 빈값 또는 null이면 에러 반환
	if host == "" {
		returnlog(r, h, merged, []byte("Host not found for API data"))
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("Host not found for API data", nil))
		return
	}

	var upstreamURL string

	if method == http.MethodGet {
		// 업스트림 송신 시에는 in.URL 전체(쿼리 포함) 그대로 사용
		upstreamURL = host + in.URL
		outBody = nil
		hasBody = false
	} else {
		upstreamURL = host + in.URL
		if len(in.Data) > 0 && string(in.Data) != "null" {
			outBody = bytes.NewReader(in.Data)
			hasBody = true
		}
	}

	log.Printf("host : %s, URL : %s, apiGroupCode : %s", host, upstreamURL, requestData.ApiGroupCode)

	// 2) 업스트림 요청 생성 (메서드 그대로 사용)
	reqUp, err := http.NewRequestWithContext(ctx, method, upstreamURL, outBody)
	if err != nil {
		returnlog(r, h, merged, []byte("build outbound request failed"))
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("build outbound request failed", err))
		return
	}

	// ✅ FW Header 생성 (Host 기반)
	// 1) 클라이언트가 보낸 X-Fw-Header 파싱
	//inFwRaw := r.Header.Get("X-Fw-Header")
	//inFw := header.Parse(inFwRaw)
	if ua := r.Header.Get("X-Fw-Header"); ua != "" {
		reqUp.Header.Set("X-Fw-Header", ua)
	}

	// 4) 다시 문자열로 직렬화하여 업스트림에 전달
	outFw := header.Serialize(merged)
	r.Header.Set("X-Fw-Header", outFw)
	// 헤더 복사 (필요시 hop-by-hop 필터링 추가 가능)
	copyHeaders(reqUp.Header, r.Header)
	if hasBody && reqUp.Header.Get("Content-Type") == "" {
		reqUp.Header.Set("Content-Type", "application/json")
	}

	// outbound log
	if ok, _ := h.Repo.ExistConfig(r.Context(), configlog.Outbound.Request); ok {

		var bodyBytes []byte
		if r.Method == http.MethodGet {
			bodyBytes = []byte{}
			reqUp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 빈 body로 복원
		} else {
			bodyBytes, _ = io.ReadAll(reqUp.Body)
			defer reqUp.Body.Close()
			reqUp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 복원
		}

		if err != nil {
			log.Printf("failed to read request body: %v", err)
			// 필요하다면 에러 처리
		}
		defer reqUp.Body.Close()

		// body를 다시 사용할 수 있게 복원
		reqUp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		headerJson, _ := json.Marshal(reqUp.Header)
		log.Printf("target request log : body=%s, header=%s", string(bodyBytes), string(headerJson))

		ev := gwLog{
			Ts:           strings.Replace(time.Now().Format("20060102150405.000"), ".", "", 1),
			Tcid:         header.Parse(reqUp.Header.Get("X-Fw-Header"))["TCID"],
			TcidSrno:     header.Parse(reqUp.Header.Get("X-Fw-Header"))["TCIDSRNO"],
			TcidCreMabd:  "00",
			BizSrvcCd:    header.Parse(reqUp.Header.Get("X-Fw-Header"))["BizSrvcCd"],
			BizSrvcIp:    header.Parse(reqUp.Header.Get("X-Fw-Header"))["BizSrvcIp"],
			RasTyp:       "21", // 11 요청, 12 응답, 21 : 송신 22 수신
			NmlYn:        "Y",
			ApiPath:      reqUp.URL.String(),
			CmnSrvcCd:    "",
			CmnBizSrvcCd: "",
			ApiGroupCd:   config.AppConfig.Application.GroupCode,
			ApiCd:        "00001",
			Guid:         "",
			InterId:      "",
			MessageCd:    "",
			HostName:     "",
			Body:         truncate(bodyBytes, 4*1024), // 4KiB 제한
			//Meta:         map[string]interface{}{"remote_addr": r.RemoteAddr},
		}
		buf, _ := json.Marshal(ev)
		// 키: TCID 우선, 없으면 빈키
		key := []byte(header.Parse(reqUp.Header.Get("X-Fw-Header"))["TCID"])
		// kafka 송신
		h.Log.Publish(key, buf)

	}

	resp, err := h.Client.Do(reqUp)
	if err != nil {
		merged["TCIDSRNO"] = header.BumpTCIDSRNO(merged["TCIDSRNO"])
		returnlog(r, h, merged, []byte("upstream request failed"))
		httpx.WriteJSON(w, http.StatusBadGateway, httpx.NewError("upstream request failed", err))
		return
	}
	defer resp.Body.Close()

	// 업스트림 응답 body 읽기 및 로그
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("failed to read upstream response body: %v", err)
		httpx.WriteJSON(w, http.StatusBadGateway, httpx.NewError("upstream response read failed", err))
		return
	}
	log.Printf("upstream response body: %s", string(bodyBytes))

	// 헤더 복사 및 상태코드 설정
	fwHeader := resp.Header.Get("X-Fw-Header")
	bumped := header.BumpTCIDSRNO(fwHeader)
	copyHeaders(w.Header(), resp.Header)
	w.Header().Set("X-Fw-Header", bumped)
	w.WriteHeader(resp.StatusCode)

	// GET 방식이면 bodyBytes 그대로 반환, POST 등은 Content-Type을 json으로 설정
	if r.Method == http.MethodGet {
		_, _ = w.Write(bodyBytes)
	} else {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bodyBytes)
	}

	// outbound log (원하는 위치에 bodyBytes 사용)
	if ok, _ := h.Repo.ExistConfig(r.Context(), configlog.Outbound.Response); ok {
		headerJson, _ := json.Marshal(resp.Header)
		log.Printf("target response log : body=%s, header=%s", string(bodyBytes), string(headerJson))
		ev := gwLog{
			Ts:           strings.Replace(time.Now().Format("20060102150405.000"), ".", "", 1),
			Tcid:         header.Parse(w.Header().Get("X-Fw-Header"))["TCID"],
			TcidSrno:     header.Parse(w.Header().Get("X-Fw-Header"))["TCIDSRNO"],
			TcidCreMabd:  "00",
			BizSrvcCd:    header.Parse(w.Header().Get("X-Fw-Header"))["BizSrvcCd"],
			BizSrvcIp:    header.Parse(w.Header().Get("X-Fw-Header"))["BizSrvcIp"],
			RasTyp:       "12",
			NmlYn:        "Y",
			ApiPath:      r.URL.String(),
			CmnSrvcCd:    "",
			CmnBizSrvcCd: "",
			ApiGroupCd:   config.AppConfig.Application.GroupCode,
			ApiCd:        "00001",
			Guid:         "",
			InterId:      "",
			MessageCd:    "",
			HostName:     "",
			Body:         truncate(bodyBytes, 4*1024),
		}
		buf, _ := json.Marshal(ev)
		key := []byte(header.Parse(w.Header().Get("X-Fw-Header"))["TCID"])
		h.Log.Publish(key, buf)
	}

	copyHeaders(w.Header(), resp.Header)
	w.Header().Set("X-Fw-Header", bumped)
	w.WriteHeader(resp.StatusCode)

	var buf bytes.Buffer
	tee := io.TeeReader(resp.Body, &buf)

	// 먼저 w로 카피
	_, _ = io.Copy(w, tee)

	// outbound log
	if ok, _ := h.Repo.ExistConfig(r.Context(), configlog.Inbound.Response); ok {
		bodyBytes, err := io.ReadAll(bytes.NewReader(buf.Bytes()))
		if err != nil {
			log.Printf("failed to read request body: %v", err)
			// 필요하다면 에러 처리
		}
		defer resp.Body.Close()

		// body를 다시 사용할 수 있게 복원
		resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		headerJson, _ := json.Marshal(resp.Header)
		log.Printf("target request log : body=%s, header=%s", string(bodyBytes), string(headerJson))

		ev := gwLog{
			Ts:           strings.Replace(time.Now().Format("20060102150405.000"), ".", "", 1),
			Tcid:         header.Parse(w.Header().Get("X-Fw-Header"))["TCID"],
			TcidSrno:     header.Parse(w.Header().Get("X-Fw-Header"))["TCIDSRNO"],
			TcidCreMabd:  "00",
			BizSrvcCd:    header.Parse(w.Header().Get("X-Fw-Header"))["BizSrvcCd"],
			BizSrvcIp:    header.Parse(w.Header().Get("X-Fw-Header"))["BizSrvcIp"],
			RasTyp:       "12", // 11 요청, 12 응답, 21 : 송신 22 수신
			NmlYn:        "Y",
			ApiPath:      r.URL.String(),
			CmnSrvcCd:    "",
			CmnBizSrvcCd: "",
			ApiGroupCd:   config.AppConfig.Application.GroupCode,
			ApiCd:        "00001",
			Guid:         "",
			InterId:      "",
			MessageCd:    "",
			HostName:     "",
			Body:         truncate(bodyBytes, 4*1024), // 4KiB 제한
			//Meta:         map[string]interface{}{"remote_addr": r.RemoteAddr},
		}
		buf, _ := json.Marshal(ev)
		// 키: TCID 우선, 없으면 빈키
		key := []byte(header.Parse(w.Header().Get("X-Fw-Header"))["TCID"])
		// kafka 송신
		h.Log.Publish(key, buf)

	}

}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func returnlog(r *http.Request, h *DynamicGateway, merged map[string]string, data []byte) {

	configlog := config.AppConfig.Application.Log

	merged["TCIDSRNO"] = header.BumpTCIDSRNO(merged["TCIDSRNO"])

	// ==== inbound response log ====
	if ok, _ := h.Repo.ExistConfig(r.Context(), configlog.Inbound.Response); ok {
		var bodyBytes []byte
		if r.Method == http.MethodGet {
			bodyBytes = []byte{}
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 빈 body로 복원
		} else {
			bodyBytes, _ = io.ReadAll(r.Body)
			defer r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 복원
		}

		defer r.Body.Close()
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 복원
		ev := gwLog{
			Ts:           strings.Replace(time.Now().Format("20060102150405.000"), ".", "", 1),
			Tcid:         merged["TCID"],
			TcidSrno:     merged["TCIDSRNO"],
			TcidCreMabd:  "00",
			BizSrvcCd:    merged["BizSrvcCd"],
			BizSrvcIp:    merged["BizSrvcIp"],
			RasTyp:       "12", // 11 요청, 12 응답, 21 : 송신 22 수신
			NmlYn:        "N",
			ApiPath:      r.URL.String(),
			CmnSrvcCd:    "",
			CmnBizSrvcCd: "",
			ApiGroupCd:   config.AppConfig.Application.GroupCode,
			ApiCd:        "00001",
			Guid:         "",
			InterId:      "",
			MessageCd:    "",
			HostName:     "",
			Body:         truncate(data, 4*1024), // 4KiB 제한
			//Meta:         map[string]interface{}{"remote_addr": r.RemoteAddr},
		}
		buf, _ := json.Marshal(ev)
		// 키: TCID 우선, 없으면 빈키
		key := []byte(header.Parse(r.Header.Get("X-Fw-Header"))["TCID"])
		// kafka 송신
		h.Log.Publish(key, buf)
	}

}
