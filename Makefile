VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
LDFLAGS := -X github.com/Hoodoo/bossman/internal/version.Version=$(VERSION)
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: build test vet check install

build:
	go build -ldflags "$(LDFLAGS)" -o bin/bossman ./cmd/bossman

test:
	go test ./...

vet:
	go vet ./...

check: vet test

install: build
	install -d "$(BINDIR)"
	install -m 0755 bin/bossman "$(BINDIR)/bossman"
	@echo "installed $(BINDIR)/bossman ($(VERSION))"
