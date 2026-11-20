# cloudsim-engine

The Rust simulation engine for **CloudSim**, a local AWS-style cloud training simulator
(an independent educational project, not an AWS product).

The engine is an internal HTTP service that only the Go backend talks to. It keeps the
*runtime* simulation state in memory — resource lifecycles, events, metrics, faults — and
offers stateless simulators for VPC reachability, load balancing, auto scaling and object
storage. SQLite (owned by the backend) is the source of truth; after an engine restart the
backend re-syncs every live resource with `PUT /v1/resources/{id}`.

The wire contract is [`docs/engine-protocol.md`](../docs/engine-protocol.md) (protocol v1).
The engine never executes shell commands or user-supplied code.

## Module overview

| Module | Responsibility |
|---|---|
| `config` | Env-var configuration; refuses to start without a non-empty `ENGINE_TOKEN`. |
| `api` | axum router, `X-Engine-Token` auth middleware, `X-Engine-Protocol: 1` response header, access log, `{"error":{code,message}}` mapping, lenient JSON extractor (all body errors → `422 invalid_request`). |
| `engine` | `Engine` handle (`Arc` + `parking_lot` locks, never held across `.await`): register/sync, actions, deregister, workload, ticks, quotas, faults, LB apply-load, stats. |
| `lifecycle` | Pure state machines: `apply_action(kind, state, action) -> Result<Transition>`, `initial(kind)`, `completion_for(kind, state)`. |
| `registry` | Resource store, reverse dependency index, per-owner quota usage, resource views. |
| `scheduler` | Delayed completions on tokio timers with token-based supersede/cancel. `ENGINE_TIME_SCALE=0` completes synchronously inside the request. |
| `events` | Fixed-capacity ring buffer with contiguous `seq` (O(1) cursor lookup) + `watch`-based long-poll. |
| `metrics` | Per-tick CPU/memory/network/disk samples from workload profiles (pure, seeded). |
| `network::cidr` | Hand-rolled IPv4 CIDR math and `/v1/network/cidr` ops. |
| `network::reachability` | Stateless reachability evaluation: SG egress → NACL out → routing (local/IGW/NAT, longest-prefix match) → NACL in → SG ingress → NACL return traffic. |
| `lb` | Request distribution (round robin, least outstanding requests, weighted random) + latency model. |
| `autoscaling` | Target-tracking / step policy decisions with hysteresis, cooldown, clamping, replacement list. |
| `storage` | S3-style lifecycle rule evaluation and a deterministic latency/throughput model. |
| `faults` | Fault store (`crash`, `degrade`, `start_failure`) with deterministic rolls. |
| `healthcheck` | `cloudsim-engine --healthcheck`: dependency-free probe of `/healthz` for container health checks. |

Determinism: every random decision derives its own ChaCha8 RNG from `ENGINE_SEED` plus
stable inputs (resource id, tick, fault id, probe…), so results do not depend on thread
scheduling or hash-map iteration order. Same seed + same inputs ⇒ same outputs.

## Configuration

| Var | Default | Meaning |
|---|---|---|
| `ENGINE_ADDR` | `0.0.0.0:7070` | listen address |
| `ENGINE_TOKEN` | — (required) | shared secret checked on every `/v1/*` request |
| `ENGINE_TIME_SCALE` | `1.0` | multiplies all transition delays; `0` = complete immediately |
| `ENGINE_SEED` | `42` | seed for all pseudo-random behaviour |
| `ENGINE_TICK_MS` | `1000` | metrics/fault tick interval |
| `ENGINE_EVENT_BUFFER` | `10000` | event ring-buffer capacity |
| `RUST_LOG` | `info` | log filter; logs are JSON lines on stdout |

The process exits with code 2 on invalid configuration and shuts down gracefully on
SIGTERM/SIGINT (in-flight requests drain, long-polls are released immediately).

## Run, test, bench

Rust is not required on the host; everything runs in Docker. Named volumes keep the
cargo registry and target dir warm between runs:

```sh
cd engine
RUN="docker run --rm -v $PWD:/work -v cloudsim-cargo-registry:/usr/local/cargo/registry \
     -v cloudsim-engine-target:/work/target -w /work rust:1.85-bookworm"

$RUN cargo test                       # unit + integration tests
$RUN cargo bench -- --warm-up-time 1 --measurement-time 3 --noplot
```

`cargo fmt --check` and `cargo clippy --all-targets -- -D warnings` need the `rustfmt` and
`clippy` components (`rustup component add rustfmt clippy`), which the stock image lacks.

Run the service locally:

```sh
docker build -f infrastructure/docker/Dockerfile.engine -t cloudsim-engine .   # from repo root
docker run --rm -e ENGINE_TOKEN=dev-secret -p 127.0.0.1:7070:7070 cloudsim-engine
curl -s localhost:7070/healthz
curl -s -H 'X-Engine-Token: dev-secret' localhost:7070/v1/stats
```

(In Docker Compose the engine is never published to the host network.)

### Tests

* Unit tests live next to the code (`lifecycle`, `registry`, `events`, `faults`, `metrics`,
  `network::*`, `lb`, `autoscaling`, `storage`, `config`, `util`).
* `tests/api.rs` drives the axum `Router` in-process (`tower::ServiceExt::oneshot`): auth,
  full instance lifecycle and 409s, timed transitions and supersede, restart recovery,
  dependency violations, quotas, event long-poll ordering and ring overflow, faults,
  workload/metrics, stats, storage endpoints.
* `tests/network.rs`: reachability scenarios and CIDR ops.
* `tests/simulations.rs`: LB distribution per algorithm and determinism, ASG
  scale-out/in/cooldown/clamp/replace, lifecycle rules.
* `tests/load.rs`: concurrency (32 writer tasks + readers over 800 instances, plus 300
  real-timer transitions racing with terminate) and a stress test (10k resources, 50k events).

## Benchmarks

Measured inside Docker on the developer's Mac (8 vCPU), `cargo bench -- --warm-up-time 1
--measurement-time 3` (criterion 0.5, release profile). Median of criterion's estimate.

| Benchmark | Median time | Throughput |
|---|---|---|
| `lifecycle/apply_action_instance_cycle` (4 pure transitions) | 27.8 ns | 144 M transitions/s |
| `engine/stop_start_cycle_time_scale_0` (2 actions + 2 completions + 4 events, locked) | 1.34 µs | 1.49 M actions/s |
| `events/ingest_10k_into_ring_10k` | 1.74 ms | 5.75 M events/s |
| `events/ingest_10k_via_bus` (mutex + watch notify) | 1.76 ms | 5.67 M events/s |
| `events_read/page_1000_from_full_ring` | 119 µs | 8.4 M events/s |
| `reachability_50_subnets/internet_to_public_ssh` | 22.6 µs | — |
| `reachability_50_subnets/same_vpc_cross_subnet` | 22.9 µs | — |
| `reachability_50_subnets/private_to_internet_via_nat` | 22.8 µs | — |
| `reachability_50_subnets/blocked_sg` | 22.1 µs | — |
| `lb_100k_requests_10_targets/round_robin` | 1.10 ms | 91 M requests/s |
| `lb_100k_requests_10_targets/least_outstanding_requests` | 2.04 ms | 49 M requests/s |
| `lb_100k_requests_10_targets/weighted_random` | 1.69 ms | 59 M requests/s |

The reachability topology is 2 VPCs × 25 subnets (half private behind a NAT), 200
endpoints, 20-rule security groups and 10-entry NACLs; each evaluation includes building
the lookup index from the request snapshot.

## Simplifications vs. real AWS

* **Lifecycles** are the small state machines from the protocol. DB stop/start/delete
  durations (unspecified in the protocol) are 3 s / 4 s / 3 s. LB and DB `delete` are
  accepted from any state except `deleting`/`deleted`; instance `terminate` from any state
  except `terminated` (re-terminating restarts the shutdown timer).
* **Reachability** is evaluated on a snapshot: no VPC peering, transit gateways, VPN,
  endpoints or prefix lists (routes whose destination is not a CIDR are ignored). Cross-VPC
  traffic is modelled as internet traffic between public IPs. Intra-subnet traffic skips
  NACLs (as in AWS). NACL return traffic is allowed only by a rule whose port range covers
  the whole ephemeral range 1024–65535. A subnet without a NACL gets an allow-all default;
  without a route table it uses the VPC's `main` table if supplied, else local-only. The
  literal `"internet"` is represented by 198.51.100.1. Endpoint states `running`,
  `available`, `in-use` and `active` count as up.
* **Metrics** are synthetic per-tick values; there is no CPU credit model, no EBS/ENI
  limits, and network/disk bytes are linear functions of CPU.
* **Load balancing** has no connection draining, stickiness, slow start or cross-zone
  settings; latency is an M/M/1-style `5 ms / (1 - utilisation)` with seeded jitter and a
  1 % tail; requests are never dropped unless no target is healthy.
* **Auto Scaling** evaluates one decision per call on average CPU of running instances;
  no warm pools, lifecycle hooks or instance refresh.
* **Storage** lifecycle uses whole elapsed days (no midnight-UTC rounding), coldest eligible
  transition wins, expiration beats transition. The latency model is first-byte + size /
  100 MB/s (`throughput_mbps` is effective megabits per second).
* **Faults** apply to instances only; `start_failure` is one-shot (deactivated after it
  fires but still listed until deleted); `degrade` and `crash` stay active until deleted or
  `duration_ms` elapses.
* **Quotas** are global per-owner limits; they are not persisted across engine restarts.
