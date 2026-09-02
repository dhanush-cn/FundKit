package cache

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	RedisClient *redis.Client
	ctx         = context.Background()
)

func InitRedis() {
	addr := os.Getenv("REDIS_URL")
	if addr == "" {
		addr = "localhost:6379"
	}

	RedisClient = redis.NewClient(&redis.Options{
		Addr: addr,
	})

	if _, err := RedisClient.Ping(ctx).Result(); err != nil {
		log.Fatal("Failed to connect to Redis:", err)
	}

	fmt.Println("Successfully connected to Redis")
}

func GetNavPrice(fundID string) (float64, bool, error) {
	value, err := RedisClient.Get(ctx, navKey(fundID)).Result()
	if err == redis.Nil {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}

	var price float64
	_, scanErr := fmt.Sscanf(value, "%f", &price)
	if scanErr != nil {
		return 0, false, scanErr
	}

	return price, true, nil
}

func SetNavPrice(fundID string, price float64, ttl time.Duration) error {
	return RedisClient.Set(ctx, navKey(fundID), fmt.Sprintf("%.4f", price), ttl).Err()
}

func navKey(fundID string) string {
	return fmt.Sprintf("nav:%s", fundID)
}
