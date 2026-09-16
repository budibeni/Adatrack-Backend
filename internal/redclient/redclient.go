package redclient

import (
	"context"
	"time"

	"adatrack/internal/config"
	"adatrack/internal/logger"
	"github.com/redis/go-redis/v9"
)

var Client *redis.Client

func Connect(ctx context.Context, cfg *config.Config) error {
	Client = redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPassword,
		PoolSize:     200,
		MinIdleConns: 20,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	var lastErr error
	backoff := 1 * time.Second

	for i := 0; i < 10; i++ {
		if err := Client.Ping(ctx).Err(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		logger.Log.Warn("Redis not ready, retrying...", "attempt", i+1, "error", lastErr)
		
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}

	return lastErr
}
