package model

import "time"

type SessionData struct {
	SessionKey   string        `json:"sessionKey"`
	SessionValue string        `json:"sessionValue"`
	SessionTTL   time.Duration `json:"sessionTTL"` // "3m","1h" 문자열 자동 파싱
}
