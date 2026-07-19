# Common Lazylab development helpers.

set dotenv-load
set shell := ["bash", "-uc"]

# Show available recipes
default:
	@just --list

# Run against a GitLab host with a token
[group('run')]
run HOST="https://gitlab.com" TOKEN="$GITLAB_TOKEN":
	go run ./cmd/lazylab --host {{HOST}} --token {{TOKEN}}

# Run tests with the race detector
[group('dev')]
test:
	go test -race ./...

# Build darwin and linux binaries into build/
[group('build')]
build:
	mkdir -p build
	GOOS=darwin GOARCH=$(go env GOARCH) go build -o build/lazylab-darwin-$(go env GOARCH) ./cmd/lazylab
	GOOS=linux GOARCH=amd64 go build -o build/lazylab-linux-amd64 ./cmd/lazylab

# Build a binary and record the VHS demo
[group('run')]
demo:
	go build -o lazylab ./cmd/lazylab
	vhs demo.tape

# Build then copy the darwin binary into ~/self-made-bin
[group('build')]
install: build
	cp build/lazylab-darwin-$(go env GOARCH) ~/self-made-bin/lazylab
	codesign -s - ~/self-made-bin/lazylab

# Format with gofumpt
[group('dev')]
fmt:
	~/go/bin/gofumpt -w .

# Vet, staticcheck, and gofumpt diff check
[group('dev')]
lint:
	go vet ./...
	~/go/bin/staticcheck ./...
	~/go/bin/gofumpt -l -d . && test -z "$(~/go/bin/gofumpt -l .)"

# Report unreachable code (informational, test-only functions are expected)
[group('dev')]
deadcode:
	~/go/bin/deadcode ./...

# Remove build artifacts
[group('build')]
clean:
	rm -rf build lazylab
