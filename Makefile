.PHONY: build test mutants vet

# The commit git reports for this tree, stamped into the binary because Go's own stamp misses a
# linked worktree and, nested in another repository, names that repository's commit (#157).
# Only when this directory is the checkout's root: a tree without its own git inside another
# repository would otherwise read that repository's commit too, so it stays empty and falls back.
ifeq ($(shell git rev-parse --show-toplevel 2>/dev/null),$(realpath $(CURDIR)))
COMMIT := $(shell git rev-parse HEAD)$(shell test -n "$$(git status --porcelain)" && echo +dirty)
endif

build:
	go build -ldflags "-X github.com/alesierraalta/tsp/internal/buildinfo.Commit=$(COMMIT)" -o bin/tsp ./cmd/tsp

test:
	go test ./... -count=1

mutants:
	go run ./tools/mutants

vet:
	test -z "$$(gofmt -l .)" && go vet ./...
