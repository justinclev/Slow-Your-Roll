.PHONY: build test lint integration tidy cover

build:
	go build ./...

test:
	go test -race -count=1 -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -func=coverage.out | grep total

cover: test
	go tool cover -html=coverage.out

lint:
	golangci-lint run

integration:
	go test -race -count=1 -tags integration ./...

tidy:
	go mod tidy
