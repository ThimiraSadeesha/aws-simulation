# Architecture

CloudSim is an independent, educational, AWS-*style* cloud simulator. Nothing in it talks to real
AWS; every resource is simulated locally and persisted in SQLite.

```
 Browser ──► Next.js console (frontend, :3000)
                │  same-origin /api/* proxy (+ WebSockets)
                ▼
            Go backend (:8080) ──────────────► SQLite (/data/cloudsim.db, WAL)
            REST + WebSocket                   object blobs (/data/objects)
            auth · IAM policy engine           backups (/data/backups)
            services · orchestration
                │  protocol v1 (JSON/HTTP, shared token, internal network only)
                ▼
            Rust simulation engine (:7070, never published)
            lifecycle state machines · event bus · metrics/workloads
            network reachability · LB / ASG / storage simulation · faults · quotas

 cloudsim CLI ──► Go backend (Authorization: CloudSim <key>:<secret>)
```

## Responsibilities

| Component | Owns | Does not own |
|---|---|---|
| **Go backend** (`backend/`) | API routing, authentication (sessions, CSRF, access keys, STS), IAM policy evaluation on every request, validation, business rules, SQLite persistence (source of truth), audit log, background controllers (event pump, reconciler, metrics collector, alarm evaluator, ASG controller, lifecycle rules, queue retention), WebSocket fan-out, in-browser terminal | Timing of lifecycle transitions, simulation maths |
| **Rust engine** (`engine/`) | Runtime resource registry, state machines with timed transitions, dependency graph, quotas, event ring buffer (long-poll), workload/metrics generation, network reachability evaluation, LB distribution & health, ASG decisions, S3 lifecycle evaluation, storage latency model, fault injection | Persistence, users, HTTP auth beyond the shared token |
| **Next.js console** (`frontend/`) | AWS-style management UI, forms with client-side validation (mirrors server rules), charts, topology diagram, lab runner with xterm terminal | Any business logic or authorization decisions |
| **CLI** (`cli/`) | AWS-CLI-like command grammar (`cli/pkg/commands`), profiles, output formats | — (the same command package powers the web terminal in-process) |

## Key flows

**Launching an instance.** `POST /ec2/instances` → IAM check (`ec2:RunInstances`) → validation
(AMI, type, subnet, SGs, key pair) → sandboxed user-data interpretation (whitelisted commands
only — see below) → transaction: allocate IP inside the write lock, insert instance + primary ENI
+ tags → `PUT /v1/resources/{id}` on the engine (quota check, `pending`) → response. The engine
completes `pending → running` after the boot delay and emits an event; the backend's event pump
(long-poll) applies it to SQLite and pushes it to WebSocket subscribers.

**Restart recovery.** SQLite is authoritative. On startup, whenever the engine's event sequence
resets, and when the reconciler (every 30 s) finds a resource the engine doesn't know, the backend
re-registers every live resource in dependency order with its persisted state; transitional states
(`pending`, `stopping`, …) resume their timers. Verified by `backend/tests` (fake engine killed) and
`scripts/system_test.py` (real engine container restarted).

**Authorization.** Console sessions act as the account root user (full access to their own account,
like the AWS root user). Access keys belong to IAM users or STS sessions; every route declares its
IAM action + resource ARN and the policy engine evaluates explicit Deny > Allow > implicit deny over
attached, inline and group policies (plus resource policies such as S3 bucket policies). Platform
admin endpoints additionally require the platform `admin` role.

## Security model

* No user input ever reaches a shell. User-data scripts are *interpreted* by a small sandbox
  (`backend/internal/userdata`) that models packages, services, files and env vars; unsupported
  commands are reported and skipped. The terminal runs the `cloudsim` command grammar in-process.
* The engine port is not published; the backend authenticates to it with `ENGINE_TOKEN`.
* Passwords: bcrypt. Session tokens and access-key secrets are stored only as SHA-256 hashes;
  key-pair private keys are returned once and never stored; secrets are AES-GCM encrypted at rest.
* CSRF tokens for cookie sessions (`SameSite=Strict` cookies too), WebSocket first-frame CSRF check,
  origin checks, per-principal and login rate limiting, request size limits, security headers,
  audit log of all mutations and all denials.
* Containers: non-root users, read-only root filesystems (engine, backend), dropped capabilities,
  `no-new-privileges`, memory/CPU limits, ports bound to loopback by default.

## Simulated vs real behaviour

See `docs/services.md` for each service's scope and simplifications. Highlights: instances are
state machines with synthetic metrics, not VMs; "public" IPs come from 198.18.0.0/15 (benchmark
range); networking is evaluated analytically (no packets leave the host); S3 has no SigV4 /
S3-wire compatibility — it uses the CloudSim REST API.
