.PHONY: build test clean run-local

build:
	mkdir -p bin
	go build -o bin/blackhole-dnsd ./src
	xcodebuild -project MenuBar/Blackhole.xcodeproj -scheme Blackhole -configuration Release CONFIGURATION_BUILD_DIR=$(PWD)/bin CODE_SIGN_IDENTITY="" CODE_SIGNING_REQUIRED=NO

test:
	go test ./...

clean:
	rm -rf bin/
	rm -rf .build/
	rm -f blackhole-release.zip

run-local: build
	sudo ./bin/blackhole-dnsd
