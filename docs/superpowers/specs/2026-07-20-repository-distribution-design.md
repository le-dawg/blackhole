# Specification: GitHub Repository Setup & Release Packaging Pipeline

This specification outlines the developer experience (devex) repository structure, local build/test task configurations, and the automated packaging/installer pipeline for Project Blackhole.

---

## 1. Repository Structure & Git Isolation

The repository will be reorganized into a clean, standard hybrid Go/Swift project structure.

### Project Layout
```
blackhole/
├── docs/                   # Specs, designs, and development plans
├── src/                    # Go codebase
│   ├── dnsd/               # DNS core logic, process correlation, and resolver
│   └── main.go             # DNS Daemon CLI entry point
├── MenuBar/                # SwiftUI Menu Bar App
│   ├── Package.swift       # SPM Project configuration
│   ├── BlackholeApp.swift  # App entry point
│   ├── DNSHelper.swift     # DNS Override controller
│   └── Views/              # Popover UI views
├── Makefile                # Unified developer task runner (build, test, run)
├── README.md               # GitHub homepage documentation
├── package.sh              # Developer packaging script (makes zip artifact)
├── install.sh              # User-facing curl installation script
└── com.solution8.blackhole.dnsd.plist  # LaunchDaemon Template
```

### Git Settings (`.gitignore`)
To prevent committing build caches, logs, databases, and local runtime configurations:
```gitignore
# Go Build Artifacts & Caches
blackhole-dnsd
bin/
*.exe
*.test
*.prof

# Swift / SPM Build Artifacts
MenuBar/.build/
MenuBar/.swiftpm/
MenuBar/Package.resolved

# Local Runtime & Logs
var/log/
*.log
*.json
!tests/**/*.json

# OS Specific
.DS_Store
```

---

## 2. Developer Task Runner (`Makefile`)

A root `Makefile` will provide shortcuts for building, testing, cleaning, and debugging:

```makefile
.PHONY: build test run-local clean

build:
	mkdir -p build
	go build -o build/blackhole-dnsd src/main.go
	cd MenuBar && swift build -c release
	cp MenuBar/.build/release/MenuBar build/BlackholeApp

test:
	go test -race -v ./src/dnsd
	cd MenuBar && swift test

run-local:
	go run src/main.go -port 5354 -exclusions ~/Library/Application\ Support/blackhole/exclusions.json

clean:
	rm -rf build
	rm -rf MenuBar/.build
```

---

## 3. Distribution Packaging Pipeline

### A. Release Packager (`package.sh`)
A developer script executed locally to generate release zip archives:
1. Compiles the Go binary optimized for size (`-ldflags="-s -w"`).
2. Generates the standard macOS App structure (`build/Blackhole.app/Contents/MacOS/Blackhole`).
3. Embeds a minimal `Info.plist` setting `LSUIElement=true` to hide the application dock icon.
4. Packs `blackhole-dnsd`, `Blackhole.app`, and `com.solution8.blackhole.dnsd.plist` into `blackhole-release.zip`.

### B. User-Facing Installer (`install.sh`)
A shell script that users can download via `curl` to install the latest pre-compiled GitHub release:
1. Queries the GitHub API to find the latest release artifact `blackhole-release.zip`.
2. Downloads and extracts it to a temporary directory.
3. Moves `blackhole-dnsd` to `/usr/local/bin/blackhole-dnsd` (using `sudo`).
4. Moves `Blackhole.app` to `/Applications/Blackhole.app` (using `sudo`).
5. Copies `com.solution8.blackhole.dnsd.plist` to `/Library/LaunchDaemons/com.solution8.blackhole.dnsd.plist` (using `sudo`).
6. Fixes LaunchDaemon ownership and loads it:
   ```bash
   sudo chown root:wheel /Library/LaunchDaemons/com.solution8.blackhole.dnsd.plist
   sudo launchctl bootstrap system /Library/LaunchDaemons/com.solution8.blackhole.dnsd.plist
   open -a /Applications/Blackhole.app
   ```
