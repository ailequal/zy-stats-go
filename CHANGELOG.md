# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-10-11

### Added

- Signal statistics from a Zyxel Nebula FWA505 router, fetched over HTTPS with the same plaintext login the router's web interface uses; no browser is needed.
- Terminal report with LTE and 5G NR (NSA) signal values (RSRP, RSRQ, SINR, RSSI) as coloured bars, plus technology, PLMN, eNB ID, cell ID, PCI, EARFCN, primary band, carrier aggregation and an LTE Italy map link.
- `--log` flag that appends one JSON line per poll to a daily file, `ZY_LOGS_DIR/YYYY-MM-DD.log`, in local time.
- `--compact` flag that prints one line per poll instead of the full report; it combines with `--log`.
- `--once` flag that fetches the stats a single time and exits.
- `--log-level` flag (`debug`, `info`, `warn`, `error`) for structured diagnostics on stderr, and `--debug` as a shorthand for `--log-level debug` that also prints every HTTP exchange with credentials, cookies and sessionkeys redacted.
- Configuration from flags, `ZY_` environment variables (`ZY_SERVER_URL`, `ZY_USERNAME`, `ZY_PASSWORD`, `ZY_INTERVAL`, `ZY_LOGS_DIR`, `ZY_LOG_LEVEL`) and a `.env` file, in that order of precedence.
- Automatic re-login once when the router expires the session (HTTP 504 or 401), and logout on shutdown.
- Clear error messages for wrong credentials, locked accounts, session limits and unreachable routers, with exit codes `0` (success), `1` (runtime error) and `2` (invalid flags or configuration).
- `--version` flag that prints the version, the Go toolchain, the build date and the platform.
- Static binaries for Linux and macOS (amd64, arm64) and Windows (amd64), with SHA-256 checksums.
- Docker setup: a distroless production image running as a non-root user, and a development container that runs from source.
- Fake FWA505 router for tests and manual end-to-end runs (`just fakemodem`).

[unreleased]: https://github.com/ailequal/zy-stats-go/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/ailequal/zy-stats-go/releases/tag/v1.0.0
