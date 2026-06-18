APP_URL ?= http://localhost:8080

.PHONY: up down logs test fmt tidy load

up:
	docker compose up --build

down:
	docker compose down

logs:
	docker compose logs -f --tail=100

fmt:
	gofmt -w ./cmd ./internal

tidy:
	go mod tidy

test:
	go test ./...

load:
	go run ./cmd/loadgen -url $(APP_URL) -event $(EVENT_ID) -users 30 -orders 120 -qty 1
