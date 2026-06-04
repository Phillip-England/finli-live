PORT ?= 9876
BIN ?= finli-live

.PHONY: run build install tidy clean

run:
	PORT=$(PORT) go run .

build:
	go build -o bin/$(BIN) .

install:
	go install .

tidy:
	go mod tidy

clean:
	rm -rf bin data
