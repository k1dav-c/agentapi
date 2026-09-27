CHAT_SOURCES_STAMP = chat/.sources.stamp
CHAT_SOURCES = $(shell find chat \( -path chat/node_modules -o -path chat/out -o -path chat/.next \) -prune -o -not -path chat/.sources.stamp -type f -print)
BINPATH ?= out/agentapi
# Version stamped into the binary. Release CI passes the release tag; local
# builds derive it from the latest vX.Y.Z tag (e.g. 0.14.0-3-gabc1234-dirty).
VERSION ?= $(shell git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS = $(if $(VERSION),-X github.com/coder/agentapi/internal/version.Version=$(VERSION))
# This must be kept in sync with the magicBasePath in lib/httpapi/embed.go.
BASE_PATH ?= /magic-base-path-placeholder
FIND_EXCLUSIONS= \
	-not \( \( -path '*/.git/*' -o -path './out/*' -o -path '*/node_modules/*' -o -path '*/.terraform/*' \) -prune \)
SHELL_SRC_FILES := $(shell find . $(FIND_EXCLUSIONS) -type f -name '*.sh')

$(CHAT_SOURCES_STAMP): $(CHAT_SOURCES)
	@echo "Chat sources changed. Running build steps..."
	cd chat && NEXT_PUBLIC_BASE_PATH="${BASE_PATH}" bun run build
	rm -rf lib/httpapi/chat && mkdir -p lib/httpapi/chat && touch lib/httpapi/chat/marker
	cp -r chat/out/. lib/httpapi/chat/
	touch $@

.PHONY: embed
embed: $(CHAT_SOURCES_STAMP)
	@echo "Chat build is up to date."

.PHONY: build
build: embed
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ${BINPATH} main.go

.PHONY: fmt
fmt: fmt/go

.PHONY: fmt/go
fmt/go:
	go tool mvdan.cc/gofumpt -w -l .

.PHONY: gen
gen:
	go generate ./...

lint: lint/shellcheck lint/go lint/ts lint/actions/actionlint
.PHONY: lint

lint/go:
	go tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint run
	go tool github.com/coder/paralleltestctx/cmd/paralleltestctx ./...
.PHONY: lint/go

lint/shellcheck: $(SHELL_SRC_FILES)
	echo "--- shellcheck"
	shellcheck --external-sources $(SHELL_SRC_FILES)
.PHONY: lint/shellcheck

lint/ts:
	cd ./chat && bun lint
.PHONY: lint/ts

lint/actions/actionlint:
	go tool github.com/rhysd/actionlint/cmd/actionlint --config-file actionlint.yaml
.PHONY: lint/actions/actionlint
