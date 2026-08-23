.PHONY: build test clean run-local preflight

preflight:
	go mod tidy
	goimports -w src/
	golangci-lint run ./src/...
	go test -race ./src/dnsd/...
	govulncheck ./...

package: build
	rm -rf blackhole-release
	mkdir -p blackhole-release/Blackhole.app/Contents/MacOS
	cp bin/blackhole-dnsd blackhole-release/
	cp MenuBar/Info.plist blackhole-release/Blackhole.app/Contents/Info.plist
	cp MenuBar/.build/release/MenuBar blackhole-release/Blackhole.app/Contents/MacOS/Blackhole
	cp install.sh blackhole-release/
	cp com.blackhole.dnsd.plist blackhole-release/
	zip -r blackhole-release.zip blackhole-release
	@SHA=$$(shasum -a 256 blackhole-release.zip | awk '{print $$1}') && sed -i "" "s/sha256 .*/sha256 \"$$SHA\"/" Casks/blackhole.rb

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
