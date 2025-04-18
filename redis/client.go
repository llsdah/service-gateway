package redis

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// var = 전역변수
var rdb *redis.Client = redis.NewClient(&redis.Options{
	Addr: "localhost:6379",
})

// set key-value 저장, 0 TTL
// redis는 비동기/병렬성 고려 context.Context 로 받는다
func SaveSession(ctx context.Context, key, value string) error {
	return rdb.Set(ctx, key, value, 0).Err()
}

func GetSession(ctx context.Context, key string) (string, error) {
	return rdb.Get(ctx, key).Result()
}
