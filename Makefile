BINARY    := tallyawg
LDFLAGS   := -s -w
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build linux dist test vet clean

build:
	go build -o $(BINARY) .

# Static linux/amd64 binary (no cgo) — drop it straight onto a server.
linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-amd64 .

# Release archives into dist/ + SHA256SUMS, named the way `tallyawg update` expects to find them.
dist:
	@mkdir -p dist
	@version=$$(sed -n 's/.*Version  *= *"\([^"]*\)".*/\1/p' version.go); \
	for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; name=$(BINARY)_$${version}_$${os}_$${arch}; \
		echo "  $$name"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/$$name . ; \
		if [ $$os = linux ]; then \
			tar -C dist --owner=0 --group=0 --mode=0755 -czf dist/$$name.tar.gz $$name; \
		else \
			( cd dist && zip -9 -q $$name.zip $$name ) || exit 1; \
		fi; \
		rm -f dist/$$name; \
	done
	@cd dist && sha256sum --text $(BINARY)_* > SHA256SUMS && echo "  -> dist/SHA256SUMS"

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY).exe
	rm -rf dist
