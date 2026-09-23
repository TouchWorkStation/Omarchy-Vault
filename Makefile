# Omarchy Vault — build targets. Every target is read-only with respect to
# your drives; `install` is delegated to scripts/install.sh, which asks first.

# A build copy without .git (the plugin installer's) carries its version in
# .version.
VERSION ?= $(shell cat .version 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
COMMIT  ?= $(shell cat .version 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X github.com/TouchWorkStation/Omarchy-Vault/internal/version.Version=$(VERSION) \
	-X github.com/TouchWorkStation/Omarchy-Vault/internal/version.Commit=$(COMMIT)

.PHONY: all web build test vet fmt check dev demo clean install uninstall sftpgo

all: web build

web/node_modules: web/package.json
	cd web && npm ci
	@touch web/node_modules

web: web/node_modules
	cd web && npm run build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vaultd ./cmd/vaultd
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vaultctl ./cmd/vaultctl

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w cmd internal web/embed.go

# Everything CI should run.
check: vet test web
	@test -z "$$(gofmt -l cmd internal web/embed.go)" || (echo "gofmt needed:"; gofmt -l cmd internal web/embed.go; exit 1)

# Build the pinned file service into ~/.local/share/omarchy-vault/sftpgo.
sftpgo:
	./scripts/build-sftpgo.sh

dev:
	./scripts/dev.sh

demo:
	./scripts/dev.sh --demo

install: all
	./scripts/install.sh

uninstall:
	./scripts/uninstall.sh

clean:
	rm -rf bin
	find web/dist -mindepth 1 ! -name .keep -delete
