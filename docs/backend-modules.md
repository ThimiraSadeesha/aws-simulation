# Backend module guide (Go)

How to add a service module to `backend/` without touching shared files. Read alongside
`docs/api.md` (the REST contract) and `docs/engine-protocol.md` (the Rust engine contract).

## Layout

| Layer | Location | Notes |
|---|---|---|
| Migration | `backend/migrations/NNN_<name>.sql` | Applied in lexical order inside a transaction. Use FKs (`ON DELETE …`), indexes, `CHECK`s. Timestamps are TEXT in `db.TimeFormat`. Never edit a migration owned by another module. |
| Repository | `backend/internal/repositories/<name>.go` | Declare an interface + SQLite impl. **Do not edit `repositories.go`'s `Store`**; instead expose a constructor `func NewXRepo(d *db.DB) XRepo`. Use `notFound(err)` and the `Querier` pattern for transactional writes. |
| Service | `backend/internal/services/<name>.go` | Business logic + validation. Embed `*services.Deps` (Store, Engine, Log, Pub, Logs). Return `apierr.*` errors. Use `RecordEvent` for user-visible events, `EngineErr` to map engine errors, `ValidateTags`, `withName`. |
| Routes | `backend/internal/api/routes_<name>.go` | Register via `func init() { RegisterModule("<name>", <order>, func(s *Server) {...}) }`. Use `s.op(pattern, iamAction, arnFn, handler)` for every authenticated route — it authenticates, authorizes via IAM (backend-enforced), audits mutations and maps errors. Use `paginate(items, parseList(r), allowedFilterFields...)` for lists. |
| Tests | `backend/tests/<name>_test.go` | Use `testutil.New(t)` (real SQLite + in-process fake engine `simtest`), `h.Signup(name)`, `c.Must(status, method, path, body)`, `c.IAMUser(name, policies...)` for access-key clients, `h.Eventually`. |

## Hooks available on `*api.Server` (set them inside your module func)

* `s.KindHandlers["<engine kind>"] = func(ctx, simulator.Event)` — receives engine `state_changed` / `fault_triggered` events for that kind.
* `s.Resyncers = append(s.Resyncers, func(ctx) error)` — re-register your live resources with the engine on startup / engine restart (`simulator.RegisterRequest{State: &persistedState}`); order matters: parents first (core runs first).
* `s.Workers = append(s.Workers, func(ctx))` — background loops; use `orchestration.Every(ctx, interval, fn)`. Must return when ctx is cancelled.
* `s.DashboardContrib = append(s.DashboardContrib, func(ctx, account, out map[string]any))` — fill dashboard summary fields you own (`buckets`, `objects`, `storage_bytes`, `load_balancers`, `autoscaling_groups`, `alarms`, `avg_cpu_percent`, `cpu_series`, `network_*`, `active_labs`).
* `s.Ext.Store("<key>", svc)` / `s.Ext.Load("<key>")` — share your service with other modules.
* `s.Internal(ctx, principal, method, path, body, headers)` — execute an API call in-process as a principal (same auth/IAM/audit path). `s.PrincipalForAccount(ctx, account)` gives the account root principal for background controllers.
* `s.Deps.Logs` — `services.LogSink` (CloudWatch Logs module replaces the no-op).
* `services.TerminationHooks` — called after an EC2 instance terminates (append in your module func).
* `services.ExternalResolvers`, `services.TopologyLBs` — DNS answers / topology contributions.
* `s.EC2`, `s.Net`, `s.IAM`, `s.Auth`, `s.Store`, `s.Engine`, `s.Cfg`, `s.Hub` (`Publish(account, msg)`).

## Rules

* Account isolation: every query is scoped by `account_id`; return 404 (not 403) for other accounts' resources.
* Validation happens in the service layer, never only in the frontend.
* Never execute shell commands or user code. Persist everything that must survive restarts in SQLite.
* Resource lifecycles with timing (pending → available …) go through the engine (`s.Engine.Register` / `Action`) — not `time.Sleep`.
* Keep `gofmt` clean; `go vet ./...` and `go test ./...` from `backend/` must pass.
