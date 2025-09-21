package router

import (
	"net/http"
	"regexp"
	"strings"
)

type Match struct {
	PathPrefix  string
	PathPattern string
	Methods     map[string]struct{}

	pathRegex *regexp.Regexp // 내부용: 컴파일된 정규식
	varNames  []string       // 내부용: {id} 같은 변수 이름들
}

type Backend struct {
	Scheme      string
	Host        string
	PathRewrite string
	Method      string
}

type RouteOptions struct {
	RequireSession    bool
	GenerateIfMissing bool
}

type Route struct {
	Name    string
	Match   Match
	Backend Backend
	Options RouteOptions
}

// 아주 단순한 정적 라우팅 테이블 (v1)
type Table struct {
	routes []Route
}

func NewTable(routes []Route) *Table {
	for i := range routes {
		if routes[i].Match.PathPattern != "" {
			rx, vars := compliePattern(routes[i].Match.PathPattern)
			routes[i].Match.pathRegex = rx
			routes[i].Match.varNames = vars
		}
	}

	return &Table{routes: routes}
}

func (t *Table) Find(req *http.Request) *Route {
	for i := range t.routes {
		rt := &t.routes[i]
		if rt.Match.PathPrefix != "" && !strings.HasPrefix(req.URL.Path, rt.Match.PathPrefix) {
			continue
		}
		if len(rt.Match.Methods) > 0 {
			if _, ok := rt.Match.Methods[req.Method]; !ok {
				continue
			}
		}
		return rt
	}
	return nil
}

func compliePattern(pattern string) (*regexp.Regexp, []string) {
	// "/api/users/{id}/orders/{oid}" -> ^/api/users/(?P<id>[^/]+)/orders/(?P<oid>[^/]+)$
	varNames := []string{}
	re := regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)
	regexStr := re.ReplaceAllStringFunc(pattern,
		func(s string) string {
			name := s[1 : len(s)-1]
			varNames = append(varNames, name)
			return `(?P<` + name + `>[^/]+)`
		},
	)
	regexStr = "^" + regexStr + "$"
	return regexp.MustCompile(regexStr), varNames
}

func (t *Table) MatchRoute(req *http.Request) (*Route, map[string]string) {
	for i := range t.routes {
		rt := &t.routes[i]

		// 1) Method 체크
		if len(rt.Match.Methods) > 0 {
			if _, ok := rt.Match.Methods[req.Method]; !ok {
				continue
			}
		}

		// 2) PathPattern 우선
		if rt.Match.pathRegex != nil {
			if m := rt.Match.pathRegex.FindStringSubmatch(req.URL.Path); m != nil {
				params := map[string]string{}
				for idx, name := range rt.Match.pathRegex.SubexpNames() {
					if idx == 0 || name == "" {
						continue
					}
					params[name] = m[idx]
				}
				return rt, params
			}
			continue
		}

		// 3) PathPrefix 폴백
		if rt.Match.PathPrefix != "" && strings.HasPrefix(req.URL.Path, rt.Match.PathPrefix) {
			return rt, nil
		}
	}
	return nil, nil
}
