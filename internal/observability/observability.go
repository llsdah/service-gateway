package observability

import (
	"log"
	"net/http"
	"time"
)

type loggingRW struct {
	http.ResponseWriter
	status int
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingRW{
			ResponseWriter: w,
			status:         200,
		}
		next.ServeHTTP(lrw, r)
		log.Printf("%s %s %d %v", r.Method, r.URL.Path, lrw.status, time.Since(start))
	})
}

func (lrw *loggingRW) WriteHeader(code int) {
	lrw.status = code
	lrw.ResponseWriter.WriteHeader(code)
}

// 기동확인
func Healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}
