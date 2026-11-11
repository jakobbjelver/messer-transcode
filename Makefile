BINARY  := messer-transcode
PKG     := github.com/jakobbjelver/messer-transcode
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE   ?= messer-transcode:dev
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.BuildDate=$(DATE)

.PHONY: all web web-dev web-test build test race fmt vet lint check docker tidy clean

all: build

# The binary embeds web/app/dist (`//go:embed all:app/dist`), so the frontend is
# built first. Without it `go build` fails: pattern all:app/dist: no matching files.
web:
	cd web/app && { [ -d node_modules ] || npm ci; } && npm run build

web-dev:
	cd web/app && npm run dev

web-test:
	cd web/app && npm test

build: web
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/messer-transcode

test: web
	go test ./...

race: web
	go test -race ./...

fmt:
	gofmt -w .

vet: web
	go vet ./...

lint: web
	golangci-lint run

# fmt-check is what CI runs: it fails instead of rewriting.
fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

check: fmt-check vet test

docker:
	docker build -f deploy/Dockerfile -t $(IMAGE) .

# The image the stack runs. Built here rather than pulled: GHCR publishing needs
# a token with write:packages, and the compose entry says pull_policy: never.
image:
	docker buildx build --platform linux/arm64 --load \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t messer-transcode:$(VERSION) -t messer-transcode:latest \
		-f deploy/Dockerfile .

tidy:
	go mod tidy

clean:
	rm -rf bin web/app/dist web/app/node_modules
