BINARY  := bin/dill
CMD     := ./cmd/dill
TAGS    := containers_image_openpgp
DIST    := dist

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build install clean test vet tidy dist

build:
	@mkdir -p bin
	go build -tags $(TAGS) $(LDFLAGS) -o $(BINARY) $(CMD)

install:
	go install -tags $(TAGS) $(LDFLAGS) $(CMD)

test:
	go test -tags $(TAGS) ./...

vet:
	go vet -tags $(TAGS) ./...

tidy:
	go mod tidy

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
