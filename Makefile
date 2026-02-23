.PHONY: build install test test-adapters test-linux clean release-snapshot release-check dev plan apply

BIN_DIR := bin
DNSVARD_BIN := $(BIN_DIR)/dnsvard

build:
	@mkdir -p $(BIN_DIR)
	@go build -o $(DNSVARD_BIN) ./cmd/dnsvard
	@printf "Built %s\n" "$(DNSVARD_BIN)"

install: build
	@mkdir -p ~/.local/bin
	@rm -f ~/.local/bin/dnsvard
	cp $(DNSVARD_BIN) ~/.local/bin/

test:
	@go test ./...

test-adapters:
	@scripts/test-dev-adapters.sh

test-linux:
	@scripts/test-linux-bootstrap.sh

clean:
	@rm -f "$(DNSVARD_BIN)"

release-check:
	@goreleaser check

release-snapshot:
	@goreleaser release --snapshot --clean

dev:
	@bun run --cwd ./www dev

tofu-plan:
	@make -C ./infra/tofu --no-print-directory plan name=$(name)

tofu-apply:
	@make -C ./infra/tofu --no-print-directory apply name=$(name)
