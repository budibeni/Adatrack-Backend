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

// Connect initializes the PostgreSQL connection pool with exponential backoff for enterprise reliability.
func Connect(ctx context.Context, cfg *config.Config) error {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		cfg.DBUser, cfg.DBPass, cfg.DBHost, cfg.DBPort, cfg.DBName)

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("failed to parse db config: %w", err)
	}

	poolConfig.MaxConns = int32(cfg.DBMaxConns)
	poolConfig.MinConns = int32(cfg.DBMinConns)
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 30 * time.Minute

	var lastErr error
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	// Retry loop for resilience during startup
	for i := 0; i < 10; i++ {
		Pool, err = pgxpool.NewWithConfig(ctx, poolConfig)
		if err == nil {
			err = Pool.Ping(ctx)
			if err == nil {
				return nil
			}
		}
		
		lastErr = err
		logger.Log.Warn("Database not ready, retrying...", "attempt", i+1, "backoff", backoff, "error", err)
		
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	return fmt.Errorf("failed to connect to database after retries: %w", lastErr)
}
