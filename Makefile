.PHONY: build run test test-race vet bench proto

build:
	go build -o bin/server ./cmd/server
	go build -o bin/bench ./cmd/bench

run: build
	./bin/server

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

vet:
	go vet ./...

# Сервер должен работать в другом терминале (make run).
bench: build
	./bin/bench -warmup 100 -samples 1000 -repeats 3 -out bench/results

# Для обычной сборки protoc не нужен: gen/ уже включён в проект.
proto:
	protoc -I proto --go_out=gen --go_opt=paths=source_relative \
	  --go-grpc_out=gen --go-grpc_opt=paths=source_relative catalog/v1/catalog.proto
