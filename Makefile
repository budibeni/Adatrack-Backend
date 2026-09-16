.PHONY: dev test build

dev:
	docker-compose -f docker-compose.yml -f docker-compose.local.yml up -d

down:
	docker-compose down -v
