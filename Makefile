.PHONY: build test lint clean

build:
	go build -o persishtent cmd/persishtent/main.go

test:
	go test -v ./...

lint:
	@if command -v golangci-lint >/dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found, falling back to go vet"; \
		go vet ./...; \
	fi

clean:
	rm -f persishtent
