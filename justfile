default: test lint

build:
    go build -o bin/hpm ./cmd/hpm

install:
    go install ./cmd/hpm

test:
    go test -race ./...

lint:
    go vet ./...
    golangci-lint run ./...

# herdr link runs no build command, so rebuild after each change.
link: build
    herdr plugin link {{justfile_directory()}}
