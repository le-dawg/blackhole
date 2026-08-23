# Blackhole

[![Go Report Card](https://goreportcard.com/badge/github.com/blackhole/blackhole)](https://goreportcard.com/report/github.com/blackhole/blackhole)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**Blackhole** is a lightweight, high-performance local DNS daemon and adblocker designed specifically for macOS. It sinks unwanted domains into the void, giving you back control over your network traffic.

## Features

- **Fast & Lightweight:** Built in Go for maximum performance with minimal resource footprint.
- **macOS Native:** Integrates cleanly with macOS using `launchd`.
- **Customizable Blocklists:** Easily configure and manage your own DNS blocklists.

## Getting Started

### Prerequisites

- Go 1.26 or higher
- macOS environment

### Installation

You can install Blackhole using the provided installation script:

```bash
./install.sh
```

Alternatively, you can build from source:

```bash
make build
make install
```

## Usage

Once installed, the `blackholed` daemon runs in the background. You can interact with the system using the `blackhole` CLI tool.

## Contributing

We welcome contributions! Please refer to the CNCF guidelines for contributing to our repository.

### Code of Conduct

This project adheres to the CNCF Code of Conduct. By participating, you are expected to uphold this code.

## License

This project is licensed under the MIT License.
