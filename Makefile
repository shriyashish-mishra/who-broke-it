.PHONY: build test demo demo-team install cross fmt clean
BIN := bin/wbi
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/wbi

test:
	go vet ./...
	go test ./...

demo: build
	bash examples/demo.sh

demo-team: build
	bash examples/demo-two-machines.sh

install:
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/wbi

# Static binaries for every platform, from any machine (no cgo).
cross:
	@for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
	  os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/wbi-$$os-$$arch$$ext ./cmd/wbi && echo "built dist/wbi-$$os-$$arch$$ext"; \
	done

fmt:
	gofmt -w .

clean:
	rm -rf bin dist
