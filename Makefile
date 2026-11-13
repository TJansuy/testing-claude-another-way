VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath

.PHONY: build dist test vet clean run

# Static, dependency-free binary for SteamOS / any x86_64 Linux.
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o build/castdeck ./cmd/castdeck

# Release archives for x86_64 (Steam Deck, PCs) and arm64 Linux.
dist:
	mkdir -p dist
	for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o build/$$arch/castdeck ./cmd/castdeck && \
		tar -C build/$$arch -czf dist/castdeck-$(VERSION)-linux-$$arch.tar.gz castdeck; \
	done

test:
	go test ./...

vet:
	go vet ./...

run: build
	./build/castdeck

clean:
	rm -rf build dist
