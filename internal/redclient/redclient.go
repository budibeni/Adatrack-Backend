package redclient

import (
	"context"
	"adatrack/internal/config"
	"github.com/redis/go-redis/v9"
)

var Client *redis.Client

func Connect(cfg *config.Config) error {
	Client = redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr,
	})
	return Client.Ping(context.Background()).Err()
}
