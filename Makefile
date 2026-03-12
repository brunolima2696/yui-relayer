DOCKER := $(shell which docker)

protoVer=0.14.0
protoImageName=ghcr.io/cosmos/proto-builder:$(protoVer)
protoImage=$(DOCKER) run --user 0 --rm -v $(CURDIR):/workspace --workdir /workspace $(protoImageName)

.PHONY: build
build:
	go build -o ./build/yrly .

TESTMOCKS = core/mock_chain_test.go
.PHONY: test
test: $(TESTMOCKS)
	go test -v ./...

$(TESTMOCKS):
	go generate ./...

pre-commit:
	go mod tidy
	go fmt ./...

proto-gen:
	@echo "Generating Protobuf files"
	@$(protoImage) sh ./scripts/protocgen.sh

proto-update-deps:
	@echo "Updating Protobuf dependencies"
	$(DOCKER) run --user 0 --rm -v $(CURDIR)/proto:/workspace --workdir /workspace $(protoImageName) buf mod update

.PHONY: proto-gen proto-update-deps pre-commit
