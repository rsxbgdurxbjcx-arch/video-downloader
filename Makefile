# video-downloader Makefile
BINARY := video-downloader
PKG := ./cmd/server

.PHONY: all build run test vet fmt lint docker-check clean

all: build

build:
	go build -o $(BINARY) $(PKG)

run:
	go run $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "以下文件需要 gofmt:" && gofmt -l . && exit 1)

docker-check:
	docker compose config > /dev/null

lint: fmt-check vet test

clean:
	rm -f $(BINARY)
