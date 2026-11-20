# CloudSim Engine Protocol — v1

The Rust simulation engine (`engine/`) is an internal service. Only the Go backend talks to it.
It is **never** published to the host network in Docker Compose.

* Transport: HTTP/1.1, JSON bodies (`Content-Type: application/json`).
* Base path: `/v1`. The protocol version is also returned in every response header `X-Engine-Protocol: 1`.
* Auth: every `/v1/*` request must carry `X-Engine-Token: <ENGINE_TOKEN>`. Missing/wrong → `401`.
  `/healthz` is unauthenticated (for container health checks).
* Errors: non-2xx with body `{"error": {"code": "<snake_case>", "message": "<text>"}}`.
  Codes used: `unauthorized`, `not_found`, `invalid_request`, `invalid_transition`,
  `dependency_violation`, `quota_exceeded`, `conflict`.
* The engine holds **runtime** simulation state in memory. SQLite (owned by the Go backend) is the
  source of truth. After an engine restart the backend re-syncs every live resource with
  `PUT /v1/resources/{id}`. All registration calls are therefore idempotent.
* The engine never executes user-supplied code or shell commands.

Configuration (env vars):

| Var | Default | Meaning |
|---|---|---|
| `ENGINE_ADDR` | `0.0.0.0:7070` | listen address |
| `ENGINE_TOKEN` | (required, non-empty) | shared secret |
| `ENGINE_TIME_SCALE` | `1.0` | multiplies all transition delays. `0` = transitions complete immediately (used by tests) |
| `ENGINE_SEED` | `42` | seed for all pseudo-random behaviour (metrics noise, fault injection, LB weighted random) — same seed + same inputs ⇒ same outputs |
| `ENGINE_TICK_MS` | `1000` | metrics simulation tick |
| `ENGINE_EVENT_BUFFER` | `10000` | event ring-buffer capacity |
| `RUST_LOG` | `info` | log level; logs are JSON lines on stdout |

---

## 1. Health & stats

`GET /healthz` → `200 {"status":"ok","protocol":1,"version":"<crate version>"}`

`GET /v1/stats` →
```json
{"uptime_seconds": 12, "resources": {"instance": 3, "nat_gateway": 1}, "resources_total": 4,
 "events_emitted": 120, "event_seq": 120, "pending_transitions": 1, "active_faults": 0,
 "ticks": 12, "time_scale": 1.0, "seed": 42}
```

## 2. Resources & lifecycle state machines

Resource kinds and their state machines (`→` = engine-driven completion after a delay):

| kind | initial | actions → transitional → final |
|---|---|---|
| `instance` | `pending` → `running` (boot 3000 ms) | `stop`: running→`stopping`→`stopped` (2000 ms); `start`: stopped→`pending`→`running` (3000 ms); `reboot`: running→`rebooting`→`running` (2000 ms); `terminate`: any except terminated→`shutting-down`→`terminated` (2000 ms) |
| `nat_gateway` | `pending` → `available` (2000 ms) | `delete`: available/pending→`deleting`→`deleted` (1500 ms) |
| `load_balancer` | `provisioning` → `active` (2000 ms) | `delete`: →`deleting`→`deleted` (1000 ms) |
| `db_instance` | `creating` → `available` (4000 ms) | `stop`: available→`stopping`→`stopped`; `start`: stopped→`starting`→`available`; `delete`: →`deleting`→`deleted` |
| any other kind (`vpc`, `subnet`, `security_group`, `bucket`, `route_table`, `internet_gateway`, `network_acl`, `network_interface`, `target_group`, `autoscaling_group`, `function`, `queue`, `topic`, …) | `available` immediately | `delete`: →`deleted` immediately |

Invalid action for current state → `409 invalid_transition`. Delays are multiplied by `ENGINE_TIME_SCALE`.
A pending transition for a resource is cancelled/superseded when a new valid action arrives
(e.g. `terminate` while `pending`).

### Register / sync
`PUT /v1/resources/{id}`
```json
{"kind":"instance","owner":"123456789012","state":null,
 "depends_on":["subnet-0abc"],"attributes":{"instance_type":"t3.micro","vcpu":2,"memory_mib":1024,"workload":"steady"}}
```
* `state: null` → resource starts in the kind's initial state and its initial transition is scheduled.
* `state: "<x>"` → resource is restored in exactly that state (restart recovery). If `x` is transitional
  (`pending`, `stopping`, …) the engine re-schedules the completion.
* Idempotent: re-PUT of an existing id updates `depends_on`/`attributes`/`state` (if given) and returns `200`; new → `201`.
* Quotas are checked on create only (see §6). `depends_on` ids must already be registered, else `422 invalid_request`.

Response (`200`/`201`): the resource view:
```json
{"id":"i-0abc","kind":"instance","owner":"123456789012","state":"pending","depends_on":["subnet-0abc"],
 "dependents":[],"attributes":{...},"workload":{"profile":"steady","cpu_target":null},"version":3,
 "updated_at":"2026-11-15T10:00:00.000Z"}
```

`GET /v1/resources/{id}` → resource view or `404`.
`GET /v1/resources?kind=instance&owner=...` → `{"items":[view…]}`.

### Actions
`POST /v1/resources/{id}/actions` `{"action":"stop","reason":"User initiated"}` → `202` resource view (now in the transitional state).

### Delete / deregister
`DELETE /v1/resources/{id}` → `204`. If any registered, non-deleted resource lists `{id}` in its `depends_on`
→ `409 dependency_violation` with `message` naming the dependents. (For kinds with a lifecycle, the backend
first drives them to a terminal state via actions, then deregisters.)

## 3. Events

Every state change, fault, quota rejection and scaling decision emits an event into a ring buffer with a
monotonic `seq` (starts at 1).

`GET /v1/events?after=<seq>&limit=<n≤1000>&wait_ms=<0..30000>`
* Returns events with `seq > after`. If none and `wait_ms>0`, long-polls until one arrives or timeout.
```json
{"events":[{"seq":5,"ts":"2026-11-15T10:00:03.001Z","resource_id":"i-0abc","kind":"instance",
  "type":"state_changed","from":"pending","to":"running","reason":"Instance boot completed","owner":"123456789012"}],
 "next":5, "oldest_available":1}
```
Event `type`s: `registered`, `state_changed`, `deregistered`, `fault_injected`, `fault_triggered`, `fault_cleared`, `quota_rejected`.
If `after < oldest_available-1` the client missed events; it should re-sync via `GET /v1/resources`.

## 4. Workload & metrics simulation

Each tick, for every resource of kind `instance` in state `running`, the engine computes metrics from its
workload profile with seeded noise:

| profile | CPU % behaviour |
|---|---|
| `idle` | ~2–5 % |
| `steady` (default) | ~20–35 % |
| `spiky` | baseline 15 %, periodic bursts to 80–95 % |
| `stress` | 90–100 % |
| `custom` | `cpu_target` ± 3 % |

Additional load: `extra_load` requests/s routed via LB simulation raise CPU proportionally
(`+ requests_per_sec * 0.5 %`, capped at 100).
Memory % = function of profile (idle≈10, steady≈40, spiky≈45, stress≈85) ± noise.
Network in/out bytes and disk read/write bytes per tick are derived from CPU.
Non-running instances produce no datapoints.

`PUT /v1/resources/{id}/workload` `{"profile":"stress","cpu_target":null,"extra_load_rps":0}` → resource view.

`GET /v1/metrics/snapshot` → latest tick for all running instances:
```json
{"tick":120,"ts":"...","samples":[{"resource_id":"i-0abc","owner":"123456789012",
  "cpu_percent":27.3,"memory_percent":41.0,"network_in_bytes":12000,"network_out_bytes":8000,
  "disk_read_bytes":4096,"disk_write_bytes":2048}]}
```

## 5. Network simulation

`POST /v1/network/reachability` — stateless; the backend sends the relevant topology snapshot.
```json
{"topology":{
  "vpcs":[{"id":"vpc-1","cidr":"10.0.0.0/16"}],
  "subnets":[{"id":"subnet-1","vpc_id":"vpc-1","cidr":"10.0.1.0/24","route_table_id":"rtb-1","network_acl_id":"acl-1"}],
  "route_tables":[{"id":"rtb-1","vpc_id":"vpc-1","routes":[{"destination":"10.0.0.0/16","target":"local"},{"destination":"0.0.0.0/0","target":"igw-1"}]}],
  "internet_gateways":[{"id":"igw-1","vpc_id":"vpc-1"}],
  "nat_gateways":[{"id":"nat-1","subnet_id":"subnet-1","state":"available","public_ip":"203.0.113.10"}],
  "security_groups":[{"id":"sg-1","vpc_id":"vpc-1",
     "ingress":[{"protocol":"tcp","from_port":22,"to_port":22,"cidr":"0.0.0.0/0","source_sg":null}],
     "egress":[{"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","source_sg":null}]}],
  "network_acls":[{"id":"acl-1","vpc_id":"vpc-1","entries":[
     {"rule_number":100,"egress":false,"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","action":"allow"},
     {"rule_number":100,"egress":true,"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","action":"allow"}]}],
  "endpoints":[{"id":"i-1","subnet_id":"subnet-1","private_ip":"10.0.1.10","public_ip":"54.0.0.1","security_group_ids":["sg-1"],"state":"running"}]
 },
 "probe":{"source":"internet","destination":"i-1","protocol":"tcp","port":22}}
```
* `source`/`destination`: an endpoint id, `"internet"`, or an IPv4 address (matched against endpoints by private/public IP).
* `protocol`: `tcp` | `udp` | `icmp` | `-1` (all). Rule protocol `-1` matches everything.
* Evaluation order modeled (documented simplification of real VPC behaviour):
  1. Source endpoint must be `running`. Destination endpoint must be `running`.
  2. **Egress** from source: source SG egress rules (stateful, any SG matching allows), source subnet NACL outbound (stateless, lowest rule number wins, implicit deny `*`).
  3. **Routing**: from the source subnet's route table, longest-prefix match on destination IP. Same-VPC → `local`.
     To internet: needs `0.0.0.0/0`→`igw-*` attached to the VPC **and** source has a public IP; or →`nat-*` that is `available` and whose subnet itself routes to an IGW (outbound only).
     From internet: destination needs a public IP and its subnet route table must have a route to an IGW attached to the VPC.
  4. **Ingress** at destination: destination subnet NACL inbound, destination SG ingress (`cidr` match on source IP or `source_sg` membership).
  5. NACL return traffic is checked for ephemeral ports 1024–65535 on the reverse direction (stateless). SGs are stateful.
* Response:
```json
{"reachable":false,
 "hops":[{"component":"sg-1","type":"security_group","direction":"egress","result":"allow","detail":"rule 0.0.0.0/0 all"},
         {"component":"rtb-1","type":"route_table","result":"allow","detail":"0.0.0.0/0 -> igw-1"}],
 "blocked_by":{"component":"acl-1","type":"network_acl","detail":"inbound rule * deny"},
 "explanation":"Traffic blocked by network ACL acl-1 inbound (implicit deny).",
 "simulated_latency_ms":1.8}
```
Latency model: same subnet 0.2 ms, same VPC 0.5 ms, via IGW 15 ms, via NAT 18 ms, plus seeded jitter ≤10 %.

`POST /v1/network/cidr` `{"op":"validate|contains|overlaps|subnets","cidr":"10.0.0.0/16","other":"10.0.1.0/24","new_prefix":24}`
→ `{"valid":true,"network":"10.0.0.0","prefix":16,"first_host":"10.0.0.1","last_host":"10.0.255.254","size":65536,"contains":true,"overlaps":true,"subnets":["10.0.0.0/24", "... (max 256)"]}` (only fields relevant to op).

## 6. Quotas

Default per-owner quotas (configurable via `PUT /v1/quotas`): `instance`: 20, `vpc`: 5, `subnet`: 50,
`bucket`: 100, `nat_gateway`: 5, `load_balancer`: 10, `autoscaling_group`: 10, `security_group`: 100, others: 1000.
Only non-deleted/non-terminated resources count.

`GET /v1/quotas?owner=…` → `{"limits":{"instance":20,…},"usage":{"instance":3,…}}`
`PUT /v1/quotas` `{"limits":{"instance":5}}` → updated limits.
Exceeded on create → `429 quota_exceeded` and a `quota_rejected` event.

## 7. Load balancing simulation

`POST /v1/lb/simulate`
```json
{"algorithm":"round_robin|least_outstanding_requests|weighted_random","requests":1000,
 "targets":[{"id":"i-1","healthy":true,"weight":1,"capacity_rps":100}],
 "seed":7, "apply_load":true, "duration_s":60}
```
→ distribution over **healthy** targets only:
```json
{"total":1000,"served":1000,"dropped":0,
 "per_target":[{"id":"i-1","requests":500,"percent":50.0,"avg_latency_ms":12.1,"p99_latency_ms":30.2}],
 "avg_latency_ms":12.3}
```
No healthy targets → all requests `dropped`. If `apply_load` is true the engine sets each target's
`extra_load_rps = requests/duration_s` share on the corresponding registered instance (raises its CPU).

`POST /v1/lb/health` `{"targets":[{"id":"i-1","port":80}],"healthy_threshold":2,"unhealthy_threshold":2}`
→ `{"targets":[{"id":"i-1","state":"healthy|unhealthy|unused","reason":"..."}]}` — uses registered instance
state (`running` ⇒ healthy unless an active `degrade`/`crash` fault; not running ⇒ `unused`/unhealthy; unknown id ⇒ unhealthy "Target.NotRegistered").

## 8. Auto Scaling decisions

`POST /v1/autoscaling/evaluate`
```json
{"group":{"name":"web-asg","min":1,"max":4,"desired":2,"cooldown_s":60,"seconds_since_last_activity":120},
 "policies":[{"name":"cpu50","type":"target_tracking","target_value":50.0},
             {"name":"step-up","type":"step","adjustment":1,"threshold":80.0,"comparison":">="}],
 "instances":[{"id":"i-1","state":"running","cpu_percent":91.0,"healthy":true}]}
```
→ `{"desired":3,"change":1,"reason":"Policy cpu50: average CPU 91.0% > target 50.0%, scaling to 3 (capped by max 4)","replace":["i-x"]}`
* Unhealthy or non-running (excluding pending) instances are listed in `replace`.
* Target tracking: `desired = ceil(running * avg_cpu / target)`, clamped to [min,max]; scale-in only if avg < 0.8·target (hysteresis).
* Within cooldown → no change (`reason` says so), but `replace` still returned.
* desired always clamped to [min,max].

## 9. Storage simulation

`POST /v1/storage/lifecycle/evaluate`
```json
{"now":"2026-11-15T00:00:00Z",
 "rules":[{"id":"expire-logs","status":"Enabled","prefix":"logs/","expiration_days":30,
           "noncurrent_expiration_days":7,"transitions":[{"days":30,"storage_class":"GLACIER"}],
           "abort_incomplete_multipart_days":7}],
 "objects":[{"key":"logs/a.txt","version_id":"v1","is_latest":true,"last_modified":"...","size":10,"storage_class":"STANDARD","noncurrent_since":null}],
 "multipart_uploads":[{"upload_id":"u1","key":"x","initiated":"..."}]}
```
→ `{"actions":[{"type":"expire|expire_noncurrent|transition|abort_multipart","key":"…","version_id":"…","upload_id":null,"storage_class":"GLACIER","rule_id":"expire-logs"}]}`

`POST /v1/storage/simulate` `{"op":"put|get","size_bytes":1048576,"storage_class":"STANDARD"}`
→ `{"latency_ms":…, "throughput_mbps":…}` (deterministic model: STANDARD 10 ms first byte + size/100MBps, GLACIER first-byte 3 600 000 ms for get).

## 10. Fault injection

`POST /v1/faults` `{"target":"i-0abc" | null,"kind":"instance" | null,"type":"crash|degrade|start_failure","probability":1.0,"duration_ms":null}` → `201 {"id":"flt-1",…}`
* `crash`: a running target instance moves `running`→`stopped` with reason `Server.InternalError: simulated host failure` (evaluated each tick with `probability`).
* `degrade`: instance stays running but LB health reports unhealthy (`reason: "Target.FailedHealthChecks"`).
* `start_failure`: next `start`/boot ends in `stopped` with reason `Server.InsufficientInstanceCapacity`.
`GET /v1/faults` → `{"items":[…]}`, `DELETE /v1/faults/{id}` → `204` (emits `fault_cleared`).
