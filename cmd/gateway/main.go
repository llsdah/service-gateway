package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"service-gateway/internal/gateway"
	"service-gateway/internal/httpx"
	"service-gateway/internal/kafkax"
	"service-gateway/internal/middleware"
	"service-gateway/internal/observability"
	"service-gateway/internal/router"
	httpadapter "service-gateway/internal/router/adapter/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	config "service-gateway/internal/configs"
	"service-gateway/internal/handlers"
	"service-gateway/internal/store"
	"service-gateway/internal/store/mariadb"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace/noop"
)

func initTracer(ctx context.Context, cfg config.Config) (func(context.Context) error, error) {

	if !cfg.Tracing.Enabled {
		// Tracing 비활성화 → noop tracer provider
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	// OTLP exporter 구성
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.Tracing.OTLP.Endpoint),
	}

	if cfg.Tracing.OTLP.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	// ✅ gRPC 기반 OTLP exporter 생성
	//exporter, err := otlptracegrpc.New(ctx,
	//	otlptracegrpc.WithInsecure(),                          // TLS 없이 gRPC 통신
	//	otlptracegrpc.WithEndpoint(cfg.Tracing.OTLP.Endpoint), // OTLP gRPC 포트 (기본값 4317)
	//	otlptracegrpc.WithDialOption(grpc.WithBlock()),        // 연결 완료될 때까지 block
	//)

	if err != nil {
		return nil, err
	}

	// ✅ 리소스 (서비스 정보 등)
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.Application.Name),
			attribute.String("env", "dev"),
		),
	)
	if err != nil {
		return nil, err
	}

	// ✅ TracerProvider 구성
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil

}

func main() {
	// 1) 설정 로드
	confPath := envOr("GATEWAY_CONFIG", "configs/gateway.yaml")

	config.LoadConfig(confPath)

	// 모니터링 연결
	tp, err := initTracer(context.Background(), config.AppConfig)
	if err != nil {
		log.Fatalf("fail to initalize tracer : %v", err)
	}
	defer func() {
		if err := tp(context.Background()); err != nil {
			log.Printf("failed to shut down tracer provider: %v", err)
		}
	}()

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

	// Kafka Publisher 생성
	kc := config.AppConfig.Kafka

	if config.AppConfig.Application.Log.Topic == "" {
		log.Fatal("application.log.topic is empty") // 미설정 조기 발견
	}

	pub, err := kafkax.NewPublisher(kafkax.Config{
		Enabled:      kc.Enabled,
		Brokers:      kc.Brokers,
		ClientID:     kc.ClientID,
		Topic:        config.AppConfig.Application.Log.Topic,
		Acks:         kc.Acks,
		Compression:  kc.Compression,
		Timeout:      time.Duration(kc.TimeoutMs) * time.Millisecond,
		BatchBytes:   kc.BatchBytes,
		BatchTimeout: time.Duration(kc.BatchTimeoutMs) * time.Millisecond,
		SASL: struct {
			Enabled   bool
			Mechanism string
			Username  string
			Password  string
		}{kc.SASL.Enabled, kc.SASL.Mechanism, kc.SASL.Username, kc.SASL.Password},
		TLS: struct {
			Enabled            bool
			InsecureSkipVerify bool
		}{kc.TLS.Enabled, kc.TLS.InsecureSkipVerify},
	})
	if err != nil {
		log.Fatalf("kafka init failed: %v", err)
	}
	defer pub.Close()

	mux := http.NewServeMux()

	// health
	mux.Handle("/sid/gateway/hello", observability.Healthz())

	// === 신규: /gateway 등록 === 핸들러 생성에 주입 (타임아웃은 기존 설정 사용) kafka 추가
	dyn := handlers.NewDynamicGateway(repo, 5*time.Second, pub)

	// /gateway 및 하위 경로 모두 처리 (기존 동작 유지)
	mux.HandleFunc("/gateway/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, httpx.NewError("method not allowed", nil))
			return
		}
		dyn.Post(w, r)
	})
	// /gateway 단일 경로도 동일하게 처리
	mux.HandleFunc("/gateway", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, httpx.NewError("method not allowed", nil))
			return
		}
		dyn.Post(w, r)
	})

	// === 일반 라우팅: 경로/메서드 기반 리버스 프록시 ===
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 라우트 매칭 (PathPattern/Prefix + PathVariable)
		rt, params := table.MatchRoute(r)
		if rt == nil {
			httpx.WriteJSON(w, http.StatusNotFound, httpx.NewError("route not found", nil))
			return
		}

		ctx := r.Context()

		// 업스트림 메서드: 라우트에 명시되면 강제, 아니면 클라이언트 메서드 유지
		upMethod := r.Method
		if m := strings.TrimSpace(rt.Backend.Method); m != "" {
			upMethod = strings.ToUpper(m)
		}

		// PathVariable 반영된 업스트림 경로
		upPath := httpadapter.BuildUpstreamPath(rt, r.URL.Path, params)

		// 업스트림 전체 URL (쿼리스트링 유지)
		upstreamURL := rt.Backend.Scheme + "://" + rt.Backend.Host + upPath
		if q := r.URL.RawQuery; q != "" {
			upstreamURL += "?" + q
		}

		// 원본 바디를 그대로 전달하는 업스트림 요청 생성
		reqUp, err := http.NewRequestWithContext(ctx, upMethod, upstreamURL, r.Body)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("build upstream request failed", err))
			return
		}
		// 안전한 헤더 전달 (Hop-by-Hop 제거)
		copyProxyHeaders(reqUp.Header, r.Header)

		// 프록시 처리
		rproxy.Proxy(ctx, w, reqUp, rt.Name, rt.Backend.Scheme, rt.Backend.Host, upPath, upMethod, params)
	})

	// YAML 기반 라우팅 폴백 핸들러
	yamlFallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, params := table.MatchRoute(r)
		if rt == nil {
			httpx.WriteJSON(w, http.StatusNotFound, httpx.NewError("route not found", nil))
			return
		}
		ctx := r.Context()
		upMethod := r.Method
		if m := strings.TrimSpace(rt.Backend.Method); m != "" {
			upMethod = strings.ToUpper(m)
		}
		upPath := httpadapter.BuildUpstreamPath(rt, r.URL.Path, params)
		upstreamURL := rt.Backend.Scheme + "://" + rt.Backend.Host + upPath
		if q := r.URL.RawQuery; q != "" {
			upstreamURL += "?" + q
		}
		reqUp, err := http.NewRequestWithContext(ctx, upMethod, upstreamURL, r.Body)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, httpx.NewError("build upstream request failed", err))
			return
		}
		copyProxyHeaders(reqUp.Header, r.Header)
		rproxy.Proxy(ctx, w, reqUp, rt.Name, rt.Backend.Scheme, rt.Backend.Host, upPath, upMethod, params)
	})

	// FastAPI 스타일 코드 기반 라우팅
	gw := gateway.New(rproxy)

	// 예시: 타입/검증 포함 라우트
	type CreateUser struct {
		Name  string `json:"name" validate:"required,min=2"`
		Email string `json:"email" validate:"required,email"`
		Age   int    `json:"age" validate:"gte=0,lte=120"`
	}

	gw.POST("/api/v1/users", gateway.Upstream{
		Scheme:      "http",
		Host:        "user-service:8080",
		PathRewrite: "/api/v1/users",
	}, gateway.WithJSONValidation(gateway.JSONValidator[CreateUser]()))

	// 경로 변수 타입 포함
	gw.GET("/api/v1/users/{id:int}", gateway.Upstream{
		Scheme:      "http",
		Host:        "user-service:8080",
		PathRewrite: "/api/v1/users/{id}",
	})

	// 비동기 ACK(202) 후 백그라운드로 프록시
	gw.POST("/api/v1/orders", gateway.Upstream{
		Scheme:      "http",
		Host:        "order-service:8080",
		PathRewrite: "/api/v1/orders",
	}, gateway.WithAsyncAck())

	// "/"에 등록: 코드 라우팅 → 미스매치 시 YAML 폴백
	mux.Handle("/", gw.Handler(yamlFallback))

	var handler http.Handler = mux

	// 왜: 본문 과다 방어(엣지 미설정 대비 이중 방어)
	maxBody := int64(10 << 20)
	if v := os.Getenv("GATEWAY_MAX_BODY_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxBody = n
		}
	}
	handler = middleware.BodyLimit(handler, maxBody)

	// 왜: 원 클라이언트 컨텍스트(X-Forwarded-*) 보강(추적 ID와 목적이 다름)
	handler = middleware.ProxyHeaders(handler)

	// 왜: 상관관계 ID는 X-Fw-Header의 TCID로 통일. X-Request-Id는 생성/전파하지 않음.
	bizCode := os.Getenv("FW_BIZ_CODE")
	if bizCode == "" {
		bizCode = "service-gateway"
	}
	handler = middleware.FwHeaderTrace(bizCode, handler)

	// ...필요 시 JWT/RateLimit/CircuitBreaker 추가...
	// handler = middleware.JWTAuth(handler)
	// rl := middleware.NewRateLimiterFromEnv(); handler = rl.Middleware(handler)
	// cb := middleware.NewCircuitBreaker(5, 10*time.Second, 5*time.Second); handler = cb.Middleware(handler)

	handler = observability.Logging(handler)

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

// 파일 하단에 유틸 추가
func copyProxyHeaders(dst, src http.Header) {
	// Hop-by-Hop 헤더 제거
	hop := map[string]struct{}{
		"Connection": {}, "Proxy-Connection": {}, "Keep-Alive": {},
		"Proxy-Authenticate": {}, "Proxy-Authorization": {}, "Te": {},
		"Trailer": {}, "Transfer-Encoding": {}, "Upgrade": {},
	}
	for k, vv := range src {
		if _, bad := hop[http.CanonicalHeaderKey(k)]; bad {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
