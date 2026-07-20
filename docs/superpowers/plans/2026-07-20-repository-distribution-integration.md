# Project Blackhole Repository Setup & Distribution Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Configure developer environment settings, write a unified developer Makefile task runner, and implement release packaging and install scripts to facilitate easy GitHub sharing.

**Architecture:** 
- A root `.gitignore` isolates build and personal runtime assets.
- A `Makefile` binds Go compiling, Swift package builds, safe user-space local DNS daemon runs, and cleanup routines.
- A developer shell script `package.sh` compiles size-stripped binaries, structures a macOS App bundle `Blackhole.app` with `LSUIElement=true`, and zips them.
- A user-facing `install.sh` script downloads the latest release from GitHub, deploys the binary and app, and registers the LaunchDaemon.

**Tech Stack:** Go, Swift, Bash, Apple launchd plist.

## Global Constraints
- Target platform: macOS 15+ (Tahoe).
- Resource target: Total RAM < 35MB (Daemon <15MB, Widget <20MB). Idle CPU < 0.1%.
- Must run in user-space without private system extensions/entitlements.
- Process-level exclusion matching must support LiteLLM (python processes executing the `litellm` module) and application bundle IDs.
- VPN stack changes must be handled dynamically via SCDynamicStore notifications.
- LaunchDaemon label: `com.solution8.blackhole.dnsd`
- Binary build output: `/usr/local/bin/blackhole-dnsd`

---

## Tasks

### Task 9: Git Isolation & Developer Task Runner

**Files:**
- Create: `Makefile`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: Go daemon source files, Swift MenuBar SPM project
- Produces: Standard developer task automation commands

- [ ] **Step 1: Write the `.gitignore` additions**

  Modify `.gitignore` in the project root directory to add the following lines:
  ```gitignore
  # Go Build Artifacts & Caches
  blackhole-dnsd
  bin/
  *.exe
  *.test
  *.prof
  build/

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

- [ ] **Step 2: Create root Makefile**

  Create a file named `Makefile` in the project root directory with the following contents:
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

- [ ] **Step 3: Test local compilation and cleanup tasks**

  Run commands in terminal:
  ```bash
  make build
  make clean
  ```
  Expected output: `make build` compiles Go and Swift successfully into `build/` directory; `make clean` deletes `build/` and `MenuBar/.build/` caches cleanly.

- [ ] **Step 4: Commit**

  Run:
  ```bash
  git add Makefile .gitignore
  git commit -m "feat(devex): add root Makefile and configure robust .gitignore isolation"
  ```

---

### Task 10: Release Packager Tool

**Files:**
- Create: `package.sh`

**Interfaces:**
- Consumes: Compiled Go binary and compiled Swift Menu Bar application
- Produces: Compressed release archive `blackhole-release.zip`

- [ ] **Step 1: Write the release packager script**

  Create `package.sh` in the project root directory with the following contents:
  ```bash
  #!/bin/bash
  set -e

  echo "Cleaning previous build folders..."
  rm -rf build
  mkdir -p build/Blackhole.app/Contents/MacOS
  mkdir -p build/release

  echo "Building daemon with size optimization..."
  go build -ldflags="-s -w" -o build/blackhole-dnsd src/main.go

  echo "Building Swift Menu Bar client app..."
  cd MenuBar
  swift build -c release
  cd ..
  cp MenuBar/.build/release/MenuBar build/Blackhole.app/Contents/MacOS/Blackhole

  echo "Creating App Info.plist..."
  cat <<EOF > build/Blackhole.app/Contents/Info.plist
  <?xml version="1.0" encoding="UTF-8"?>
  <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
  <plist version="1.0">
  <dict>
      <key>CFBundleExecutable</key>
      <string>Blackhole</string>
      <key>CFBundleIdentifier</key>
      <string>com.solution8.blackhole.app</string>
      <key>CFBundleName</key>
      <string>Blackhole</string>
      <key>CFBundleVersion</key>
      <string>1.0.0</string>
      <key>LSUIElement</key>
      <true/>
  </dict>
  </plist>
  EOF

  echo "Copying LaunchDaemon plist template..."
  cp com.solution8.blackhole.dnsd.plist build/

  echo "Creating distribution ZIP..."
  cd build
  zip -r ../blackhole-release.zip blackhole-dnsd Blackhole.app com.solution8.blackhole.dnsd.plist
  cd ..

  echo "Release package created successfully at blackhole-release.zip!"
  ```

- [ ] **Step 2: Run release script to verify archive generation**

  Run commands:
  ```bash
  chmod +x package.sh
  ./package.sh
  ```
  Expected output: "Release package created successfully at blackhole-release.zip!".
  Verify using `unzip -l blackhole-release.zip` to ensure files (`blackhole-dnsd`, `Blackhole.app/Contents/MacOS/Blackhole`, `Blackhole.app/Contents/Info.plist`, `com.solution8.blackhole.dnsd.plist`) are present.

- [ ] **Step 3: Commit**

  Run:
  ```bash
  git add package.sh
  git commit -m "feat(devex): implement automated release packaging script package.sh"
  ```

---

### Task 11: User-Facing Installer & Readme

**Files:**
- Create: `install.sh`
- Create: `README.md`

**Interfaces:**
- Consumes: Released zip archive from GitHub
- Produces: Installed daemon and SwiftUI app loaded system-wide

- [ ] **Step 1: Create user-facing install.sh**

  Create `install.sh` in the project root directory with the following contents:
  ```bash
  #!/bin/bash
  set -e

  REPO="thedawgctor/blackhole"
  TEMP_DIR=$(mktemp -d)

  echo "Fetching latest release from GitHub..."
  # Resolves latest release zip download URL from GitHub releases api
  DOWNLOAD_URL=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep "browser_download_url" | cut -d '"' -f 4)

  if [ -z "$DOWNLOAD_URL" ]; then
      echo "Error: No release found. Please build from source or check repo path."
      exit 1
  fi

  echo "Downloading package..."
  curl -L -o "$TEMP_DIR/release.zip" "$DOWNLOAD_URL"

  echo "Extracting files..."
  unzip -q "$TEMP_DIR/release.zip" -d "$TEMP_DIR"

  echo "Deploying binaries and configs..."
  sudo mkdir -p /usr/local/bin
  sudo cp "$TEMP_DIR/blackhole-dnsd" /usr/local/bin/
  sudo cp -R "$TEMP_DIR/Blackhole.app" /Applications/
  sudo cp "$TEMP_DIR/com.solution8.blackhole.dnsd.plist" /Library/LaunchDaemons/

  echo "Setting permissions and starting LaunchDaemon..."
  sudo chown root:wheel /Library/LaunchDaemons/com.solution8.blackhole.dnsd.plist
  sudo launchctl bootout system/com.solution8.blackhole.dnsd 2>/dev/null || true
  sudo launchctl bootstrap system /Library/LaunchDaemons/com.solution8.blackhole.dnsd.plist

  echo "Starting Menu Bar client application..."
  open -a /Applications/Blackhole.app

  echo "Clean up..."
  rm -rf "$TEMP_DIR"

  echo "Project Blackhole has been successfully installed and launched!"
  ```

- [ ] **Step 2: Create project README.md**

  Create `README.md` in the project root directory:
  ```markdown
  # Blackhole: Local DNS Ad-Blocker for macOS

  Blackhole is an AI-friendly, lightweight, zero-latency local DNS ad-blocker for macOS 15+. It intercepts advertising domains at the resolver level and supports dynamic bypass exclusions for developer workspaces.

  ## Features
  * **System-wide DNS Filtering**: served locally on port 53.
  * **Dynamic VPN Monitoring**: automatic upstream updates when connecting/disconnecting network interfaces.
  * **SwiftUI Menu Bar UI**: premium translucency status Extra with App-level bypass configuration toggles.
  * **Developer Bypass exclusions**: dynamically exclude specific scripts (like LiteLLM / python) or macOS Bundle IDs from filtering.

  ## Installation (Latest Release)
  To install pre-compiled binaries and register the LaunchDaemon:
  ```bash
  curl -fsSL https://raw.githubusercontent.com/thedawgctor/blackhole/main/install.sh | bash
  ```

  ## Developer Setup (Source Build)
  Prerequisites: Go 1.26+, Xcode/Command Line Tools.

  ```bash
  # Compile both daemon and app
  make build

  # Run test suites
  make test

  # Run daemon locally on user port 5354
  make run-local
  ```
  ```

- [ ] **Step 3: Verify install script parsing**

  Run: `bash -n install.sh`
  Expected: Exits successfully (0) indicating syntactically correct bash script.

- [ ] **Step 4: Commit**

  Run:
  ```bash
  git add install.sh README.md
  git commit -m "feat(deploy): implement curl install script and GitHub README documentation"
  ```
