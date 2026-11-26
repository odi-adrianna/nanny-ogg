IMAGE ?= ghcr.io/odi-adrianna/nanny-ogg
TAG ?= dev

.PHONY: run build test vet tidy docker-build

# Reads MEALIE_* and TOOLS_API_KEY from .env (copy .env.example; never commit .env).
run:
	set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/nanny-ogg

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/nanny-ogg ./cmd/nanny-ogg

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

docker-build:
	docker build -t $(IMAGE):$(TAG) .
