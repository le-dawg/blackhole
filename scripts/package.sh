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
