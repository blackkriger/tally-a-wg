BINARY    := tallyawg
LDFLAGS   := -s -w
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build linux dist vet clean

build:
	go build -o $(BINARY) .

# Static linux/amd64 binary (no cgo) — drop it straight onto a server.
linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-amd64 .

# Cross-build every platform into dist/ + a SHA256SUMS file.
dist:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "  $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_$${os}_$${arch}$$ext . ; \
	done
	@cd dist && sha256sum $(BINARY)_* > SHA256SUMS && echo "  -> dist/SHA256SUMS"

vet:
	go vet ./...

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY).exe
	rm -rf dist
