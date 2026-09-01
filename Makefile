BINARY_NAME := zen
MODULE := github.com/mgreau/zen
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

.PHONY: build install test clean lint cover

build:
	go build -ldflags "-X $(MODULE)/cmd.Version=$(VERSION) -X $(MODULE)/cmd.Commit=$(COMMIT)" -o $(BINARY_NAME) .

install: build
	cp $(BINARY_NAME) $(HOME)/.local/bin/$(BINARY_NAME)

test:
	go test ./...

# Coverage of the lines this branch adds, gated at 75%. BASE overrides the
# comparison point (defaults to origin/main).
BASE ?= origin/main
cover:
	go test ./... -coverprofile=coverage.out -covermode=count
	python3 scripts/diffcover.py --profile coverage.out --base $(BASE) --min 75

clean:
	rm -f $(BINARY_NAME) coverage.out

lint:
	go vet ./...

# Quick verify: build + help
verify: build
	./$(BINARY_NAME) --help
	./$(BINARY_NAME) search --help
	./$(BINARY_NAME) watch --help
