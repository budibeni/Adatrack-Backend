// Command e2e is the end-to-end verification harness for the B1 pipeline:
// device frame → ingestion-tcp → NATS → worker-live (Redis) +
// worker-persistence (PostgreSQL). See docs and scripts/e2e-pipeline.sh.
package main

import (
	"context"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// pgConfig holds the PostgreSQL connection parameters.
type pgConfig struct {
	host, port, user, password, db string
}

// options are the harness settings.
type options struct {
	tcpAddr      string
	imei         string
	company      string
	natsURL      string
	redisAddr    string
	redisDB      int
	keyPrefix    string
	pg           pgConfig
	masterSchema string
	timeout      time.Duration
	load         bool
	rate         int
	duration     time.Duration
	devices      int
}

func main() {
	opt := parseFlags()

	if err := selfTestCRC(); err != nil {
		log.Fatalf("harness self-test failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opt.timeout+opt.duration+45*time.Second)
	defer cancel()

	rdb := redis.NewClient(&redis.Options{Addr: opt.redisAddr, DB: opt.redisDB})
	defer func() { _ = rdb.Close() }()

	db, err := openPG(opt.pg, "adatrack_gps_"+lower(opt.company))
	if err != nil {
		log.Fatalf("postgres unavailable (%s): %v", opt.pg.host, err)
	}
	defer func() { _ = db.Close() }()

	nc, err := nats.Connect(opt.natsURL, nats.Name("adatrack-e2e"))
	if err != nil {
		log.Fatalf("nats unavailable (%s): %v", opt.natsURL, err)
	}
	defer nc.Close()

	if opt.load {
		runLoad(ctx, opt, nc, db, rdb)
		return
	}
	runSingle(ctx, opt, nc, db, rdb)
}
