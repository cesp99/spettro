APP=spettro

.PHONY: test bench build build-all

test:
	go test ./...

bench:
	go test -bench=. -run=^$$ ./internal/budget

build:
	go build -o bin/$(APP) ./cmd/spettro

build-all:
	GOOS=linux GOARCH=amd64 go build -o bin/$(APP)-linux-amd64 ./cmd/spettro
	GOOS=darwin GOARCH=arm64 go build -o bin/$(APP)-darwin-arm64 ./cmd/spettro
	GOOS=windows GOARCH=amd64 go build -o bin/$(APP)-windows-amd64.exe ./cmd/spettro
