.PHONY: build build-all install test test-race coverage fmt vet lint check clean

build:
	go build -o errata ./cmd/errata

build-all:
	GOOS=darwin  GOARCH=arm64 go build -trimpath -o dist/errata-darwin-arm64  ./cmd/errata
	GOOS=darwin  GOARCH=amd64 go build -trimpath -o dist/errata-darwin-amd64  ./cmd/errata
	GOOS=linux   GOARCH=amd64 go build -trimpath -o dist/errata-linux-amd64   ./cmd/errata
	GOOS=windows GOARCH=amd64 go build -trimpath -o dist/errata-windows-amd64.exe ./cmd/errata

install:
	go install ./cmd/errata

test:
	go test ./...

test-race:
	go test -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

fmt:
	gofmt -w .

vet:
	go vet ./...

lint:
	golangci-lint run ./...

check: fmt vet lint test-race
	@echo "All checks passed."

clean:
	rm -f errata coverage.out coverage.html
	rm -rf dist/
