BINARY_NAME := zen
MODULE := github.com/mgreau/zen
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

.PHONY: build install test clean lint

build:
	go build -ldflags "-X $(MODULE)/cmd.Version=$(VERSION) -X $(MODULE)/cmd.Commit=$(COMMIT)" -o $(BINARY_NAME) .

install: build
	cp $(BINARY_NAME) $(HOME)/.local/bin/$(BINARY_NAME)

test:
	go test ./...

clean:
	rm -f $(BINARY_NAME)

lint:
	go vet ./...

# Quick verify: build + help
verify: build
	./$(BINARY_NAME) --help
	./$(BINARY_NAME) search --help
	./$(BINARY_NAME) watch --help
