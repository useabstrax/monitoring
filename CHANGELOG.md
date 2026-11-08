# Changelog

All notable changes to the Abstrax Monitoring plugin are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-11-08

First release of the official Abstrax Monitoring plugin (`abstrax-monitoring` → `abstrax monitoring …`).

### Added

- **Local collection** — Host, filesystem, and per-process metrics from `/proc` and `/sys` on the Abstrax platform matrix. No per-distro collector.
- **SQLite history** — WAL, five retention tiers, weighted rate rollups, and a storage ceiling that outranks time retention.
- **Process identity** — `instance_id` from boot id, PID, and starttime, so PID reuse and reboot do not merge history. Exited processes stay queryable until expiry.
- **Daemon and CLI** — Root-only Unix socket. `setup`, `config`, service control, `status`, `latest`, `history`, `processes`, `process`, `live`, and `storage status`.
- **`latest.json`** — Atomic vitals file for a future heartbeat. The heartbeat is unchanged.
- **systemd** — `abstrax-monitoring.service`, restart on failure, exit 78 on bad config.
