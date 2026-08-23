# Security Policy

## Supported Versions

We take security seriously. The following table outlines which versions of the Blackhole daemon currently receive security updates:

| Version | Supported          |
| ------- | ------------------ |
| v1.x    | :white_check_mark: |
| < v1.0  | :x:                |

## Reporting Vulnerabilities

**Please DO NOT report security vulnerabilities via public GitHub issues.**

If you discover a security vulnerability, please send an email directly to `security@blackholedns.com`.
If you wish to encrypt your communication, please request our public PGP key via email before sending sensitive information.

## Response SLA

- **Triage:** We commit to triaging all security reports within 48 hours of receipt.
- **Patching:** If the vulnerability is confirmed, we aim to release a patch within 14 days, depending on the severity and complexity of the issue.

## Threat Model Scope

**In Scope:**
- Remote code execution (RCE) via malformed DNS packets.
- DNS Cache poisoning attacks against the daemon.
- Privilege escalation from the unprivileged daemon user to root.
- Unauthenticated access to the IPC socket (if configured securely but bypassed).

**Out of Scope:**
- Vulnerabilities requiring local root access or physical access to the machine.
- Denial of Service (DoS) attacks requiring massive bandwidth (we rely on OS-level rate limiting).
- Issues related to upstream DNS servers returning malicious responses (unless caching is flawed).
