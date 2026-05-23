SHELL := /bin/bash
BIN   := .bin/sharedwatch
VERSION ?= dev

.PHONY: all install build fmt vet test ci smoke clean run

all: build

install:
	./install.sh

build:
	mkdir -p $(dir $(BIN))
	go build -ldflags "-X main.Version=$(VERSION)" -o $(BIN) ./cmd/sharedwatch

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./... -count=1

ci: fmt vet test build

smoke: build
	@TMP=$$(mktemp -d) && \
	  export XDG_DATA_HOME=$$TMP/xdg && \
	  $(BIN) init && \
	  $(BIN) test emit smoke.md && \
	  $(BIN) consume && \
	  $(BIN) digest list && \
	  echo "smoke OK ($$TMP)"

run: build
	$(BIN) run

clean:
	rm -f $(BIN)
