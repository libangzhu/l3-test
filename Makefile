

BUILD_FLAGS := -ldflags '-X "$(SRC_EBCLI)/buildflags.RPCAddr=http://52.74.204.233:8545"'
SRC_CLI := github.com/zhengjunhe/l3-test/cmd
CLI := build/l3test-cli

proj := "build"
.PHONY: default  build build-linux build-mac

default: build

build:
	@go build $(BUILD_FLAGS) -v -o $(CLI) $(SRC_CLI)

build-linux:
	@GOOS=linux GOARCH=amd64	go build $(BUILD_FLAGS) -v -o $(CLI) $(SRC_CLI)
build-mac:
	@GOOS=darwin GOARCH=amd64	go build $(BUILD_FLAGS) -v -o $(CLI) $(SRC_CLI)