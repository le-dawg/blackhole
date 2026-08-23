# Contributing to Blackhole

First off, thank you for considering contributing to Blackhole. It's people like you that make Blackhole such a great tool.

## Developer Workflow

1. Fork the repository on GitHub.
2. Clone your forked repository to your local machine.
3. Create a new branch for your work:
   - For features: `git checkout -b feature/your-feature-name`
   - For bug fixes: `git checkout -b fix/your-bug-fix-name`
4. Make your changes and commit them with clear, descriptive commit messages.
5. Push your branch to your fork on GitHub.
6. Submit a Pull Request against the `main` branch of the upstream repository.

## Local Setup

To set up Blackhole locally for development, you'll need Go installed on your machine.

1. **Build the project:** Run `make build` in the root directory. This will compile the daemon and CLI.
2. **Run tests:** Run `make test` to execute the full test suite.
3. **Format code:** Always format your code before committing using `go fmt ./...`.

## Code Standards

- We strictly follow Effective Go guidelines.
- All code must pass `golangci-lint` without any warnings. Run `golangci-lint run` locally before submitting a PR.
- We require a minimum of 80% test coverage for all new features.
- Ensure your tests are robust and cover edge cases.

## Issue Tracking

When opening an issue, please use the provided templates:
- **Bug Reports:** Include clear steps to reproduce, expected behavior, and actual behavior.
- **Feature Requests:** Detail the use case, proposed solution, and any alternatives considered.
