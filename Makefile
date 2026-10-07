# Makefile to build the command lines and tests in Scdo project.
# Pure Go consensus RNG; no GOPATH copy of scdorand.a.
GO ?= go
BUILD_FLAGS ?= -mod=vendor

all: discovery node client light tool vm

discovery:
	$(GO) build $(BUILD_FLAGS) -o ./build/discovery ./cmd/discovery
	@echo "Done discovery building"

node:
	$(GO) build $(BUILD_FLAGS) -o ./build/node ./cmd/node
	@echo "Done node building"

client:
	$(GO) build $(BUILD_FLAGS) -o ./build/client ./cmd/client
	@echo "Done full node client building"

light:
	$(GO) build $(BUILD_FLAGS) -o ./build/light ./cmd/client/light
	@echo "Done light node client building"

tool:
	$(GO) build $(BUILD_FLAGS) -o ./build/tool ./cmd/tool
	@echo "Done tool building"

vm:
	$(GO) build $(BUILD_FLAGS) -o ./build/vm ./cmd/vm
	@echo "Done vm building"

.PHONY: all discovery node client light tool vm
