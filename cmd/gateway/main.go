package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"service-gateway/internal/header"
	"service-gateway/internal/httpx"
	"service-gateway/internal/observability"
	"service-gateway/internal/router"
	httpadapter "service-gateway/internal/router/adapter/http"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	config "service-gateway/internal/configs"
	"service-gateway/internal/handlers"
	"service-gateway/internal/store"
	"service-gateway/internal/store/mariadb"
)

func main() {
	// 1) 설정 로드
	confPath := envOr("GATEWAY_CONFIG", "configs/gateway.yaml")

	config.LoadConfig(confPath)

	// 2) 라우팅 테이블 구성
	var routes []router.Route
	for _, r := range config.AppConfig.Routes {
		methods := make(map[string]struct{})
		for _, m := range r.Match.Methods {
			methods[strings.ToUpper(m)] = struct{}{}
		}
		routes = append(routes, router.Route{
			Name: r.Name,
			Match: router.Match{
				PathPrefix:  r.Match.PathPrefix,
				PathPattern: r.Match.PathPattern, // ✅ 새로 추가된 필드 주입
				Methods:     methods,
			},
			Backend: router.Backend{
				Scheme:      r.Backend.Scheme,
				Host:        r.Backend.Host,
				Method:      r.Backend.Method,
				PathRewrite: r.Backend.PathRewrite,
			},
		})
	}
	// 기존
	table := router.NewTable(routes)

	// 2.5) DB 리포지토리 생성 (환경변수 기반)
	repo, err := buildRepoFromConfig()
	must(err)
	defer repo.Close()

	// 어댑터 / 핸들러 조합
	client := httpadapter.NewClient(
		ms(config.AppConfig.Server.ReadTOms),
		ms(config.AppConfig.Server.WriteTOms),
		ms(config.AppConfig.Server.IdleTOms),
	)

	rproxy := &httpadapter.ReverseProxy{Client: client}

	mux := http.NewServeMux()

	// health
	mux.Handle("/sid/gateway/hello", observability.Healthz())

	// === 신규: /gateway 등록 ===
	dyn := handlers.NewDynamicGateway(repo, ms(config.AppConfig.Server.ReadTOms))
	mux.HandleFunc("/gateway", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, httpx.NewError("method not allowed", err))

			return
		}
		dyn.Post(w, r)
	})

	// 루트: 라이팅 -> 프록시
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		// rt := table.Find(r); rt != nil 정확
		// ✅ PathPattern/Prefix 기반 매칭 + PathVariable 추출
		rt, params := table.MatchRoute(r)
		if rt == nil {
			httpx.WriteJSON(w, http.StatusNotFound, httpx.NewError("not found url ", err))

			return
		}

		ctx := r.Context()

		// 업스트림 메서드 결정: 기본은 클라이언트 메서드, 백엔드가 명시하면 강제
		upMethod := r.Method
		if m := strings.TrimSpace(rt.Backend.Method); m != "" {
			upMethod = strings.ToUpper(m)
		}

		// ✅ PathVariable 치환된 업스트림 경로
		upPath := httpadapter.BuildUpstreamPath(rt, r.URL.Path, params)

		// ✅ FW Header 생성 (Host 기반)
		// 1) 클라이언트가 보낸 X-Fw-Header 파싱
		inFwRaw := r.Header.Get("X-Fw-Header")
		inFw := header.Parse(inFwRaw)

		/**
		// 2) 여기서 체크: idempotency-key / fw_AUTHORIZATION 은 오직 X-Fw-Header에서만
		idemKey := inFw["IdempotencyKey"]
		fwAuth := inFw["fw_AUTHORIZATION"]

		// (예시) idempotency-key 있으면 중복거래 체크
		if idemKey != "" {
			exists, err := repo.ExistsIdempotency(ctx, idemKey) // 구현체는 저장소에 맞게
			if err != nil {
				http.Error(w, "idempotency check error", http.StatusInternalServerError)
				return
			}
			if exists {
				http.Error(w, "duplicate request", http.StatusConflict)
				return
			}
			// 최초 요청이면 마킹 (TTL 권장)
			_ = repo.MarkIdempotency(ctx, idemKey, time.Minute*10)
		}

		// (예시) fw_AUTHORIZATION 있으면 인증 모듈 연동
		if fwAuth != "" {
			ok, err := enc.Verify(fwAuth) // ENC 모듈과 연동하는 함수라고 가정
			if err != nil || !ok {
				http.Error(w, "authorization failed", http.StatusUnauthorized)
				return
			}
		}
		*/

		// 3) 서버 강제 필드 덮어쓰기 (TCID 등) + BizSrvcCd는 설정값 사용
		bizCode := "SAMPLE" // or config.AppConfig.BizServiceCode
		merged := header.ApplyServerSideFields(inFw, bizCode, r.Host)

		// 4) 다시 문자열로 직렬화하여 업스트림에 전달
		outFw := header.Serialize(merged)
		r.Header.Set("X-Fw-Header", outFw)

		// 2) Merge/construct JSON body with jsessionid
		var in map[string]any
		if r.Body != nil {
			bodyBytes, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if len(bodyBytes) > 0 {
				_ = json.Unmarshal(bodyBytes, &in)
			}
		}
		if in == nil {
			in = map[string]any{}
		}

		// request transform based on route options: jsessionid handling + body injection
		// 1) Resolve jsessionid from header

		// 0) 특별 규칙: 라우트 이름이 "save-user"일 때 X-Fw-Session-Id 검사 및 body.key 주입
		if rt.Name == "save-user" {
			for k, v := range r.Header {
				log.Printf("Header: %s=%s", k, v)
			}

			sessionId := r.Header.Get("X-Fw-Session-Id")

			if sessionId == "" {
				sessionId = uuid.NewString()
				log.Println("세션 아이디 생성")
			}

			in["key"] = sessionId
		}
		payload, _ := json.Marshal(in)

		// 3) Replace request to upstream as POST with rewritten path
		// 업스트림 POST 요청으로 교체
		upstreamURL := rt.Backend.Scheme + "://" + rt.Backend.Host + rt.Backend.PathRewrite
		reqUp, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, io.NopCloser(strings.NewReader(string(payload))))
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("not found url ", err))

			return
		}
		reqUp.Header.Set("Content-Type", "application/json")
		// forward a few headers if needed
		// 필요 시 일부 헤더 전달 (User-Agent 등). 단, X-Fw-Session-Id는 아예 제거

		if ua := r.Header.Get("User-Agent"); ua != "" {
			reqUp.Header.Set("User-Agent", ua)
		}

		if ua := r.Header.Get("X-Fw-Session-Id"); ua != "" {
			reqUp.Header.Set("X-Fw-Session-Id", ua)
		}

		if ua := r.Header.Get("X-Fw-Header"); ua != "" {
			reqUp.Header.Set("X-Fw-Header", ua)
		}

		// hand off to proxy: use reqUp in place of original r
		r = reqUp

		rproxy.Proxy(ctx, w, r, rt.Name, rt.Backend.Scheme, rt.Backend.Host, upPath, upMethod, params)

	})

	handler := observability.Logging(mux)

	// 4) 서버 부팅 + Graceful shutdown
	srv := &http.Server{
		Addr:         config.AppConfig.Server.Addr,
		Handler:      handler,
		ReadTimeout:  ms(config.AppConfig.Server.ReadTOms),
		WriteTimeout: ms(config.AppConfig.Server.WriteTOms),
		IdleTimeout:  ms(config.AppConfig.Server.IdleTOms),
	}

	go func() {
		log.Printf("gateway listening on %s", config.AppConfig.Server.Addr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[Sevice-Gateway] server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("gateway stopped")

}

func ms(v int) time.Duration { return time.Duration(v) * time.Millisecond }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// 기존 buildRepoFromEnv 대신, config를 1순위로 사용하고, 없으면 ENV로 폴백
func buildRepoFromConfig() (store.Repository, error) {

	// 1) config.AppConfig.yaml 우선
	enabled := config.AppConfig.DB.Enabled

	if !enabled {
		log.Println("DB disabled, using mock repository")
		//return &MockRepository{}, nil
	}

	driver := config.AppConfig.DB.Driver
	host := config.AppConfig.DB.Host
	port := config.AppConfig.DB.Port
	user := config.AppConfig.DB.User
	password := config.AppConfig.DB.Password
	name := config.AppConfig.DB.Name

	switch driver {
	case "mysql":
		return mariadb.New(mariadb.Config{
			Enabled: enabled, User: user, Password: password, Host: host, Port: port, DBName: name,
		})
	default:
		return nil, fmt.Errorf("unsupported DB_DRIVER: %s", driver)
	}
}
