# Abstrax Monitoring

`abstrax-monitoring` is the official Abstrax plugin for local Linux host and process monitoring. The CLI is `abstrax monitoring …`. It collects from `/proc` and `/sys`, keeps a durable SQLite history on the server, and answers queries over a root-only Unix socket.

It does not talk to ServerAuth, the agent, the gateway, or Console. A future heartbeat can read `latest.json`. This plugin does not change the heartbeat.

## Install

Release archives are `linux-amd64` and `linux-arm64`. Which distributions those binaries run on is Abstrax's platform matrix (`docs/reference/supported-platforms.md`). Setup asks `abstrax doctor --json` and refuses a host the CLI does not support. Collectors do not branch on distribution.

```bash
sudo abstrax plugin install monitoring
sudo abstrax monitoring setup
abstrax monitoring status
```

`setup` creates directories, writes the default config when none exists, installs and starts `abstrax-monitoring.service`, and waits until the daemon answers. An existing config file is kept.

| Path | Purpose |
|---|---|
| `/usr/local/lib/abstrax/plugins/abstrax-monitoring` | Plugin binary |
| `/etc/abstrax/plugins/monitoring/config.yaml` | Configuration, mode `0600` |
| `/var/lib/abstrax/plugins/monitoring/metrics.db` | SQLite history, mode `0600` |
| `/var/lib/abstrax/plugins/monitoring/latest.json` | Small vitals file for a future heartbeat |
| `/var/lib/abstrax/plugins/monitoring/monitoring.sock` | Root-only daemon socket |
| `/etc/systemd/system/abstrax-monitoring.service` | systemd unit |

A non-root process uses the XDG config and data directories instead. The daemon is intended to run as root: it must read every process, and v1 access is root-only. There is no monitoring group. The state directory is mode `0700`. Command lines are stored truncated and are not written to the log.

## How it runs

The daemon owns collection, SQLite, retention, rollups, queries, and live subscribers. CLI commands other than `run` dial the socket. They do not open the database.

Collection intervals default to 5s for the host and processes and 60s for filesystems. The first host sample is a baseline, so `latest.json` appears on the second host sample. Filesystem `statfs` runs off the sampler goroutine, one probe per mount, so a hung mount cannot stall collection.

Shutdown on SIGTERM or SIGINT checkpoints SQLite and exits. SIGHUP reloads the config file. A bad config makes `run` exit 78, and the unit sets `RestartPreventExitStatus=78` so systemd does not restart-loop.

## Commands

Globals: `--json`, `--json-stream`, `--yes`, `--verbose`, `--quiet`, `--no-color`, `--config`, `--state-dir`.

| Command | What it does |
|---|---|
| `setup` | Platform check, directories, default config, systemd install, first sample |
| `config` | Show or change intervals, retention, rollups, and the storage ceiling |
| `start` / `stop` / `restart` | Control the systemd unit |
| `status` | Daemon health, not the current sample. A stopped daemon is data, exit 0 |
| `latest` | Newest host, filesystem, and top-process summary |
| `history --from --to [--max-points] [--cursor]` | One aligned host and filesystem page |
| `processes [--at] [--sort] [--limit] [--name]` | Current processes, or the processes responsible at a past time |
| `process <instance-id>` | One process instance, including after it has exited |
| `process history <instance-id> --from --to` | History for that instance |
| `live` | NDJSON samples (`--json-stream`; `--json` is rejected) |
| `storage status` | Database size, effective retention, and whether the ceiling is binding |
| `install-service` / `uninstall-service` | Manage the systemd unit |
| `run` | Foreground daemon |
| `version` | Plugin version |
| `plugin metadata` | Plugin metadata protocol v1. Only `live` is streamable |

`config` validates before saving and asks the running daemon to reload. Sub-second polling is rejected. Retention windows must increase (`raw < short < medium < long < maximum`). Rollup widths must increase and each width must be a multiple of the finer one.

## What is collected

Host: CPU usage, user, system, iowait, and steal as percentages of the host (0–100; iowait is not counted as busy); CPU cores; memory and swap bytes plus usage percent (`61.4` means 61.4%); load; uptime; aggregate disk and network bytes per second. Loopback, virtual interfaces that would double-count traffic, partitions, loop devices, and stacked disks are skipped.

Filesystems: one series per real mount. `proc`, `tmpfs`, `overlay`, `squashfs`, and the other pseudo types are skipped. Each point has mount, device, type, total, used, available, usage, and inodes.

Processes: PID, PPID, boot id, starttime ticks, start time, name, executable, truncated command line, user, Linux CPU percent (this can exceed 100), RSS, memory percent, read and write bytes per second, threads, and state. The public `instance_id` is the first 16 bytes of SHA-256 over boot id, PID, and starttime, hex encoded. A recycled PID or a reboot does not merge history. A process seen once is stored immediately, so it can still be attributed if it exits before a rate exists. Exited processes stay queryable until retention deletes them.

Per-core, per-interface, and per-device history are not stored. The schema can gain those series later without a new database.

## History and storage

SQLite is CGO-free (`modernc.org/sqlite`), WAL, `synchronous=FULL`, one connection, `user_version` migrations. The public `schema_version` is 1 and is not the plugin version or the SQLite `user_version`. SQL is not part of the API.

Defaults, all configurable:

| Tier | Width | Kept until |
|---|---|---|
| raw | sampling interval (stored as resolution 0) | 1 hour |
| short | 15s | 6 hours |
| medium | 1m | 24 hours |
| long | 5m | 7 days |
| archive | 1h | 90 days |

Gauges roll up as sum, max, and count, so a bucket average is not an average of averages. Rates store byte deltas and duration; the average rate is total bytes over total duration, and the max is the highest observed rate. A timestamp lives at one resolution. Rollup and delete of a tier run in one transaction.

`history` picks the finest tier that can cover the window within `--max-points` (default 240, hard maximum 500) and stays under about 768 KiB so a page fits an agent result. Host and filesystem points share one `from`, `to`, and resolution. Gaps are missing samples, not fabricated zeros. The cursor is opaque and pinned to that page's resolution; it fails if the underlying rows have been deleted or rolled up. There is no bulk export and no page numbers.

The storage ceiling (default `1GB`, decimal, 1000000000 bytes) wins over time retention. The oldest rows are deleted, collection continues, and `storage status` reports both the configured horizon and the oldest sample actually kept. `storage_constrained` is true when the database is at least 80% of the ceiling and the retained span is shorter than the configured maximum by more than one archive bucket. A young database under the ceiling is not constrained.

## Live samples and latest.json

`monitoring live` prints one JSON object per host sample, with `"type": "sample"`. It is not a job-progress envelope. The same host sample is committed before it is published. The message also carries the latest persisted filesystems and the top 15 processes by CPU. Each subscriber has a buffer of 4. A slow client is disconnected. There is no spool, and one client cannot block collection or the others. At most 8 subscribers.

`latest.json` is written by temp file, chmod `0600`, and rename. It contains `schema_version`, `collected_at`, `expected_interval_seconds`, and `vitals` (`cpu_usage_pct`, `memory_usage_pct`, `disk_usage_pct_max`, `disk_usage_mount`, `load_1m`, `cpu_cores`). It is not a full dump.

## Measured cost

`TestResourceProfile` (`go test ./internal/store/ -run TestResourceProfile`) on darwin/arm64, one process pass over a fixture `/proc` plus one SQLite write and a top-25 query. The heap figure is the test process, not a long-running Linux daemon. Database bytes include a fresh SQLite file, so the 25-process file is mostly overhead.

| Processes | Collect | Write | Top-25 query | Database |
|---|---|---|---|---|
| 25 | 0.68 ms | 1.82 ms | 0.30 ms | 145 KiB |
| 100 | 2.62 ms | 5.12 ms | 0.24 ms | 181 KiB |
| 250 | 6.80 ms | 12.3 ms | 0.32 ms | 233 KiB |
| 500 | 12.3 ms | 23.7 ms | 0.47 ms | 322 KiB |
| 1000 | 26.3 ms | 47.9 ms | 0.63 ms | 479 KiB |

From the 25-process file (148136 bytes) and the 1000-process file (490096 bytes), a process sample costs about 350 bytes once the fresh database is subtracted. Steady-state size is bounded by the storage ceiling. Collection at the default 5s interval is far below one core at 1000 processes in this fixture. Re-run the test on a Linux server before treating these numbers as production RSS.

## Development

No Makefile. From `plugins/monitoring`:

```bash
gofmt -w .
go vet ./...
go test -race ./...
go build -o bin/abstrax-monitoring ./cmd/abstrax-monitoring
```

Tests drive collectors from a fixture `/proc` and `/sys`. They do not read the machine they run on. systemd install is rendered and asserted; this tree's development host is not the place those tests start a real unit.

## Out of scope

Console, ServerAuth, the agent, the gateway, heartbeat protocol changes, alerting, logs, journald, a central store, remote shell, process kill, tracing, and Prometheus, Netdata, OpenTelemetry, or Grafana export. Threshold events are not emitted.
