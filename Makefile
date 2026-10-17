# Common development tasks. The end-to-end checks need root.
GO ?= go

.PHONY: build test e2e lint clean

build:
	$(GO) build -o pingmock .

test:
	$(GO) vet ./...
	$(GO) test -race -count=1 ./...

e2e: build
	sudo test/e2e.sh

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	$(GO) vet ./...

clean:
	rm -f pingmock
