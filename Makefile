BIN := hestia-nginx-tui

.PHONY: build test sandbox install

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $(BIN) .

test:
	go vet ./... && go test ./...

# try the TUI without root and without touching a real server
sandbox: build
	@echo 'run:  eval "$$(scripts/sandbox.sh /tmp/hnt-sandbox)" && ./$(BIN)'

install: build
	install -m 0755 $(BIN) /usr/local/bin/$(BIN)
