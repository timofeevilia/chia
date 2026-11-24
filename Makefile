TEST_ENV = TEST_DATABASE_URL='postgres://chia:chia@localhost:5432/chia?sslmode=disable' TEST_REDIS_URL='redis://localhost:6379/1'

.PHONY: up down test lint generate loadtest

up: ## start Postgres, Redis and the API on :8080
	docker compose up --build -d

down:
	docker compose down -v

test: ## unit + integration tests (needs `docker compose up -d postgres redis`)
	$(TEST_ENV) go test -race -count=1 ./...

lint:
	golangci-lint run ./...

generate: ## regenerate internal/httpapi/api.gen.go from api/openapi.yaml
	go generate ./...

loadtest: ## needs k6 and a stack started with RATE_LIMIT_PER_MINUTE=0
	k6 run loadtest/ledger.js
