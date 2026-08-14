BINARY  := mor
PKG     := ./cmd/mor
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64

.PHONY: build run vet tidy test test-panel clean dist

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

run: build
	./$(BINARY) serve

vet:
	go vet ./...

tidy:
	go mod tidy

test:
	go test ./...
	python3 scripts/check_panel.py

# Browser tests need a running mor; they are not part of `make test` because
# they cannot run without one.
test-panel:
	cd test/browser && MOR_URL=$(MOR_URL) MOR_PASSWORD=$(MOR_PASSWORD) npm test

clean:
	rm -rf $(BINARY) dist

dist: clean
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "  build $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-$$os-$$arch $(PKG); \
	done
	@cp scripts/install.sh dist/install.sh
	@cd dist && shasum -a 256 $(BINARY)-* > checksums.txt && cat checksums.txt
