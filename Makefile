# mise's shims put the pinned go, goreleaser and golangci-lint on PATH without
# needing mise activation in the calling shell.
export PATH := $(HOME)/.local/share/mise/shims:$(PATH)

.PHONY: tools test lint fmt build snapshot tidy-check

tools:
	mise install

test:
	go test -race -cover ./...

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
