.PHONY: test run lint tidy

test:
	go test -v -race ./integration/...

run:
	go run cmd/playground/main.go

tidy:
	go mod tidy
	go fmt ./...