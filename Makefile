# Common development tasks. The end-to-end checks need root.
GO ?= go

.PHONY: build test e2e e2e-exec e2e-compat e2e-all e2e-docker lint clean

build:
	$(GO) build -o ghostwire .

test:
	$(GO) vet ./...
	$(GO) test -race -count=1 ./...

e2e: build
	sudo test/e2e.sh

# ghostwire exec, rootless as the current user and then as root.
e2e-exec: build
	test/e2e-exec.sh
	sudo test/e2e-exec.sh

# Needs fping, and icmplib from test/compat/requirements.txt.
e2e-compat: build
	sudo env "PATH=$$PATH" test/e2e-compat.sh

# e2e, e2e-compat and e2e-exec at the same time, each root run in a network
# namespace of its own.
e2e-all: build
	test/e2e-all.sh

# The container image and the Compose example; needs Docker.
e2e-docker:
	test/e2e-docker.sh

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	$(GO) vet ./...
	shellcheck -S warning test/*.sh scripts/*.sh examples/*/*.sh

clean:
	rm -f ghostwire
