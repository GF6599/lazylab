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

# Run the Rust tests
[group('dev')]
test:
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

# Build the Rust binary and record the VHS demo
[group('run')]
demo:
	cargo build --release --locked
	vhs demo.tape

# Build then copy the binary into ~/self-made-bin
[group('build')]
install: build
	cp target/release/lazylab ~/self-made-bin/lazylab
	codesign -s - ~/self-made-bin/lazylab

# Format the Rust code with rustfmt
[group('dev')]
fmt:
	cargo fmt

# Every static check CI runs, in the same order
[group('dev')]
lint:
	cargo fmt --check
	cargo clippy --all-targets --locked -- -D warnings
	# Fetches the RustSec advisory database, so this recipe needs a network connection.
	cargo deny check advisories

# Remove build artifacts
[group('build')]
clean:
	rm -rf target/distrib lazylab
