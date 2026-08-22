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
launchctl bootout gui/$(id -u)/com.solution8.blackhole.dnsd 2>/dev/null || true
launchctl bootout gui/$(id -u)/com.blackhole.dnsd 2>/dev/null || true
launchctl bootout system/com.solution8.blackhole.dnsd 2>/dev/null || true
launchctl bootout system/com.blackhole.dnsd 2>/dev/null || true
launchctl unload /Library/LaunchDaemons/com.blackhole.dnsd.plist 2>/dev/null || true

echo "Building Go DNS daemon binary..."
go build -o blackhole-dnsd src/main.go 2>/dev/null || true

echo "Removing Gatekeeper quarantine..."
xattr -rd com.apple.quarantine blackhole-dnsd 2>/dev/null || true
xattr -rd com.apple.quarantine Blackhole.app 2>/dev/null || true

echo "Installing binaries..."
cp blackhole-dnsd /usr/local/bin/
rm -rf /Applications/Blackhole.app
cp -R Blackhole.app /Applications/ 2>/dev/null || true

echo "Configuring LaunchDaemon..."
# Replace {{HOME_DIR}} with actual home dir in the plist
sed "s|{{HOME_DIR}}|$USER_HOME|g" com.blackhole.dnsd.plist > /Library/LaunchDaemons/com.blackhole.dnsd.plist
chown root:wheel /Library/LaunchDaemons/com.blackhole.dnsd.plist

echo "Loading LaunchDaemon..."
launchctl load /Library/LaunchDaemons/com.blackhole.dnsd.plist

echo "Installation complete! You can now open Blackhole from Applications."
