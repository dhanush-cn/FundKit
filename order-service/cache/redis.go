package cache

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var RedisClient *redis.Client
var ctx = context.Background()

func InitRedis() {
	addr := os.Getenv("REDIS_URL")
	if addr == "" {
		addr = "localhost:6379"
	}

	RedisClient = redis.NewClient(&redis.Options{
		Addr: addr,
	})

	_, err := RedisClient.Ping(ctx).Result()
	if err != nil {
		log.Fatal("Failed to connect to Redis:", err)
	}

	fmt.Println("Successfully connected to Redis")
}

// CheckIdempotency checks if a key exists in Redis. Returns true if it exists.
func CheckIdempotency(key string) bool {
	exists, _ := RedisClient.Exists(ctx, key).Result()
	return exists > 0
}

// ClaimIdempotency atomically reserves a request key so duplicate submissions
// cannot slip through a race between the existence check and write.
func ClaimIdempotency(key string, expiration time.Duration) bool {
	ok, err := RedisClient.SetNX(ctx, key, "processing", expiration).Result()
	if err != nil {
		return false
	}
	return ok
}

// SetIdempotency sets a key in Redis with an expiration
func SetIdempotency(key string, expiration time.Duration) {
	RedisClient.Set(ctx, key, "processed", expiration)
}

// ReleaseIdempotency removes a reservation when order persistence fails.
func ReleaseIdempotency(key string) {
	RedisClient.Del(ctx, key)
}
