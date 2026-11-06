BINARY  := messer-transcode
PKG     := github.com/jakobbjelver/messer-transcode
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE   ?= messer-transcode:dev
LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.BuildDate=$(DATE)

.PHONY: all build test race fmt vet lint check docker tidy clean

all: build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/messer-transcode

test:
	go test ./...

race:
	go test -race ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

lint:
	golangci-lint run

# fmt-check is what CI runs: it fails instead of rewriting.
fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

check: fmt-check vet test

docker:
	docker build -f deploy/Dockerfile -t $(IMAGE) .

tidy:
	go mod tidy

clean:
	rm -rf bin
