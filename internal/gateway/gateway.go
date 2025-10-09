package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"service-gateway/internal/router"
	httpadapter "service-gateway/internal/router/adapter/http"
)

// FastAPI 스타일 경로 타입 지원: {id:int}, {name:str}, {path:path}, {uuid:uuid} 등
func toPattern(path string) string {
	typeMap := map[string]string{
		"int":   `\d+`,
		"float": `\d+(\.\d+)?`,
		"str":   `[^/]+`,
		"path":  `.+`,
		"uuid":  `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}`,
	}
	re := regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)(?::([a-zA-Z_][a-zA-Z0-9_]*))?\}`)
	return re.ReplaceAllStringFunc(path, func(seg string) string {
		m := re.FindStringSubmatch(seg)
		name := m[1]
		typ := "str"
		if len(m) > 2 && m[2] != "" {
			typ = m[2]
		}
		if rx, ok := typeMap[strings.ToLower(typ)]; ok {
			return "{" + name + ":" + rx + "}"
		}
		return "{" + name + ":" + typeMap["str"] + "}"
	})
}

type routeOptions struct {
	validateJSON func([]byte) error // 본문 JSON 검증기(있으면 400 처리)
	asyncAck     bool               // true면 202 반환 후 백그라운드에서 프록시
}

type Gateway struct {
	routes []router.Route
	rproxy *httpadapter.ReverseProxy
	opts   map[string]routeOptions
}

func New(rp *httpadapter.ReverseProxy) *Gateway {
	return &Gateway{
		rproxy: rp,
		opts:   make(map[string]routeOptions),
	}
}

type Upstream = router.Backend

type RouteOption func(*routeOptions)

// JSON 검증 옵션(구조체 태그 기반). validation.JSONValidator[T]()와 함께 사용.
func WithJSONValidation(validator func([]byte) error) RouteOption {
	return func(o *routeOptions) { o.validateJSON = validator }
}

// 202 Accepted 응답 후 백그라운드 프록시 처리
func WithAsyncAck() RouteOption {
	return func(o *routeOptions) { o.asyncAck = true }
}

func (g *Gateway) add(method, path string, up Upstream, opts ...RouteOption) {
	methods := map[string]struct{}{strings.ToUpper(method): {}}
	route := router.Route{
		Name: method + " " + path,
		Match: router.Match{
			PathPattern: toPattern(path),
			Methods:     methods,
		},
		Backend: router.Backend{
			Scheme:      up.Scheme,
			Host:        up.Host,
			Method:      up.Method,      // 비워두면 원본 메서드
			PathRewrite: up.PathRewrite, // 비우면 원본 경로
		},
	}
	g.routes = append(g.routes, route)

	ro := routeOptions{}
	for _, opt := range opts {
		opt(&ro)
	}
	g.opts[route.Name] = ro
}

func (g *Gateway) GET(path string, up Upstream, opts ...RouteOption) {
	g.add(http.MethodGet, path, up, opts...)
}
func (g *Gateway) POST(path string, up Upstream, opts ...RouteOption) {
	g.add(http.MethodPost, path, up, opts...)
}
func (g *Gateway) PUT(path string, up Upstream, opts ...RouteOption) {
	g.add(http.MethodPut, path, up, opts...)
}
func (g *Gateway) PATCH(path string, up Upstream, opts ...RouteOption) {
	g.add(http.MethodPatch, path, up, opts...)
}
func (g *Gateway) DELETE(path string, up Upstream, opts ...RouteOption) {
	g.add(http.MethodDelete, path, up, opts...)
}

func (g *Gateway) Handler(fallback http.Handler) http.Handler {
	table := router.NewTable(g.routes)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, params := table.MatchRoute(r)
		if rt == nil {
			fallback.ServeHTTP(w, r)
			return
		}

		// 업스트림 메서드
		upMethod := r.Method
		if m := strings.TrimSpace(rt.Backend.Method); m != "" {
			upMethod = strings.ToUpper(m)
		}

		// 경로 rewrite/변수 치환
		upPath := httpadapter.BuildUpstreamPath(rt, r.URL.Path, params)

		// 검증 필요 시 본문을 미리 읽기
		var bodyReader *bytes.Reader
		if opt, ok := g.opts[rt.Name]; ok && opt.validateJSON != nil {
			raw, err := ioReadAllAndClose(r.Body)
			if err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			if len(raw) > 0 {
				if err := opt.validateJSON(raw); err != nil {
					http.Error(w, "validation failed: "+err.Error(), http.StatusBadRequest)
					return
				}
			}
			bodyReader = bytes.NewReader(raw)
		}

		// 업스트림 URL
		upstreamURL := rt.Backend.Scheme + "://" + rt.Backend.Host + upPath
		if q := r.URL.RawQuery; q != "" {
			upstreamURL += "?" + q
		}

		// 요청 생성
		var reqBody io.Reader
		if bodyReader != nil {
			reqBody = bodyReader
		} else {
			reqBody = r.Body
		}
		reqUp, err := http.NewRequestWithContext(r.Context(), upMethod, upstreamURL, reqBody)
		if err != nil {
			http.Error(w, "build upstream request failed", http.StatusInternalServerError)
			return
		}
		copyProxyHeaders(reqUp.Header, r.Header)

		// 비동기 ACK 모드
		if opt, ok := g.opts[rt.Name]; ok && opt.asyncAck {
			w.WriteHeader(http.StatusAccepted)
			go func() {
				// bodyReader를 사용한 경우 재사용 불가하니 새 reader로 재구성
				var bgBody io.Reader
				if bodyReader != nil {
					buf := new(bytes.Buffer)
					_ = json.NewEncoder(buf).Encode(struct{}{}) // no-op to allocate; keeping simple
					// 원문을 다시 만들기 위해 바이트 복사
					b := make([]byte, bodyReader.Len())
					_, _ = bodyReader.ReadAt(b, 0)
					bgBody = bytes.NewReader(b)
					reqUp.Body = ioNopCloser(bgBody)
				}
				g.rproxy.Proxy(contextWithDetachedCancel(r.Context()), ioDiscardResponseWriter{}, reqUp, rt.Name, rt.Backend.Scheme, rt.Backend.Host, upPath, upMethod, params)
			}()
			return
		}

		// 동기 프록시
		g.rproxy.Proxy(r.Context(), w, reqUp, rt.Name, rt.Backend.Scheme, rt.Backend.Host, upPath, upMethod, params)
	})
}

// 유틸

func copyProxyHeaders(dst, src http.Header) {
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

func ioReadAllAndClose(rc io.ReadCloser) ([]byte, error) {
	if rc == nil {
		return nil, nil
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// 202 비동기 처리용: 응답은 버리되 연결을 점유하지 않도록 더미 writer
type ioDiscardResponseWriter struct{}

func (ioDiscardResponseWriter) Header() http.Header        { return make(http.Header) }
func (ioDiscardResponseWriter) Write([]byte) (int, error)  { return 0, nil }
func (ioDiscardResponseWriter) WriteHeader(statusCode int) {}

func ioNopCloser(r io.Reader) io.ReadCloser {
	return io.NopCloser(r)
}

func contextWithDetachedCancel(ctx context.Context) context.Context {
	c, _ := context.WithCancel(ctx)
	return c
}
