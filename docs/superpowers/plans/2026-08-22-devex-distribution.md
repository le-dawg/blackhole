# DevEx & Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prepare the project for open-source distribution. Scrub all internal branding, establish standard build tooling, automate the release packaging process, and provide user-friendly installation mechanisms including a robust `install.sh` script and a Homebrew Cask.

**Architecture:** A standard `Makefile` for local dev, a `package.sh` script to bundle the compiled binaries and UI into a zip, an `install.sh` script that handles Gatekeeper quarantine stripping and LaunchDaemon templating, and a Homebrew Cask for package management.

**Tech Stack:** Bash, Make, Ruby (Homebrew Cask).

## Global Constraints

- Target platform: macOS
- No assumptions about the user's `$HOME` directory path in static files.
- Install script must handle Gatekeeper quarantine stripping correctly.
- Must cleanly uninstall prior versions before installing new ones.

---

### Task 1: Brand Scrubbing

**Files:**
- Rename: `com.solution8.blackhole.dnsd.plist` -> `com.blackhole.dnsd.plist`
- Modify: `com.blackhole.dnsd.plist`
- Modify: `src/dnsd/process_monitor_test.go`
- Modify: `install.sh`

- [ ] **Step 1: Rename and scrub the plist**
Run: `mv com.solution8.blackhole.dnsd.plist com.blackhole.dnsd.plist`
Modify `com.blackhole.dnsd.plist` to change the `<key>Label</key>` value from `com.solution8.blackhole.dnsd` to `com.blackhole.dnsd`.

- [ ] **Step 2: Scrub test mocks**
Modify `src/dnsd/process_monitor_test.go` and `src/dnsd/cache_test.go` (if applicable) to replace all instances of `com.solution8.*` with `com.blackhole.*`.
*Note: Use `grep -r "solution8" src/` to find all instances.*

- [ ] **Step 3: Scrub install.sh**
Modify `install.sh` to reference `com.blackhole.dnsd.plist` instead of the old name.

- [ ] **Step 4: Commit**
`git add . && git commit -m "chore: scrub legacy solution8 branding"`

---

### Task 2: Root Makefile

**Files:**
- Create: `Makefile`

**Interfaces:**
- Produces: `make build`, `make test`, `make clean`

- [ ] **Step 1: Create Makefile**
Create a `Makefile` at the project root:
```makefile
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
```

- [ ] **Step 2: Test Makefile**
Run: `make clean && make test`
Expected: `go test` runs and passes.

- [ ] **Step 3: Commit**
`git add Makefile && git commit -m "build: add root Makefile"`

---

### Task 3: Release Packager

**Files:**
- Create: `scripts/package.sh`

- [ ] **Step 1: Create package.sh**
Create `scripts/package.sh` (ensure it has execute permissions):
```bash
#!/bin/bash
set -e

echo "Cleaning and building..."
make clean
make build

echo "Creating staging directory..."
STAGING_DIR="blackhole-release"
rm -rf "$STAGING_DIR"
mkdir -p "$STAGING_DIR"

echo "Copying artifacts..."
cp bin/blackhole-dnsd "$STAGING_DIR/"
cp -R bin/Blackhole.app "$STAGING_DIR/"
cp com.blackhole.dnsd.plist "$STAGING_DIR/"
cp install.sh "$STAGING_DIR/"

echo "Zipping release..."
zip -r blackhole-release.zip "$STAGING_DIR"

echo "Cleaning up..."
rm -rf "$STAGING_DIR"

echo "Done. SHA-256 hash:"
shasum -a 256 blackhole-release.zip
```
Run `chmod +x scripts/package.sh`.

- [ ] **Step 2: Commit**
`git add scripts/package.sh && git commit -m "build: add release packager script"`

---

### Task 4: User-Facing Installer

**Files:**
- Modify: `install.sh`
- Modify: `com.blackhole.dnsd.plist`

- [ ] **Step 1: Template the plist**
In `com.blackhole.dnsd.plist`, find the `ProgramArguments` or `EnvironmentVariables` where the exclusions file is passed (it might be hardcoded to `/Users/...`). Change it to `{{HOME_DIR}}`.

- [ ] **Step 2: Upgrade install.sh**
Rewrite `install.sh` to safely stop old versions, remove quarantine, template the plist, and install.
```bash
#!/bin/bash
set -e

if [ "$EUID" -ne 0 ]; then
  echo "Please run as root (sudo ./install.sh)"
  exit 1
fi

# Get the actual user's home directory (SUDO_USER is preferred if run via sudo)
if [ -n "$SUDO_USER" ]; then
    USER_HOME=$(eval echo ~$SUDO_USER)
else
    USER_HOME=$HOME
fi

echo "Stopping existing daemon (if any)..."
launchctl unload /Library/LaunchDaemons/com.blackhole.dnsd.plist 2>/dev/null || true

echo "Removing Gatekeeper quarantine..."
xattr -rd com.apple.quarantine blackhole-dnsd 2>/dev/null || true
xattr -rd com.apple.quarantine Blackhole.app 2>/dev/null || true

echo "Installing binaries..."
cp blackhole-dnsd /usr/local/bin/
rm -rf /Applications/Blackhole.app
cp -R Blackhole.app /Applications/

echo "Configuring LaunchDaemon..."
# Replace {{HOME_DIR}} with actual home dir in the plist
sed "s|{{HOME_DIR}}|$USER_HOME|g" com.blackhole.dnsd.plist > /Library/LaunchDaemons/com.blackhole.dnsd.plist

echo "Loading LaunchDaemon..."
launchctl load /Library/LaunchDaemons/com.blackhole.dnsd.plist

echo "Installation complete! You can now open Blackhole from Applications."
```

- [ ] **Step 3: Commit**
`git add install.sh com.blackhole.dnsd.plist && git commit -m "feat: robust install script with quarantine strip and templating"`

---

### Task 5: Homebrew Cask

**Files:**
- Create: `Casks/blackhole.rb`

- [ ] **Step 1: Create Cask Formula**
Create `Casks/blackhole.rb`:
```ruby
cask "blackhole" do
  version "1.0.0"
  sha256 "REPLACE_WITH_SHA256"

  url "https://github.com/solution8/blackhole/releases/download/v#{version}/blackhole-release.zip"
  name "Blackhole"
  desc "A Pi-hole-grade DNS adblocker for macOS"
  homepage "https://github.com/solution8/blackhole"

  app "blackhole-release/Blackhole.app"
  binary "blackhole-release/blackhole-dnsd", target: "/usr/local/bin/blackhole-dnsd"

  postflight do
    # Note: A real postflight might run the install script or load the daemon via sudo.
    # For this task, we will just echo instructions to the user.
    system_command "xattr",
                   args: ["-rd", "com.apple.quarantine", "#{appdir}/Blackhole.app"],
                   sudo: true
  end

  uninstall delete: "/Library/LaunchDaemons/com.blackhole.dnsd.plist",
            quit:   "com.blackhole.MenuBar"

  caveats <<~EOS
    To start the background DNS daemon, you must run the install script manually:
      sudo /usr/local/bin/blackhole-dnsd --install
  EOS
end
```
*(Wait, we need to scrub solution8 from the URL and homepage! Change them to `github.com/blackhole-dns/blackhole` or similar generic OSS org).*

```ruby
cask "blackhole" do
  version "1.0.0"
  sha256 "REPLACE_WITH_SHA256"

  url "https://github.com/blackhole-dns/blackhole/releases/download/v#{version}/blackhole-release.zip"
  name "Blackhole"
  desc "A Pi-hole-grade DNS adblocker for macOS"
  homepage "https://github.com/blackhole-dns/blackhole"

  app "blackhole-release/Blackhole.app"
  binary "blackhole-release/blackhole-dnsd", target: "/usr/local/bin/blackhole-dnsd"

  postflight do
    system_command "xattr",
                   args: ["-rd", "com.apple.quarantine", "#{appdir}/Blackhole.app"],
                   sudo: true
  end

  uninstall delete: "/Library/LaunchDaemons/com.blackhole.dnsd.plist",
            quit:   "com.blackhole.MenuBar"
end
```

- [ ] **Step 2: Commit**
`git add Casks/ && git commit -m "feat: add homebrew cask formula"`

---
