BINARY  := tgl
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test integration vet fmt lint clean install

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/tgl

install:
	CGO_ENABLED=0 go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/tgl

test:
	go test ./...

# Requires TANGLED_HANDLE, TANGLED_APP_PASSWORD and TGL_TEST_REPO; skips without them.
integration:
	go test -tags integration ./integration/... -v

vet:
	go vet ./...
	go vet -tags integration ./integration/...

fmt:
	gofmt -l -w .

lint: fmt vet test

clean:
	rm -f $(BINARY)
