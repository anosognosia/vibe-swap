BINARY := vibeswap
VERSION ?= dev
DIST_DIR := dist

.PHONY: test build install-local clean dist

test:
	go test ./...

build:
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd

install-local: build
	mkdir -p "$$HOME/.local/bin"
	cp $(BINARY) "$$HOME/.local/bin/$(BINARY)"
	@echo "Installed $(BINARY) to $$HOME/.local/bin/$(BINARY)"

clean:
	rm -rf $(DIST_DIR) $(BINARY)

dist: clean
	mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(DIST_DIR)/$(BINARY) ./cmd
	tar -C $(DIST_DIR) -czf $(DIST_DIR)/$(BINARY)_Darwin_arm64.tar.gz $(BINARY)
	rm $(DIST_DIR)/$(BINARY)
	GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(DIST_DIR)/$(BINARY) ./cmd
	tar -C $(DIST_DIR) -czf $(DIST_DIR)/$(BINARY)_Darwin_x86_64.tar.gz $(BINARY)
	rm $(DIST_DIR)/$(BINARY)
