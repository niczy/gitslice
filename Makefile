.PHONY: install proto build build-slice build-admin build-cli start-servers test clean install_gs

GOPATH := $(shell go env GOPATH)

install:
	go mod download
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0

proto:
	PATH=$(GOPATH)/bin:$(PATH) sh -c 'cd proto/slice && protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative slice_service.proto'
	PATH=$(GOPATH)/bin:$(PATH) sh -c 'cd proto/admin && protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative admin_service.proto'

build: proto
	go build -o slice_service_server ./slice_service/
	go build -o admin_service_server ./admin_service/
	go build -o gs_cli/gs_cli ./gs_cli/

build-slice: proto
	go build -o slice_service_server ./slice_service/

build-admin: proto
	go build -o admin_service_server ./admin_service/

build-cli: proto
	go build -o gs_cli/gs_cli ./gs_cli/

start-servers: build
	./slice_service_server &
	./admin_service_server &
	@echo "Services started. Press Ctrl+C to stop."

test:
	go test ./...

clean:
	rm -f slice_service_server admin_service_server gs_cli/gs_cli

install_gs: build-cli
	cp gs_cli/gs_cli $(GOPATH)/bin/gs
