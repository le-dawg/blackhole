.PHONY: build test clean run-local preflight

preflight:
	go mod tidy
	goimports -w src/
	golangci-lint run ./src/...
	go test -race ./src/dnsd/...
	govulncheck ./...

build:
	mkdir -p bin
	go build -o bin/blackhole-dnsd ./src
	cd MenuBar && swift build -c release

test:
	go test ./...
	cd MenuBar && swift test

clean:
	rm -rf bin/
	rm -rf .build/
	rm -f blackhole-release.zip
	cd MenuBar && rm -rf .build/

run-local: build
	sudo ./bin/blackhole-dnsd
