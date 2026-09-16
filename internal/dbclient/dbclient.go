package dbclient
import (
	"context"
	"fmt"
	"time"
	"backend/internal/config"
	"backend/internal/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)
var Pool *pgxpool.Pool
func Connect(ctx context.Context, cfg *config.Config) error {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		cfg.DBUser, cfg.DBPass, cfg.DBHost, cfg.DBPort, cfg.DBName)
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse config error: %w", err)
	}
	poolConfig.MaxConns = int32(cfg.DBMaxConns)
	poolConfig.MinConns = int32(cfg.DBMinConns)
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 30 * time.Minute

	var lastErr error
	backoff := 1 * time.Second
	for i := 0; i < 10; i++ {
		Pool, err = pgxpool.NewWithConfig(ctx, poolConfig)
		if err == nil {
			if err = Pool.Ping(ctx); err == nil {
				return nil
			}
		}
		lastErr = err
		logger.Log.Warn("DB not ready, retrying...", "attempt", i+1, "error", lastErr)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second { backoff = 30*time.Second }
	}
	return fmt.Errorf("failed to connect after retries: %w", lastErr)
}
