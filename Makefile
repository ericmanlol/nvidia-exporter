.PHONY: build test clean

build:
	go build -o bin/nvidia-exporter ./cmd/nvidia-exporter

test:
	go test -race ./...

clean:
	rm -rf bin/
