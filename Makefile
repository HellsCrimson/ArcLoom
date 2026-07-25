# ArcLoom build tasks.
BINARY   := arcloom
WEBDIR   := web
EMBEDDIR := internal/webui/dist

# Build metadata, surfaced by `arcloom version` and the admin Instance panel.
# GoReleaser injects its own values for releases; this covers local builds.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

.PHONY: dev dev-web dev-api build-web build test tidy docker clean

## dev-api: run the Go server (serves API; frontend via Vite in dev)
dev-api:
	go run ./cmd/arcloom

## dev-web: run the Vite dev server (proxies /api to :8080)
dev-web:
	cd $(WEBDIR) && npm run dev

## build-web: build the SPA and stage it for embedding
build-web:
	cd $(WEBDIR) && npm ci && npm run build
	rm -rf $(EMBEDDIR)
	mkdir -p $(EMBEDDIR)
	cp -r $(WEBDIR)/build/. $(EMBEDDIR)/

## build: build the single self-contained binary (frontend embedded)
build: build-web
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/arcloom

## test: run Go tests
test:
	go test ./...

## tidy: tidy go modules
tidy:
	go mod tidy

## docker: build the container image
docker:
	docker build -t arcloom:latest .

## clean: remove build artifacts (restores dev placeholder in dist)
clean:
	rm -f $(BINARY)
	rm -rf $(WEBDIR)/build $(EMBEDDIR)
	mkdir -p $(EMBEDDIR)
	echo "Populated by the frontend build. In dev, the SPA is served by Vite." > $(EMBEDDIR)/placeholder.txt
