.PHONY: dev down migrate

dev:
	./scripts/compose-up.sh local

down:
	docker-compose --env-file .env.local -f docker-compose.local.yml down -v

migrate:
	./scripts/migrate.sh
