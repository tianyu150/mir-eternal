GO ?= go

.PHONY: all build test test-race vet fmt-check generate generate-check clean

all: test build

build:
	$(GO) build -o bin/accountserver ./cmd/accountserver
	$(GO) build -o bin/gameserver ./cmd/gameserver

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$($(GO) fmt ./...)" || (echo "Go files were not formatted" && exit 1)

generate:
	$(GO) generate ./internal/gameprotocol

generate-check:
	@cp internal/gameprotocol/catalog_gen.go /tmp/mir-catalog.$$$$; \
	$(GO) generate ./internal/gameprotocol; \
	cmp /tmp/mir-catalog.$$$$ internal/gameprotocol/catalog_gen.go; \
	rm -f /tmp/mir-catalog.$$$$

clean:
	rm -rf bin
