.PHONY: build clean test

GO = $(shell which go)

GO_FLAGS=

# -----------------------------------------------------------------------------
# Dependencies
# -----------------------------------------------------------------------------
deps: deps/go


.PHONY: deps/go
deps/go:
	${GO} mod tidy
	$(GO) install github.com/vektra/mockery/v3@v3.5.5
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0

# -----------------------------------------------------------------------------
# Tests and linting
# -----------------------------------------------------------------------------
.PHONY: test
test:
	GOFLAGS="-count=1" go test -v -p 1 -parallel 1 ./...

.PHONY: lint
lint:
	golangci-lint run --timeout "5m"

.PHONY: fmt
fmt:
	gofmt -w .

.PHONY: fmtcheck
fmtcheck:
	@unformatted_files=$$(gofmt -l .); \
	if [ -n "$$unformatted_files" ]; then \
		echo "The following files are not properly formatted:"; \
		echo "$$unformatted_files"; \
		echo "Please run 'gofmt -w .' to format them."; \
		exit 1; \
	fi

.PHONY: mocks
mocks:
	@echo "Generating mocks..."
	mockery

