package redisrate

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func ExampleNewLimiter() {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	_ = rdb.FlushDB(ctx).Err()

	limiter := NewLimiter(rdb)
	res, err := limiter.Allow(ctx, "project:123", PerSecond(10.0))
	if err != nil {
		panic(err)
	}
	// Use %.0f to ensure we get whole numbers without decimal places
	fmt.Printf("allowed %.0f remaining %.0f\n", res.Allowed, res.Remaining)
	// Output: allowed 1 remaining 9
}
