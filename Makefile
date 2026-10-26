LOCALBIN ?= $(shell pwd)/bin
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
GOLANGCI_LINT_VERSION ?= v2.8.0
GO_VERSION := $(shell awk '/^go / { print $$2; exit }' go.mod)
# Match the toolchain in go.mod. A newer `go` on PATH cannot load that
# stdlib's export data with the golangci-lint release the operator uses.
export GOTOOLCHAIN := go$(GO_VERSION)

.PHONY: test
test: ## Run unit tests.
	go test ./...

.PHONY: test-e2e
test-e2e: ## Build the server and run scripts/e2e.sh.
	./scripts/e2e.sh

.PHONY: lint
lint: golangci-lint ## Run golangci-lint.
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint and apply fixes.
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint configuration.
	"$(GOLANGCI_LINT)" config verify

.PHONY: golangci-lint
golangci-lint: $(LOCALBIN) ## Install golangci-lint locally if needed.
	@if ! "$(GOLANGCI_LINT)" version 2>/dev/null | grep -q "$(patsubst v%,%,$(GOLANGCI_LINT_VERSION))"; then \
		echo "Downloading github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; \
		GOBIN="$(LOCALBIN)" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	fi

$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"
