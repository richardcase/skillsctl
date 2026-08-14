# mise's shims put the pinned go, goreleaser and golangci-lint on PATH without
# needing mise activation in the calling shell.
export PATH := $(HOME)/.local/share/mise/shims:$(PATH)

.PHONY: tools test test-manual lint fmt build snapshot tidy-check

tools:
	mise install

test:
	go test -race -cover ./...

# Opt-in, and not part of CI: this really runs `claude plugin install` and
# `claude plugin uninstall` against the machine's own Claude Code. It skips
# when claude is absent, and skips rather than touching a plugin that is
# already installed.
test-manual:
	go test -tags manual -run Manual -v ./internal/channel/...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

build:
	go build -o skillsctl ./cmd/skillsctl

snapshot:
	goreleaser release --snapshot --clean

tidy-check:
	go mod tidy
	git diff --exit-code -- go.mod go.sum
