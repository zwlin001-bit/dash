.PHONY: all build build-dashd build-agent build-agent-all test lint migrate clean

VERSION ?= dev
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_TIME ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.gitCommit=$(GIT_COMMIT) -X main.buildTime=$(BUILD_TIME)

all: lint test build

build: build-dashd build-agent

build-dashd:
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/dashd ./cmd/dashd

build-agent:
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/dash-agent ./cmd/dash-agent

build-agent-all:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/dash-agent-linux-amd64 ./cmd/dash-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/dash-agent-linux-arm64 ./cmd/dash-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/dash-agent-linux-armv7 ./cmd/dash-agent
	@cd bin && sha256sum dash-agent-linux-amd64 dash-agent-linux-arm64 dash-agent-linux-armv7 > sha256sums.txt
	@echo "Built all agent binaries and generated bin/sha256sums.txt"

test:
	go test -v ./...

lint:
	@./scripts/lint-imports.sh
	@go vet ./...
	@if [ -f scripts/lint-sql.sh ]; then ./scripts/lint-sql.sh; fi

migrate:
	go run ./cmd/dashd migrate

clean:
	rm -rf bin/

.PHONY: build-web
build-web:
	@if command -v npm >/dev/null 2>&1 && [ -f web/package.json ]; then \
		echo "Building web assets..."; \
		(cd web && npm run build) && \
		rm -rf internal/api/dist && \
		mkdir -p internal/api/dist && \
		cp -r web/dist/* internal/api/dist/; \
	fi
