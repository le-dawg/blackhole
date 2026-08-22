#!/bin/bash
set -e

echo "Building Go DNS daemon binary..."
go build -o blackhole-dnsd src/main.go
sudo cp blackhole-dnsd /usr/local/bin/blackhole-dnsd

echo "Copying LaunchDaemon configuration..."
sudo cp com.blackhole.dnsd.plist /Library/LaunchDaemons/com.blackhole.dnsd.plist
sudo chown root:wheel /Library/LaunchDaemons/com.blackhole.dnsd.plist

echo "Cleaning up old agents and unloading previous daemon..."
launchctl bootout gui/$(id -u)/com.blackhole.dnsd 2>/dev/null || true
rm -f ~/Library/LaunchAgents/com.blackhole.dnsd.plist
sudo launchctl bootout system/com.blackhole.dnsd 2>/dev/null || true

echo "Bootstrapping new LaunchDaemon..."
sudo launchctl bootstrap system /Library/LaunchDaemons/com.blackhole.dnsd.plist

echo "Deployment successful! Daemon is running in the background."
