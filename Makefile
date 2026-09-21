.PHONY: dev down migrate build test provision-tenant

dev:
	./scripts/compose-up.sh local

down:
	docker-compose --env-file .env.local -f docker-compose.local.yml down -v

migrate:
	./scripts/migrate.sh up

migrate-up:
	./scripts/migrate.sh up

migrate-down:
	./scripts/migrate.sh down

build:
	go build -o bin/ ./services/...

test:
	go test -v ./...

provision-tenant:
	@echo "Use POST /api/v1/companies endpoint to provision a tenant as SuperAdmin."
