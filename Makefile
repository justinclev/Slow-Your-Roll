.PHONY: build test lint integration tidy cover harness-server harness-load harness-compare

build:
	go build ./...

test:
	go test -race -count=1 -coverprofile=coverage.out -covermode=atomic $$(go list ./... | grep -v '/testharness')
	@go tool cover -func=coverage.out | grep total

cover: test
	go tool cover -html=coverage.out

lint:
	golangci-lint run

integration:
	go test -race -count=1 -tags integration ./...

tidy:
	go mod tidy

harness-server:
	go run ./testharness/server

harness-load:
	go run ./testharness/load

harness-compare:
	go run ./testharness/compare
