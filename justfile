# Common Lazylab development helpers.

default:
	@just --list

run HOST="https://gitlab.com" TOKEN="$GITLAB_TOKEN":
	go run ./cmd/lazylab --host {{HOST}} --token {{TOKEN}}

test:
	go test -race ./...

build:
	mkdir -p build
	GOOS=darwin GOARCH=$(go env GOARCH) go build -o build/lazylab-darwin-$(go env GOARCH) ./cmd/lazylab
	GOOS=linux GOARCH=amd64 go build -o build/lazylab-linux-amd64 ./cmd/lazylab

demo:
	go build -o lazylab ./cmd/lazylab
	vhs demo.tape

install: build
	cp build/lazylab-darwin-$(go env GOARCH) ~/self-made-bin/lazylab
	codesign -s - ~/self-made-bin/lazylab

clean:
	rm -rf build lazylab
