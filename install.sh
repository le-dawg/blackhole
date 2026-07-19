#!/bin/bash
set -e

echo "Building Go DNS daemon binary..."
go build -o ~/.local/bin/blackhole-dnsd src/main.go

echo "Copying LaunchAgent configuration..."
mkdir -p ~/Library/LaunchAgents
cp com.solution8.blackhole.dnsd.plist ~/Library/LaunchAgents/

echo "Loading LaunchAgent into launchd..."
# Unload if previously running
launchctl bootout gui/$(id -u)/com.solution8.blackhole.dnsd 2>/dev/null || true
# Load agent
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.solution8.blackhole.dnsd.plist

echo "Deployment successful! Daemon is running in the background."
