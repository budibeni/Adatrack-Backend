.PHONY: dev test build

dev:
	./scripts/compose-up.sh local

down:
	docker-compose -f docker-compose.yml -f docker-compose.local.yml down -v
