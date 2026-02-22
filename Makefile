.PHONY: build build-all install test lint clean

build:
	go build -o errata ./cmd/errata

build-all:
	GOOS=darwin  GOARCH=arm64 go build -o dist/errata-darwin-arm64  ./cmd/errata
	GOOS=darwin  GOARCH=amd64 go build -o dist/errata-darwin-amd64  ./cmd/errata
	GOOS=linux   GOARCH=amd64 go build -o dist/errata-linux-amd64   ./cmd/errata
	GOOS=windows GOARCH=amd64 go build -o dist/errata-windows-amd64.exe ./cmd/errata

install:
	go install ./cmd/errata

test:
	go test ./...

lint:
	golangci-lint run ./...

clean:
	rm -f errata
	rm -rf dist/
