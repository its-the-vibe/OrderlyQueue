BINARY_NAME=orderlyq

build:
	go build -o $(BINARY_NAME) main.go

test:
	go test -v ./...

clean:
	rm -f $(BINARY_NAME)
	go clean

docker-build:
	docker build -t $(BINARY_NAME):latest .

.PHONY: build test clean docker-build
