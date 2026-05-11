# Top-level Makefile — coordinates the three-module workspace.
#
# Layout:
#   qwdemo/       — event schema + MVD source (ingestion layer)
#   qwanalytics/  — analysis pipeline + result schema
#   qw-web/       — browser UX + WASM glue
#
# `make build` produces dist/ for Netlify deploy. Other targets wrap the
# usual Go tools so contributors don't have to remember which module is
# where.

WASM_MAIN  := ./qw-web/cmd/wasm
MVD_MAIN   := ./qwanalytics/cmd/qw-mvd
DIST_DIR   := dist
STATIC_DIR := qw-web/static
LOC_DATA   := qwanalytics/loc/data
GIT_HASH   := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TAG    := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "dev")
BUILD_DATE := $(shell date -u +%Y-%m-%d)
LDFLAGS    := -ldflags "-s -w -X main.GitHash=$(GIT_HASH) -X main.GitTag=$(GIT_TAG) -X main.BuildDate=$(BUILD_DATE)"

.PHONY: build build-mvd build-mvd-linux build-mvd-darwin build-mvd-windows build-mvd-all serve clean test fmt help

# Build the deployable web bundle into dist/.
build:
	@rm -rf $(DIST_DIR)
	@echo "Building WASM module..."
	@mkdir -p $(DIST_DIR)
	GOOS=js GOARCH=wasm go build $(LDFLAGS) -o $(DIST_DIR)/analyzer.wasm $(WASM_MAIN)
	@echo "Copying wasm_exec.js..."
	@cp "$$(go env GOROOT)/misc/wasm/wasm_exec.js" $(DIST_DIR)/ 2>/dev/null || cp $(STATIC_DIR)/wasm_exec.js $(DIST_DIR)/
	@echo "Copying static files..."
	@cp $(STATIC_DIR)/index.html $(DIST_DIR)/
	@cp $(STATIC_DIR)/styles.css $(DIST_DIR)/
	@cp $(STATIC_DIR)/app.js $(DIST_DIR)/
	@cp $(STATIC_DIR)/worker.js $(DIST_DIR)/
	@cp -r $(STATIC_DIR)/maps $(DIST_DIR)/
	@echo "Copying loc corpus from $(LOC_DATA)..."
	@mkdir -p $(DIST_DIR)/locs && cp $(LOC_DATA)/*.loc $(DIST_DIR)/locs/
	@echo "Build complete!"
	@ls -lh $(DIST_DIR)/

# Build the qw-mvd binary for the host platform.
build-mvd:
	@mkdir -p $(DIST_DIR)
	go build $(LDFLAGS) -o $(DIST_DIR)/qw-mvd $(MVD_MAIN)

# Cross-compile qw-mvd for distribution. Output: dist/qw-mvd-<os>-<arch>[.exe].
build-mvd-linux:
	@mkdir -p $(DIST_DIR)
	GOOS=linux  GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/qw-mvd-linux-amd64    $(MVD_MAIN)

build-mvd-darwin:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/qw-mvd-darwin-amd64   $(MVD_MAIN)
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/qw-mvd-darwin-arm64   $(MVD_MAIN)

build-mvd-windows:
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/qw-mvd-windows-amd64.exe $(MVD_MAIN)

build-mvd-all: build-mvd-linux build-mvd-darwin build-mvd-windows
	@ls -lh $(DIST_DIR)/qw-mvd-*

# Serve the built bundle on localhost.
serve: build
	@echo "Serving on http://localhost:8080"
	@cd $(DIST_DIR) && python3 -m http.server 8080

# Run tests across all modules in the workspace.
test:
	go test ./qwdemo/... ./qwanalytics/... ./qw-web/...

# Remove the dist/ tree.
clean:
	rm -rf $(DIST_DIR)

# Format every module.
fmt:
	go fmt ./qwdemo/... ./qwanalytics/... ./qw-web/...

# Help.
help:
	@echo "MVD Analyzer — three-module workspace"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "  build           Build WASM + copy static assets + loc corpus into dist/"
	@echo "  build-mvd       Build the qw-mvd binary for the host platform"
	@echo "  build-mvd-all   Cross-compile qw-mvd for linux/darwin/windows into dist/"
	@echo "  serve           make build, then python3 -m http.server 8080 in dist/"
	@echo "  test            Run tests across every module"
	@echo "  clean           Remove dist/"
	@echo "  fmt             Format code across every module"
	@echo "  help            Show this help"
