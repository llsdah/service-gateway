package httpx

import (
	"encoding/json"
	"net/http"
)

// 응답 공통 스키마(예시)
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// 유틸 함수: 에러 응답 생성 (Gin 제거)
func NewError(msg string, err error) Response {
	var reason string
	if err != nil {
		reason = err.Error()
	}
	return Response{
		Success: false,
		Message: msg,
		Data:    map[string]any{"error": reason},
	}
}
