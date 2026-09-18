# Nimbus-One — single-binary agent runtime.
# Everything is CGO_ENABLED=0 (no C compiler needed anywhere, Termux included).

BINARY   := nimbus-one
PKG      := ./cmd/nimbus-one
GOFLAGS  ?=
BUILD    := CGO_ENABLED=0 go build -trimpath $(GOFLAGS)
DIST     := dist

.PHONY: all build vet test fmt tidy clean install cross help

all: vet test build

build:
	$(BUILD) -o $(BINARY) $(PKG)

vet:
	go vet ./...

test:
	go test -count=1 ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

clean:
	rm -rf $(BINARY) $(DIST)

install: build
	mkdir -p "$(HOME)/bin"
	cp $(BINARY) "$(HOME)/bin/$(BINARY)"
	@echo "installed to $(HOME)/bin/$(BINARY) — ensure it is on PATH"

# Cross-platform static binaries. No Cgo, no toolchain beyond Go itself.
cross:
	mkdir -p $(DIST)
	GOOS=linux   GOARCH=amd64  $(BUILD) -o $(DIST)/$(BINARY)-linux-amd64   $(PKG)
	GOOS=linux   GOARCH=arm64  $(BUILD) -o $(DIST)/$(BINARY)-linux-arm64   $(PKG)
	GOOS=android GOARCH=arm64  $(BUILD) -o $(DIST)/$(BINARY)-android-arm64 $(PKG)
	GOOS=darwin  GOARCH=arm64  $(BUILD) -o $(DIST)/$(BINARY)-darwin-arm64  $(PKG)
	GOOS=windows GOARCH=amd64  $(BUILD) -o $(DIST)/$(BINARY)-windows-amd64.exe $(PKG)
	@ls -la $(DIST)

help:
	@echo "targets: build vet test fmt tidy clean install cross"
