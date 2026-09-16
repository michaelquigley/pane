.PHONY: build clean examples
.DEFAULT_GOAL := build
GOBIN ?= $(shell go env GOPATH)/bin

clean:
	go clean
	rm -f ${GOBIN}/*
	rm -rf ui/dist ui/node_modules

build:
	npm --prefix ui install
	npm --prefix ui run build
	go install ./...

test:
	npm --prefix ui run test
	go test ./... -count=1
	go vet ./...
