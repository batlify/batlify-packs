GO ?= go
BINARY ?= batlify-packs

.PHONY: build fmt test test-race vet

build:
	$(GO) build -buildvcs=false -o $(BINARY) .

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...
