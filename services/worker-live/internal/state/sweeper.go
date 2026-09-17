package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"backend/internal/logger"
	"backend/internal/models"
	"backend/internal/natsclient"
	"backend/internal/redclient"
	"github.com/redis/go-redis/v9"
)

func StartOfflineSweeper(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				sweepOffline(ctx)
			}
		}
	}()
}

func sweepOffline(ctx context.Context) {
	now := time.Now().Unix()
	cutoff := float64(now - (5 * 60)) // 5 minutes ago

	ops := redclient.Client
	expired, err := ops.ZRangeByScore(ctx, "adatrack_gps:last_updates", &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%f", cutoff),
	}).Result()

	if err != nil {
		logger.Log.Error("Sweeper failed to ZRangeByScore", "err", err)
		return
	}

	if len(expired) > 0 {
		for _, member := range expired {
			// member is "company_code:imei"
			parts := strings.Split(member, ":")
			if len(parts) != 2 {
				continue
			}
			companyCode := parts[0]
			imei := parts[1]
			
			// Publish OFFLINE status
			p := models.TelemetryPayload{
				IMEI:        imei,
				CompanyCode: companyCode,
				Status:      "OFFLINE",
				Timestamp:   time.Now(),
			}
			val, _ := json.Marshal(p)
			
			if companyCode != "" {
				wsTenantSubject := fmt.Sprintf("telemetry.live.%s.%s", companyCode, imei)
				natsclient.NC.Publish(wsTenantSubject, val)
			}
			wsSubject := fmt.Sprintf("telemetry.live.%s", imei)
			natsclient.NC.Publish(wsSubject, val)
			
			// Remove from ZSET so we don't spam
			ops.ZRem(ctx, "adatrack_gps:last_updates", member)
		}
		logger.Log.Info("Swept offline vehicles", "count", len(expired))
	}
}
