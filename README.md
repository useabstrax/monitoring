# Abstrax Monitoring Plugin

Free, open-source host and process monitoring for an Abstrax server. Metrics stay on the machine, in a local SQLite history.

Binary: `abstrax-monitoring` → `abstrax monitoring …`  
Trust level: `official`  
Licence: Apache-2.0  
OS: Debian/Ubuntu and RHEL-family.

Full user docs: [useabstrax.com/docs/plugins/official/monitoring](https://useabstrax.com/docs/plugins/official/monitoring)

## What it does

A root daemon samples `/proc` and `/sys` and stores the results under `/var/lib/abstrax/plugins/monitoring/`. The CLI talks to that daemon over a Unix socket.

- Host CPU, memory, swap, load, uptime, and aggregate disk and network rates
- Capacity and inodes for each real filesystem mount
- Per-process CPU, memory, disk I/O, and a stable `instance_id` that survives PID reuse and reboot
- History with automatic rollups, plus a live NDJSON stream
- A storage ceiling so the database cannot grow without limit

Sampling defaults to 5s for the host and processes, and 60s for filesystems. The daemon runs as root so it can read every process. The state directory is mode `0700`. The database, socket, and `latest.json` are mode `0600`. Command lines are truncated before they are stored, and they are not written to the log.

## Install

```bash
sudo abstrax plugin install monitoring
sudo abstrax monitoring setup
abstrax monitoring version
```

`setup` checks the host with `abstrax doctor`, writes the default config when none exists, installs `abstrax-monitoring.service`, and waits for the daemon to answer. An existing config file is left in place.

Local build:

```bash
go build -o bin/abstrax-monitoring ./cmd/abstrax-monitoring
sudo cp bin/abstrax-monitoring /usr/local/lib/abstrax/plugins/
```

Tagged releases (`v*`) publish linux-amd64 / linux-arm64 archives and `plugin-manifest.json`.

| Path | Purpose |
|------|---------|
| `/usr/local/lib/abstrax/plugins/abstrax-monitoring` | Plugin binary |
| `/etc/abstrax/plugins/monitoring/config.yaml` | Configuration |
| `/var/lib/abstrax/plugins/monitoring/metrics.db` | SQLite history |
| `/var/lib/abstrax/plugins/monitoring/latest.json` | Latest vitals |
| `/var/lib/abstrax/plugins/monitoring/monitoring.sock` | Daemon socket |
| `/etc/systemd/system/abstrax-monitoring.service` | systemd unit |

## Quick start

```bash
sudo abstrax monitoring setup
sudo abstrax monitoring status
sudo abstrax monitoring latest

sudo abstrax monitoring history \
  --from=2026-11-08T00:00:00Z \
  --to=2026-11-08T12:00:00Z

sudo abstrax monitoring processes --sort=cpu --limit=10
sudo abstrax monitoring live
```

`latest.json` is rewritten after each host sample once a baseline exists, so the file appears on the second sample. It holds `schema_version`, `collected_at`, the expected interval, and vitals: CPU, memory, the fullest filesystem, 1-minute load, and CPU cores.

## Commands

The socket is root-only. Query commands need `sudo` when the daemon is running as root.

| Command | Root? | Description |
|---------|-------|-------------|
| `monitoring setup` | yes | Config, systemd unit, and the first sample |
| `monitoring config` | yes (writes) | Show or change intervals, retention, rollups, and the storage ceiling |
| `monitoring start` / `stop` / `restart` | yes | Control the systemd unit |
| `monitoring install-service` | yes | Install and enable the unit |
| `monitoring uninstall-service` | yes | Stop and remove the unit |
| `monitoring status` | yes | Daemon health, retention, and database size |
| `monitoring latest` | yes | Current host, filesystems, and top processes |
| `monitoring history` | yes | One page of host and filesystem history |
| `monitoring processes` | yes | Current processes, or `--at` a past time |
| `monitoring process <instance-id>` | yes | One process, including after it has exited |
| `monitoring process history <instance-id>` | yes | History for that instance |
| `monitoring live` | yes | NDJSON samples. Use `--json-stream` |
| `monitoring storage status` | yes | Database size, oldest sample, and the storage ceiling |
| `monitoring run` | yes | Run the daemon in the foreground |
| `monitoring version` | no | Plugin version |

Globals: `--json`, `--json-stream`, `--yes`, `--verbose`, `--quiet`, `--no-color`, `--config`, `--state-dir`.

`monitoring live` prints one JSON object per sample (`"type": "sample"`) and rejects `--json`. Other commands use the usual Abstrax result envelope. `data.schema_version` is `1`.

`config` checks the new values before saving, then asks the running daemon to reload. Intervals must be whole seconds of at least 1s. Retention windows must increase (`raw < short < medium < long < maximum`). Each rollup width must be coarser than the one before it, and a multiple of that finer width.

A bad config makes `run` exit 78. The unit sets `RestartPreventExitStatus=78`, so systemd does not restart-loop. SIGTERM and SIGINT flush SQLite and exit. SIGHUP reloads the config file.

```bash
sudo abstrax monitoring config --host-interval=5s --max-storage=1GB
journalctl -u abstrax-monitoring -f
```

## Metrics

**Host.** CPU usage, user, system, iowait, and steal as a percentage of the machine (0–100). CPU usage is user, system, irq, softirq, and steal. Also cores, memory and swap in bytes and percent, load, uptime, and aggregate disk and network bytes per second. Loopback, bridge and veth-style interfaces, partitions, loop devices, and stacked disks are left out of the totals.

**Filesystems.** One series per real mount: device, type, total, used, available, usage, and inodes. `proc`, `sysfs`, `tmpfs`, `devtmpfs`, `overlay`, and `squashfs` are skipped. `statfs` runs off the sampler, one probe per mount.

**Processes.** PID, PPID, boot id, start time, name, executable, command line, user, CPU percent, RSS, memory percent, read and write bytes per second, threads, and state. Process CPU is the Linux figure and can exceed 100 on a machine with more than one core. `instance_id` is derived from boot id, PID, and starttime, so a recycled PID is a new series. Exited processes remain queryable until retention removes them.

## History and storage

History is one page: one `from`, one `to`, and one resolution for the host and every filesystem. `--max-points` defaults to 240 and cannot exceed 500. Pass `--cursor` from the previous page to continue. A cursor fails if those rows have since been deleted or rolled into a coarser tier. Missing samples stay missing.

Defaults:

| Tier | Width | Kept for |
|------|-------|----------|
| raw | sampling interval | 1 hour |
| short | 15s | 6 hours |
| medium | 1 minute | 24 hours |
| long | 5 minutes | 7 days |
| archive | 1 hour | 90 days |

Gauges keep a sum, a max, and a count. Rates keep byte deltas and duration, so the average is total bytes over total time, and the max is the highest rate seen in the bucket.

The storage ceiling defaults to `1GB` (1000000000 bytes) and takes priority over the time limits. The oldest rows are deleted, collection continues, and `storage status` reports both the configured retention and the oldest sample still on disk. `storage_constrained` is true when the database is at least 80% of the ceiling and the history on disk is shorter than the configured maximum.

SQLite uses WAL and `synchronous=FULL`, through `modernc.org/sqlite`, with a single connection.

## Development

```bash
go test -race ./...
go vet ./...
go build -o bin/abstrax-monitoring ./cmd/abstrax-monitoring
```
