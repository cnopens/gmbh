# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get

GIMLET_BINARY=gimlet
CLI_BINARY=gmbh

all: build-gimlet build-cli

install: all install-gimlet install-cli

build-gimlet:
	$(GOBUILD) -o ./bin/$(GIMLET_BINARY) ./cmd/gimlet/*.go

build-cli:
	$(GOBUILD) -o ./bin/$(CLI_BINARY) ./cmd/gmbh/*.go

install-gimlet:
	cp bin/$(GIMLET_BINARY) /usr/local/bin/

install-cli:
	cp bin/$(CLI_BINARY) /usr/local/bin/

# test: 
# 	$(GOTEST) -v ./...

deps:
	$(GOGET) github.com/fatih/color
	$(GOGET) google.golang.org/grpc
	$(GOGET) gopkg.in/yaml.v2
	$(GOGET) github.com/rs/xid
	$(GOGET) github.com/golang/protobuf/proto

clean: 
	rm -f ./bin/$(BINARY_NAME)
run:
	$(GOBUILD) -o ./bin/$(BINARY_NAME)  ./src
	./bin/$(BINARY_NAME)

.PONY:
	install-gimlet install-cli
