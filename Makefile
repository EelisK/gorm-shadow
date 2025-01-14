MODULE = $(shell go list -m)
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

VERB := $(if $(filter $(verb),false),,-v)
CLEAR := $(if $(filter $(clear),false),,-c)

test:
	go test $(VERB) ./...

lint:
	golangci-lint run
