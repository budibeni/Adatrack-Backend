.PHONY: build run test

build:
	go build -o bin/service-websocket main.go

run: build
	./bin/service-websocket

test:
	go test -v ./...
