.PHONY: fmt vet test test-race build check

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

build:
	go build -trimpath -ldflags="-s -w" -o build/olcrtc-panel ./cmd/olcrtc-panel

check: vet test-race
	node --check internal/web/static/app.js
	node --check internal/assets/files/wb/worker.mjs
	bash -n install.sh uninstall.sh internal/assets/files/wb/*.sh internal/assets/files/update/*.sh
