# Common Lazylab development helpers.

set dotenv-load
set shell := ["bash", "-uc"]

# Show available recipes
default:
	@just --list

# The token is deliberately not a recipe argument, because a command line is
# visible to every local user.

# Run the Rust version against a GitLab host, reading GITLAB_TOKEN from the environment or .env
[group('run')]
run HOST="https://gitlab.com":
	cargo run --locked -- --host {{HOST}}

# Run the Go tests with the race detector, then the Rust tests
[group('dev')]
test:
	go test -race ./...
	cargo test --locked

# Build an optimized binary into target/release/
[group('build')]
build:
	cargo build --release --locked

# Render the release archive and Homebrew formula for this machine into target/distrib/, and publish none of it
[group('build')]
release-dry-run:
	dist plan
	dist build

# Build a binary and record the VHS demo
[group('run')]
demo:
	go build -o lazylab ./cmd/lazylab
	vhs demo.tape

# Build then copy the binary into ~/self-made-bin
[group('build')]
install: build
	cp target/release/lazylab ~/self-made-bin/lazylab
	codesign -s - ~/self-made-bin/lazylab

# Format the Go code with gofumpt and the Rust code with rustfmt
[group('dev')]
fmt:
	go tool gofumpt -w .
	cargo fmt

# Every static check CI runs, in the same order
[group('dev')]
lint:
	go vet ./...
	go tool staticcheck ./...
	go tool gofumpt -l -d .
	# Reaches vuln.go.dev, so this recipe needs a network connection.
	go tool govulncheck ./...
	cargo fmt --check
	cargo clippy --all-targets --locked -- -D warnings
	# Fetches the RustSec advisory database, which also needs the network.
	cargo deny check advisories

# Report unreachable code (informational, test-only functions are expected)
[group('dev')]
deadcode:
	~/go/bin/deadcode ./...

# Remove build artifacts
[group('build')]
clean:
	rm -rf target/distrib lazylab
