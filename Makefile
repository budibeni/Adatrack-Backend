.PHONY: dev test build

dev:
	./scripts/compose-up.sh local

down:
	docker-compose --env-file .env.local -f docker-compose.local.yml down -v
