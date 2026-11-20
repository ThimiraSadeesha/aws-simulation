# CloudSim — local AWS-style cloud training simulator

CloudSim is a self-hosted platform for learning cloud concepts hands-on: launch instances, build
VPCs, write IAM policies, store objects, put load balancers and auto scaling in front of apps,
watch metrics and alarms, and work through guided labs that check real simulator state.

> **CloudSim is an independent educational project. It is not affiliated with or endorsed by
> Amazon Web Services.** Nothing here talks to real AWS. Every resource is simulated locally
> and costs nothing. Service names such as "EC2" and "S3" are used only as generic labels for
> the concepts being taught.

| Component | Tech | Directory |
|---|---|---|
| Management console | Next.js, React, TypeScript, Tailwind, shadcn/ui, TanStack Query, Recharts, Monaco, xterm.js | `frontend/` |
| API backend | Go: REST + WebSocket, IAM policy engine, SQLite, background controllers | `backend/` |
| Simulation engine | Rust: tokio/axum state machines, event bus, network/LB/ASG/storage simulation | `engine/` |
| CLI | Go, AWS-CLI-like `cloudsim` command | `cli/` |
| Training labs | Declarative lab definitions, 3 levels | `training-labs/` |

How the pieces fit together is described in [docs/architecture.md](docs/architecture.md).

## Quick start

Requirements: Docker with Compose v2. You don't need Go, Rust or Node to run the platform.

```bash
cp .env.example .env
```

Set `ENGINE_TOKEN` in `.env` to a long random value, for example the output of `openssl rand -hex 32`.
Running `make .env` instead does both steps for you. Then start the stack:

```bash
docker compose up --build
```

1. Open **http://localhost:3000**.
2. Register an account. The first user becomes the platform administrator.
3. The dashboard opens. Go to **Training Labs** and start **Launch your first instance**.

Data is kept in the `cloudsim-data` Docker volume, which holds the SQLite database, object
storage and backups. It survives `docker compose down` and restarts. To wipe everything:

```bash
docker compose down -v
```

### Ports and exposure

| Service | Default | Exposure |
|---|---|---|
| Console | `127.0.0.1:3000` | Proxies `/api/*` and WebSockets to the backend |
| Backend API | `127.0.0.1:8080` | Published on loopback only, for the CLI |
| Engine | — | **Never published**; reachable only on the internal Docker network |

To expose the platform on a network, set `CONSOLE_BIND=0.0.0.0` (and `SECURE_COOKIES=true`
behind HTTPS). All settings are listed in [.env.example](.env.example).

## Training labs

The labs are under **Training Labs** in the console. Each lab has step-by-step instructions,
hints, a split view with an in-browser terminal, and a **Validate step** button. Validation
queries the real state of your simulated account; nothing is pre-scripted. **Reset lab**
deletes the lab's resources and clears your progress.

| Level | Labs |
|---|---|
| Beginner | Launch your first instance · Create a storage bucket · Configure IAM permissions |
| Intermediate | VPC with public and private subnets · Security groups · App behind a load balancer · Auto Scaling · Monitoring alarms |
| Advanced | Decouple with queues and notifications · Three-tier architecture |

How labs work and how to write your own: [docs/labs.md](docs/labs.md).

## The `cloudsim` CLI

Build the CLI:

```bash
make cli
```

Configure it with an access key. In the console, open **IAM → Users**, create a user and an
access key, then run:

```bash
./bin/cloudsim configure
```

Example commands:

```bash
./bin/cloudsim ec2 run-instances --image-id ami-0c1a2b3c4d5e6f701 --instance-type t3.micro --name web
```

```bash
./bin/cloudsim ec2 describe-instances --output table
```

```bash
./bin/cloudsim s3 mb local://my-bucket
```

```bash
./bin/cloudsim s3 cp ./notes.txt local://my-bucket/notes.txt
```

```bash
./bin/cloudsim iam list-users
```

```bash
./bin/cloudsim vpc describe-vpcs
```

S3 URIs use the `local://` scheme so they can't be confused with real S3 locations. The default
endpoint is `http://localhost:8080`; change it with `--endpoint-url` or `CLOUDSIM_ENDPOINT`.
The same commands work in the lab terminal in the browser. They run in-process; no shell is
ever spawned.

## Services

| Service | Status |
|---|---|
| EC2 (instances, AMIs, types, key pairs, user data, console output, metadata, tags) | Implemented |
| VPC (VPCs, subnets, route tables, IGW, NAT, security groups, NACLs, ENIs, DNS, reachability, topology) | Implemented |
| IAM / STS (users, groups, roles, managed and inline policies, access keys, policy simulator, assume role) | Implemented |
| S3 (buckets, objects, versioning, policies, ACLs, lifecycle, multipart, stats) | Implemented |
| Elastic Load Balancing, Auto Scaling, CloudWatch (metrics, alarms, logs) | Implemented |
| SQS, SNS, Secrets Manager, DynamoDB, Route 53 | Implemented (simplified) |
| Lambda, RDS, ECS/ECR/EKS, CloudFormation | Not yet; the API returns `501 NotImplemented` |

What each service simulates, and where it differs from real AWS: [docs/services.md](docs/services.md).
REST API reference: [docs/api.md](docs/api.md). Engine protocol: [docs/engine-protocol.md](docs/engine-protocol.md).

## Development

`make help` lists all targets.

| Target | What it does |
|---|---|
| `make test` | Backend (`go test -race`), CLI, engine (`cargo test`, in Docker) and frontend (lint, typecheck, Vitest) |
| `make system-test` | Brings up the real Compose stack and checks health, every core service, IAM enforcement, a lab, persistence across backend restart, recovery after engine restart, and cleanup |
| `make e2e` | Playwright tests against a running stack |
| `make bench` / `make bench-backend` | Engine criterion benchmarks; Go benchmarks plus an HTTP load test |

To run the parts without Docker:

1. Start the engine:
   `cd engine && ENGINE_TOKEN=dev cargo run`
2. Start the backend:
   `cd backend && ENGINE_TOKEN=dev LABS_DIR=../training-labs go run ./cmd/server`
3. Start the console:
   `cd frontend && npm install && BACKEND_INTERNAL_URL=http://localhost:8080 npm run dev`

Adding a backend service module is covered in [docs/backend-modules.md](docs/backend-modules.md).

## Benchmarks

Measured results are in [docs/benchmarks.md](docs/benchmarks.md), including the engine's
criterion numbers and the HTTP load test. No figure in this project is claimed without a
measurement behind it.

## Security notes

- Passwords are hashed with bcrypt. Session tokens and access-key secrets are stored only as
  hashes. Key-pair private keys are shown once and never stored. Secrets are AES-GCM encrypted
  at rest.
- Cookie sessions use CSRF tokens. WebSockets check the origin and require a CSRF token in the
  first frame. Logins and API requests are rate-limited. Every mutation and every denied request
  goes to the audit log.
- IAM is enforced in the backend on every request; the frontend never makes access decisions.
- User-data scripts are interpreted by a sandbox that only knows a whitelist of commands. User
  input never reaches a shell.
- Containers run as non-root with dropped capabilities. The engine and backend have read-only
  root filesystems.
