.PHONY: all build test migrate docker-up docker-down clean

BIN_DIR := bin
SERVICES := api probe evaluator publisher pagehost

all: test build

build:
	@mkdir -p $(BIN_DIR)
	@for svc in $(SERVICES); do \
		echo "Building $$svc..."; \
		go build -o $(BIN_DIR)/$$svc ./cmd/$$svc; \
	done
	@echo "Build complete. Artifacts in $(BIN_DIR)/"

test:
	go test -v -race ./...

migrate:
	./scripts/migrate.sh

docker-up:
	docker compose up -d

docker-down:
	docker compose down -v

clean:
	rm -rf $(BIN_DIR)
