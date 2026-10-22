# Architecture — milestone 3

## Deployment and verification status

The collector image is a multi-stage Go build followed by a non-root Alpine runtime. Compose keeps MySQL and Redis private, publishes the collector only on loopback, and waits for dependency health checks before starting it. The collector's container health check calls `/readyz`, which requires both MySQL schema readiness and Redis connectivity. This topology is configured but has not been executed in this Windows environment because Docker is unavailable.

CI separates portable unit checks from a real MySQL/Redis integration job using disposable GitHub Actions service containers. Prometheus uses the official Go client with a private collector registry, an official `promhttp` handler, bounded metric labels, and a Compose scrape target. Scrapes bypass HTTP instrumentation to prevent self-observation loops.

## Boundaries

`cmd/agent` loads validated configuration, installs SIGINT/SIGTERM cancellation, and starts the agent. `internal/agent` owns scheduling and HTTP delivery behind small source/sink interfaces. `internal/metrics` owns the JSON schema, validation, pure procfs parsers, and Linux filesystem operations. `internal/config` has no global flag or environment mutation. `cmd/collector` owns HTTP server lifecycle, dependency startup checks, migrations, and worker cancellation. `internal/collector` handles routing, strict decoding, validation, and storage orchestration through interfaces in `internal/storage`. `internal/alerts` owns pure alert lifecycle evaluation; `internal/recovery` owns bounded cache repair with retry. MySQL and Redis packages own their connections, timeouts, and queries.

Linux system calls are confined to `collector_linux.go`. Portable parsers can be tested on other operating systems; `collector_other.go` reports unsupported collection. No background worker pool is necessary: one synchronous loop bounds resource use and avoids racing the previous CPU sample. The standard HTTP client and signal handling manage their own runtime work.

## Metric semantics

| Field | Source / semantics |
| --- | --- |
| `cpu.utilization_percent` | `100 × (delta total − delta idle) / delta total`, aggregate across all CPUs, range 0–100. Total sums user, nice, system, idle, iowait, irq, softirq, steal; idle includes iowait. Guest fields are excluded because user/nice already include them. |
| `cpu.iowait_percent` | `100 × delta iowait / delta total`, aggregate across all CPUs, range 0–100. It is optional so existing senders that do not emit it remain valid. |
| `memory.total_bytes` / `available_bytes` | `MemTotal` / `MemAvailable` from `/proc/meminfo`, kernel kB converted using 1024 bytes. Missing `MemAvailable` produces an error rather than an estimate. |
| `memory.used_percent` | `100 × (total − available) / total`; Linux available memory includes reclaimable memory, unlike free memory. |
| `load.one` / `five` / `fifteen` | Kernel load averages, not percentages and not normalized by CPU count. |
| Filesystem bytes | `statfs` block counts × filesystem block size. Free includes reserved blocks; available is space available to unprivileged users. |
| Filesystem `used_percent` | `100 × (blocks − free blocks) / blocks`. Reserved free space is not counted as used; this differs from some `df` capacity displays. |
| Disk operations | Completed read/write operation counters; merged requests are not added. |
| Disk bytes | Cumulative sectors read/written × 512, regardless of physical/logical sector size. No per-second rate is calculated. |

All valid diskstats rows are retained, including partitions, virtual devices, and loop devices. Do not sum them blindly: partitions and layered devices can count the same I/O multiple times. Counters reset on reboot or device replacement. Parsers accept newer diskstats trailing fields but only consume the baseline counters; malformed rows generate a partial error while preserving valid rows.

CPU requires two successful samples. The first sample, a non-advancing counter, or a counter reset omits CPU and reports why; the latest valid counters become the next baseline. A read/parse failure clears the baseline. The utilization window is the time between actual collections, which can exceed the configured interval during slow delivery.

`collected_at` is UTC at collection start. Reads are sequential and not atomic across metric families. Hostname, `runtime.GOOS`, and `runtime.GOARCH` supply host metadata. OS is the kernel platform (`linux`), not the distribution name/version. Filesystem collection defaults to `/` and uses configured paths, not automatic mount discovery. The agent normally needs no root privileges; inaccessible sources are reported individually.

## Delivery and scheduling

The first snapshot is collected immediately. A ticker controls subsequent collections. The loop completes collection and delivery before taking another tick, so missed ticks coalesce and snapshots never accumulate in an unbounded queue.

Each POST has `Content-Type: application/json` and a client timeout (5s by default). Any 2xx status is success. Network failures, HTTP 408, 429, and 5xx retry up to the configured bound. Other statuses, including redirects, fail immediately. Network failures are retried conservatively, including potentially permanent DNS/TLS failures, but never indefinitely. Redirects are disabled. Response bodies are neither logged nor retained; at most 4 KiB is drained for connection reuse.

Retries reuse the exact JSON snapshot and timestamp, waiting 500ms, 1s, 2s, 4s, then 8s as needed. Default retries are three additional attempts (four total); the maximum is five. No jitter or Retry-After support is present in milestone 1. Context cancellation interrupts HTTP requests and backoff waits. Linux file reads and `statfs` cannot be interrupted mid-system-call; a stalled remote filesystem can delay shutdown, so prefer local filesystem paths.

An exhausted delivery is logged and dropped; there is no spool or durable queue. The collector makes retries idempotent using `(node_id, collected_at)` at nanosecond precision. Distinct agents must have distinct stable node IDs. Redis availability represents accepted contact, not certainty that an underlying machine is running or its measurements are current.

## Wire contract and errors

`schema_version: 1`, `node_id`, OS, architecture, and timestamp describe a snapshot. CPU, memory, and load are optional objects; disks and filesystems contain only successful measurements. The optional `errors` map identifies unavailable families or filesystem paths. A missing field is unknown, not zero. Healthy snapshots omit the errors map. The collector accepts this existing schema; incompatible future changes require deliberate versioning.

## Collector data flow

```mermaid
sequenceDiagram
    participant A as Linux agent
    participant C as Collector
    participant M as MySQL
    participant R as Redis
    A->>C: POST v1 snapshot
    C->>C: Bound body, strict decode, validate, normalize UTC
    C->>M: Transaction: insert or compare hash; update host index
    M-->>C: Commit; latest durable snapshot and receipt time
    C->>R: Lua: monotonic latest state + absolute heartbeat deadline
    R-->>C: Updated
    C-->>A: 201 accepted or 200 duplicate
```

Conflicting content returns 409 before any cache update. Historical-write errors return 503 before Redis is touched. Cache errors after commit return 503 even though MySQL may already contain the record. A lost COMMIT response has the same uncertain outcome; clients must retry the identical identity and data. There is no cross-store transaction, rollback of committed history, or exactly-once network delivery claim.

## Historical persistence

The embedded `internal/storage/mysql/migrations/001_initial.sql` creates InnoDB tables. `snapshots` has `(host_id VARBINARY(128), collected_ns BIGINT)` as its primary key, a 32-byte SHA-256 digest, and full snapshot JSON. Using integer nanoseconds avoids MySQL DATETIME microsecond truncation. Validation limits timestamp range to fit signed nanoseconds. Bytewise host IDs are case-sensitive. Object key ordering and timestamp offsets are normalized by typed JSON encoding; arrays retain order.

`hosts` stores one row per established host with the greatest collection timestamp and greatest collector receipt time. Insert/deduplication, host-index update, and latest-state read happen within a transaction. Ingestion races serialize at unique keys and the host row; deadlocks or database timeouts return retryable failure. Parameters are bound for every data-bearing SQL query. Connection counts, lifetime, and operation/network deadlines are bounded.

Queries use the composite primary key, inclusive lower/exclusive upper time bounds, descending timestamps, and exclusive keyset cursors. Host listing uses a bytewise ascending ID cursor. Both have row limits and an approximately 8 MiB stored-payload page budget. Cursors can lead to one final empty page. Pagination does not promise a consistent point-in-time export under concurrent backfills. No automatic retention, partitioning, deletion API, or aggregation is implemented.

Migration runs under a database-specific advisory lock on one connection. `schema_migrations` records version 1 after the initial idempotent DDL completes. MySQL DDL commits implicitly: replay is supported, atomic DDL rollback is not. Unknown newer schema versions are rejected. The normal service can use a non-DDL account after a privileged `collector migrate` step; Compose opts into migration on startup.

## Latest state and heartbeat

`nodewatch:host:<id>:latest` is a non-expiring hash containing `snapshot`, `collected_ns`, and `seen_ns`. `nodewatch:host:<id>:heartbeat` is a string containing the accepted receipt timestamp with an expiry deadline of receipt time plus configured TTL. Both are updated by one Lua script. Fixed-width decimal nanosecond strings are compared lexically rather than converted to imprecise Lua floating-point numbers. Older cache completions cannot replace newer measurements or extend a newer receipt deadline. A completion delayed past its deadline cannot resurrect presence.

Identical retries and out-of-order reports refresh receipt time only after their historical identity is validated. A cache repair uses MySQL's actual latest snapshot, not necessarily the submitted older snapshot. Continuous replay can therefore keep heartbeat presence alive without producing fresh measurements. A current heartbeat whose latest collection time exceeds the configured freshness threshold is `stale`; cached state without a heartbeat is `offline`; missing cached state is `unknown`. Synchronized collector/Redis clocks are required for absolute expiry semantics. Redis Cluster is not supported by this multi-key script/key layout.

The durable MySQL host index supplies bounded listing; Redis status lookups are pipelined without key scans. MySQL supplies durable latest data, and a newer cached snapshot can supersede a concurrent earlier read. Reads across stores are eventually consistent, not atomic. Cached latest plus heartbeat means online; cached latest without heartbeat means offline; missing latest means unknown. Missing MySQL host identity means never reported (404). Redis errors return 503 instead of silently converting an outage into offline hosts. Historical queries remain usable during a Redis outage.

Redis loss does not erase the durable host catalog or history. A new valid report repairs a host's cache. The reconciliation worker also walks MySQL's ordered host catalog in bounded pages and writes latest state back to Redis. Per-host failures receive bounded exponential retry with jitter; a page failure is reported and retried on a later interval. It is not an outbox or a cross-store transaction. Redis AOF in Compose helps persistence but is not a substitute for MySQL durability or cache repair.

## Alerts

The alert worker reads one bounded host page per evaluation and retains a cursor so every known host is eventually visited. It evaluates `host_offline` (critical), `filesystem_usage`, and `cpu_iowait`; the latter is skipped for old snapshots without I/O-wait data. Threshold conditions create one opaque ID per rule, host, and target. A condition transitions from `pending` to `firing` only after it remains active for the configured duration. A later page that sees the condition clear transitions it to `resolved`. Alerts are in-memory, capped at 10,000 records, exposed over the alert API, and deliberately have no notification delivery or durable incident history.

## HTTP lifecycle and operational limits

The server bounds headers, request bodies, handler time, idle connections, and graceful shutdown. Storage requests inherit cancellation and have shorter dependency deadlines. The listener runs concurrently with signal handling so shutdown can drain requests. HTTP timeout may occur after a durable commit; retry semantics still apply. Liveness is independent of dependencies; readiness checks Redis and MySQL schema version. Startup requires both dependencies and the expected schema. Structured logs omit bodies, credentials, query strings, and raw driver errors.

The service does not authenticate requests or terminate TLS. Compose publishes only loopback and keeps database ports internal. Trusted deployment boundaries or an authenticated HTTPS reverse proxy are required before remote use. SQL/Redis transport encryption, rate limiting, retention, backup policy, global ingestion backpressure, durable notification delivery, and a UI remain unimplemented. Container execution and real database integration require verification on an environment that supports those services; emulator tests are not substitutes.
