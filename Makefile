BINARY  := bin/dill
CMD     := ./cmd/dill
TAGS    := containers_image_openpgp,exclude_graphdriver_devicemapper,exclude_graphdriver_btrfs
DIST    := dist
PKL     ?= pkl

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build build-import build-serve install clean test integration-test integration-test-offline vet tidy fmt validate dist

build:
	@mkdir -p bin
	go build -tags $(TAGS) $(LDFLAGS) -o $(BINARY) $(CMD)

# Optional local status and snapshot-staging service; not part of the default build.
build-serve:
	@mkdir -p bin
	go build -tags $(TAGS) -o bin/dill-serve ./cmd/dill-serve

# Optional offline Compose importer; the core build does not include it.
build-import:
	@mkdir -p bin
	go build -tags $(TAGS) -o bin/dill-import ./cmd/dill-import

install:
	go install -tags $(TAGS) $(LDFLAGS) $(CMD)

test:
	go test -tags $(TAGS) ./...

integration-test:
	@case "$${DILL_TEST_ENGINE:-$${DILL_ENGINE:-}}" in docker|podman) ;; *) echo 'Set DILL_TEST_ENGINE=docker or podman for engine integration tests' >&2; exit 1;; esac
	go test -tags "$(TAGS),integration" ./test/integration -count=1 -v $(if $(DILL_TEST_RUN),-run $(DILL_TEST_RUN),)

# Offline integration-tagged tests may skip real-engine and qualification cases.
integration-test-offline:
	go test -tags "$(TAGS),integration" ./test/integration -count=1 -v $(if $(DILL_TEST_RUN),-run $(DILL_TEST_RUN),)

vet:
	go vet -tags $(TAGS) ./...

tidy:
	go mod tidy

fmt:
	gofmt -w .
	@for f in $$(find . -name "*.pkl" -not -path "./.git/*"); do \
		pkl format -w "$$f"; \
	done

validate:
	PKL=$(PKL) python3 scripts/package-schema.py --version 0.0.0 --output /tmp/dill-out
	@set -e; for f in examples/**/compose.pkl; do \
		[ -f "$$f" ] || continue; \
		echo "eval $$f"; \
		$(PKL) eval "$$f" > /dev/null; \
	done
	DB_USER=test DB_PASSWORD=test $(PKL) eval examples/overrides/my-server.pkl > /dev/null
	PKL=$(PKL) python3 docs/design/check_examples.py
	PKL=$(PKL) python3 -m unittest discover -s scripts -p 'test_*.py'

clean:
	rm -rf bin $(DIST)

dist: dist-darwin-arm64 dist-darwin-amd64 dist-linux-amd64 dist-linux-arm64

dist-darwin-arm64:
	GOOS=darwin  GOARCH=arm64 go build -tags $(TAGS) $(LDFLAGS) -o $(DIST)/$(BINARY)-darwin-arm64  $(CMD)

dist-darwin-amd64:
	GOOS=darwin  GOARCH=amd64 go build -tags $(TAGS) $(LDFLAGS) -o $(DIST)/$(BINARY)-darwin-amd64  $(CMD)

dist-linux-amd64:
	GOOS=linux   GOARCH=amd64 go build -tags $(TAGS) $(LDFLAGS) -o $(DIST)/$(BINARY)-linux-amd64   $(CMD)

dist-linux-arm64:
	GOOS=linux   GOARCH=arm64 go build -tags $(TAGS) $(LDFLAGS) -o $(DIST)/$(BINARY)-linux-arm64   $(CMD)
