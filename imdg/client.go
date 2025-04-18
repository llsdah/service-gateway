package imdg

import (
	"sync"
)

var store = make(map[string]string)

// 멀티 쓰레드 환경 안전하게 읽고/쓰기
var mu sync.RWMutex

func Put(key, value string) {
	// 쓸때 Lock
	mu.Lock()
	// 함수 끝나기 직전 실행될 코드 (락 해제), 다쓰고 unlock
	defer mu.Unlock()
	store[key] = value
}

func Get(key string) (string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	val, ok := store[key]
	return val, ok
}
