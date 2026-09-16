module backend/worker-alert

go 1.21

require (
	backend/internal v0.0.0
	github.com/redis/go-redis/v9 v9.5.1
)

replace backend/internal => ../../internal
