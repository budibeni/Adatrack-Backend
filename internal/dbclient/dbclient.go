package dbclient

import (
	"context"
	"fmt"
	"ajb_gps/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

var Pool *pgxpool.Pool

func Connect(cfg *config.Config) error {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		cfg.DBUser, cfg.DBPass, cfg.DBHost, cfg.DBPort, cfg.DBName)
	var err error
	Pool, err = pgxpool.New(context.Background(), dsn)
	if err != nil {
		return err
	}
	return Pool.Ping(context.Background())
}
