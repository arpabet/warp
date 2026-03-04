.PHONY: test build clean

GOCACHE ?= $(CURDIR)/.gocache

test:
	@mkdir -p "$(GOCACHE)"
	GOCACHE="$(GOCACHE)" go test ./...

build:
	@mkdir -p "$(GOCACHE)"
	GOCACHE="$(GOCACHE)" go build ./...

clean:
	rm -rf "$(GOCACHE)"
