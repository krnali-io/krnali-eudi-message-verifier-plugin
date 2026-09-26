.PHONY: test build lint run
test:
	go test -race ./...
build:
	CGO_ENABLED=0 go build -trimpath -o bin/verifylink ./cmd/verifylink
lint:
	go vet ./...
	test -z "$$(gofmt -l cmd internal web)"
run:
	go run ./cmd/verifylink
