.PHONY: build test lint fmt clean tidy run

BINARY := searchlight
PROD_CID ?= dev-prod-client-id
LDFLAGS := -X github.com/headlinevc/searchlight-cli/internal/config.prodClientID=$(PROD_CID)

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(BINARY) .

test:
	go test -race -cover ./...

lint:
	go vet ./...
	@command -v golangci-lint >/dev/null && golangci-lint run ./... || echo "golangci-lint not installed; skipping"

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

run: build
	./$(BINARY) $(ARGS)

clean:
	rm -f $(BINARY)
	rm -rf dist/
