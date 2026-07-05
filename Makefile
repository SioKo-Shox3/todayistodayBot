BINARY_NAME := todayistodaybot
CMD_PATH := ./cmd/bot

.PHONY: build build-linux-amd64 build-linux-arm64 docker-build test vet clean

build:
	go build -o bin/$(BINARY_NAME) $(CMD_PATH)

build-linux-amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/$(BINARY_NAME)-linux-amd64 $(CMD_PATH)

build-linux-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/$(BINARY_NAME)-linux-arm64 $(CMD_PATH)

docker-build:
	docker build -t $(BINARY_NAME):latest -f deploy/Dockerfile .

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin/
