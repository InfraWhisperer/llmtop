.PHONY: build build-sim run test lint install release clean demo demo-preview sim

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)

build:
	go build -ldflags="$(LDFLAGS)" -o llmtop ./cmd/llmtop

build-sim:
	go build -ldflags="$(LDFLAGS)" -o llmsim ./cmd/llmsim

run:
	go run ./cmd/llmtop

test:
	go test -race -cover ./...

lint:
	golangci-lint run

install:
	go install -ldflags="$(LDFLAGS)" ./cmd/llmtop

release:
	goreleaser release --clean

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -f llmtop llmsim
	rm -rf dist/

# Simulation targets
sim:
	go run ./cmd/llmsim --scenario steady

demo: build
	vhs docs/demo.tape

demo-preview: build
	./llmtop --sim --scenario demo
