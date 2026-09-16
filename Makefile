.PHONY: dev down provision

dev:
	./scripts/compose-up.sh local

down:
	docker-compose --env-file .env.local -f docker-compose.local.yml down -v

provision:
	./scripts/provision-tenant.sh $(company)
