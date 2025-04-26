package redis

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	defaultRdb *redis.Client
	clientPool sync.Map // key : "ip:port" -> value : *redis.Client
)

func init() {
	// 기본 localhost Redis 연결
	defaultRdb = redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6379",
	})
}

// set key-value 저장, 0 TTL
// redis는 비동기/병렬성 고려 context.Context 로 받는다

// localhost 입력, 조회
func SaveSession(ctx context.Context, key, value string, ttl time.Duration) error {
	return defaultRdb.Set(ctx, key, value, ttl).Err()
}

func GetSession(ctx context.Context, key string) (string, error) {
	return defaultRdb.Get(ctx, key).Result()
}

// 내부용 : IP:PORT 기준 레디스 클라이언트 가져오지 (없으면 새로 연결)
func getClient(ip string, port string) *redis.Client {
	addr := ip + ":" + port
	if client, ok := clientPool.Load(addr); ok {
		return client.(*redis.Client)
	}

	// 새연결
	newClient := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	clientPool.Store(addr, newClient)

	return newClient

}

// ip port 기반 연결
func SaveSessionWithTarget(ctx context.Context, ip, port, key, value string, ttl time.Duration) error {
	client := getClient(ip, port)
	return client.Set(ctx, key, value, ttl).Err()
}

func GetSessionWithTarget(ctx context.Context, ip, port, key string) (string, error) {
	client := getClient(ip, port)
	return client.Get(ctx, key).Result()
}
