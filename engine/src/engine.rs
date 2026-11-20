//! The engine: shared simulation state and every operation the HTTP API exposes.
//!
//! Locking: `reg` (RwLock) → `faults` (Mutex) → event log (Mutex) is the only lock order
//! used. All locks are parking_lot locks that are never held across an `.await`.

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};

use parking_lot::{Mutex, RwLock};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use tokio::sync::watch;

use crate::autoscaling;
use crate::config::Config;
use crate::error::{EngineError, EngineResult};
use crate::events::{EventBus, EventType, EventsPage, NewEvent};
use crate::faults::{Fault, FaultRequest, FaultStore, FaultType};
use crate::lb;
use crate::lifecycle::{self, Completion};
use crate::metrics::{compute_sample, MetricsSnapshot};
use crate::registry::{Profile, Registry, Resource, ResourceView, Workload};
use crate::scheduler;
use crate::util::{fmt_ts, now};

pub const MAX_ID_LEN: usize = 256;

#[derive(Debug, Clone, Deserialize)]
pub struct RegisterRequest {
    pub kind: String,
    #[serde(default)]
    pub owner: String,
    #[serde(default)]
    pub state: Option<String>,
    #[serde(default)]
    pub depends_on: Option<Vec<String>>,
    #[serde(default)]
    pub attributes: Option<Value>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct ActionRequest {
    pub action: String,
    #[serde(default)]
    pub reason: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WorkloadRequest {
    #[serde(default)]
    pub profile: Option<String>,
    #[serde(default)]
    pub cpu_target: Option<f64>,
    #[serde(default)]
    pub extra_load_rps: Option<f64>,
}

#[derive(Debug, Clone, Serialize)]
pub struct Stats {
    pub uptime_seconds: u64,
    pub resources: BTreeMap<String, u64>,
    pub resources_total: u64,
    pub events_emitted: u64,
    pub event_seq: u64,
    pub pending_transitions: u64,
    pub active_faults: u64,
    pub ticks: u64,
    pub time_scale: f64,
    pub seed: u64,
}

#[derive(Debug, Clone, Serialize)]
pub struct QuotaView {
    pub limits: BTreeMap<String, u32>,
    pub usage: BTreeMap<String, u32>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct HealthTarget {
    pub id: String,
    #[serde(default)]
    pub port: Option<u16>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct HealthRequest {
    #[serde(default)]
    pub targets: Vec<HealthTarget>,
    #[serde(default)]
    pub healthy_threshold: Option<u32>,
    #[serde(default)]
    pub unhealthy_threshold: Option<u32>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct HealthStatus {
    pub id: String,
    pub state: String,
    pub reason: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
pub struct HealthResponse {
    pub targets: Vec<HealthStatus>,
}

pub struct Inner {
    pub(crate) cfg: Config,
    started: Instant,
    pub(crate) reg: RwLock<Registry>,
    pub(crate) events: EventBus,
    pub(crate) faults: Mutex<FaultStore>,
    metrics: RwLock<MetricsSnapshot>,
    ticks: AtomicU64,
    next_token: AtomicU64,
    shutdown: watch::Sender<bool>,
}

/// Cheap-to-clone handle to the shared engine state.
#[derive(Clone)]
pub struct Engine {
    pub(crate) inner: Arc<Inner>,
}

fn validate_id(id: &str) -> EngineResult<()> {
    if id.is_empty() || id.len() > MAX_ID_LEN || id.chars().any(char::is_control) {
        return Err(EngineError::InvalidRequest(format!(
            "invalid resource id {id:?} (must be 1-{MAX_ID_LEN} printable characters)"
        )));
    }
    Ok(())
}

impl Engine {
    pub fn new(cfg: Config) -> Engine {
        let (shutdown, _) = watch::channel(false);
        let events = EventBus::new(cfg.event_buffer);
        Engine {
            inner: Arc::new(Inner {
                cfg,
                started: Instant::now(),
                reg: RwLock::new(Registry::new()),
                events,
                faults: Mutex::new(FaultStore::new()),
                metrics: RwLock::new(MetricsSnapshot {
                    tick: 0,
                    ts: fmt_ts(&now()),
                    samples: Vec::new(),
                }),
                ticks: AtomicU64::new(0),
                next_token: AtomicU64::new(1),
                shutdown,
            }),
        }
    }

    pub fn config(&self) -> &Config {
        &self.inner.cfg
    }

    pub(crate) fn from_inner(inner: Arc<Inner>) -> Engine {
        Engine { inner }
    }

    fn emit(&self, ev: NewEvent) -> u64 {
        self.inner.events.emit(ev)
    }

    pub(crate) fn next_token(&self) -> u64 {
        self.inner.next_token.fetch_add(1, Ordering::Relaxed)
    }

    // ---- resources ------------------------------------------------------------

    /// `PUT /v1/resources/{id}`. Returns the view and whether the resource was created.
    pub fn register(&self, id: &str, req: RegisterRequest) -> EngineResult<(ResourceView, bool)> {
        validate_id(id)?;
        let kind = req.kind.trim().to_string();
        if kind.is_empty() {
            return Err(EngineError::InvalidRequest("kind is required".into()));
        }
        let attributes: Option<Map<String, Value>> = match req.attributes {
            None | Some(Value::Null) => None,
            Some(Value::Object(m)) => Some(m),
            Some(_) => {
                return Err(EngineError::InvalidRequest(
                    "attributes must be a JSON object".into(),
                ))
            }
        };
        if let Some(s) = &req.state {
            if s.trim().is_empty() {
                return Err(EngineError::InvalidRequest(
                    "state must not be empty".into(),
                ));
            }
        }
        let deps: Option<Vec<String>> = req.depends_on.map(|d| {
            let mut seen = std::collections::HashSet::new();
            d.into_iter().filter(|x| seen.insert(x.clone())).collect()
        });

        let mut reg = self.inner.reg.write();
        if let Some(deps) = &deps {
            for d in deps {
                if d == id {
                    return Err(EngineError::InvalidRequest(format!(
                        "resource {id} cannot depend on itself"
                    )));
                }
                if !reg.contains(d) {
                    return Err(EngineError::InvalidRequest(format!(
                        "depends_on references unregistered resource {d}"
                    )));
                }
            }
        }

        if let Some(existing) = reg.get(id) {
            if existing.kind != kind {
                return Err(EngineError::Conflict(format!(
                    "resource {id} is already registered with kind {}",
                    existing.kind
                )));
            }
            if existing.owner != req.owner {
                return Err(EngineError::Conflict(format!(
                    "resource {id} is already registered to a different owner"
                )));
            }
            let old_state = existing.state.clone();
            if let Some(deps) = deps {
                reg.set_depends_on(id, deps);
            }
            if let Some(r) = reg.get_mut(id) {
                if let Some(attrs) = attributes {
                    if attrs.contains_key("workload") || attrs.contains_key("cpu_target") {
                        let w = Workload::from_attributes(&attrs);
                        r.workload.profile = w.profile;
                        r.workload.cpu_target = w.cpu_target;
                    }
                    r.attributes = attrs;
                }
                r.touch();
            }
            if let Some(state) = req.state.as_deref() {
                if state != old_state {
                    reg.cancel_pending(id);
                    reg.set_state(id, state);
                    self.emit(
                        NewEvent::new(EventType::StateChanged, "State synchronized by backend")
                            .resource(id, &kind, &req.owner)
                            .from_to(Some(&old_state), Some(state)),
                    );
                    if let Some(c) = lifecycle::completion_for(&kind, state) {
                        self.schedule(&mut reg, id, c);
                    }
                }
            }
            let view = reg.view_of(id).expect("resource exists");
            return Ok((view, false));
        }

        // Create. Quotas apply to genuinely new resources (state: null); restoring an
        // explicit state is restart recovery and is never rejected.
        if req.state.is_none() {
            let limit = reg.limit_for(&kind);
            let used = reg.usage(&req.owner, &kind);
            if used >= limit {
                let msg =
                    format!("Quota exceeded for {kind}: limit {limit} per owner, {used} in use");
                self.emit(
                    NewEvent::new(EventType::QuotaRejected, msg.clone())
                        .resource(id, &kind, &req.owner),
                );
                return Err(EngineError::QuotaExceeded(msg));
            }
        }
        let (state, completion, reason) = match req.state.as_deref() {
            Some(s) => (
                s.to_string(),
                lifecycle::completion_for(&kind, s),
                "Resource restored by sync",
            ),
            None => {
                let t = lifecycle::initial(&kind);
                (t.to.to_string(), t.completion, "Resource registered")
            }
        };
        let attrs = attributes.unwrap_or_default();
        let workload = Workload::from_attributes(&attrs);
        reg.insert(Resource {
            id: id.to_string(),
            kind: kind.clone(),
            owner: req.owner.clone(),
            state: state.clone(),
            depends_on: deps.unwrap_or_default(),
            attributes: attrs,
            workload,
            version: 1,
            updated_at: now(),
            pending: None,
            boots: 0,
        });
        self.emit(
            NewEvent::new(EventType::Registered, reason)
                .resource(id, &kind, &req.owner)
                .from_to(None, Some(&state)),
        );
        if let Some(c) = completion {
            self.schedule(&mut reg, id, c);
        }
        let view = reg.view_of(id).expect("resource exists");
        Ok((view, true))
    }

    pub fn get(&self, id: &str) -> Option<ResourceView> {
        self.inner.reg.read().view_of(id)
    }

    pub fn list(&self, kind: Option<&str>, owner: Option<&str>) -> Vec<ResourceView> {
        let reg = self.inner.reg.read();
        let mut items: Vec<ResourceView> = reg
            .iter()
            .filter(|r| kind.is_none_or(|k| r.kind == k))
            .filter(|r| owner.is_none_or(|o| r.owner == o))
            .map(|r| reg.view(r))
            .collect();
        items.sort_by(|a, b| a.id.cmp(&b.id));
        items
    }

    /// `POST /v1/resources/{id}/actions`.
    pub fn action(
        &self,
        id: &str,
        action: &str,
        reason: Option<&str>,
    ) -> EngineResult<ResourceView> {
        let action = action.trim();
        if action.is_empty() {
            return Err(EngineError::InvalidRequest("action is required".into()));
        }
        let mut reg = self.inner.reg.write();
        let r = reg
            .get(id)
            .ok_or_else(|| EngineError::NotFound(format!("resource {id} not found")))?;
        let (kind, owner, from) = (r.kind.clone(), r.owner.clone(), r.state.clone());
        let t = lifecycle::apply_action(&kind, &from, action)
            .map_err(|e| EngineError::InvalidTransition(format!("{id}: {e}")))?;
        reg.cancel_pending(id);
        reg.set_state(id, t.to);
        let reason = reason
            .map(str::to_string)
            .filter(|s| !s.trim().is_empty())
            .unwrap_or_else(|| lifecycle::default_action_reason(action));
        self.emit(
            NewEvent::new(EventType::StateChanged, reason)
                .resource(id, &kind, &owner)
                .from_to(Some(&from), Some(t.to)),
        );
        if let Some(c) = t.completion {
            self.schedule(&mut reg, id, c);
        }
        Ok(reg.view_of(id).expect("resource exists"))
    }

    /// `DELETE /v1/resources/{id}`.
    pub fn deregister(&self, id: &str) -> EngineResult<()> {
        let mut reg = self.inner.reg.write();
        if !reg.contains(id) {
            return Err(EngineError::NotFound(format!("resource {id} not found")));
        }
        let deps = reg.active_dependents(id);
        if !deps.is_empty() {
            return Err(EngineError::DependencyViolation(format!(
                "resource {id} has dependent resources: {}",
                deps.join(", ")
            )));
        }
        let r = reg.remove(id).expect("checked above");
        self.emit(
            NewEvent::new(EventType::Deregistered, "Resource deregistered")
                .resource(id, &r.kind, &r.owner)
                .from_to(Some(&r.state), None),
        );
        Ok(())
    }

    /// `PUT /v1/resources/{id}/workload`.
    pub fn set_workload(&self, id: &str, req: WorkloadRequest) -> EngineResult<ResourceView> {
        let mut reg = self.inner.reg.write();
        let r = reg
            .get_mut(id)
            .ok_or_else(|| EngineError::NotFound(format!("resource {id} not found")))?;
        let mut w = r.workload;
        if let Some(p) = req.profile.as_deref() {
            w.profile = Profile::parse(p).ok_or_else(|| {
                EngineError::InvalidRequest(format!(
                    "unknown workload profile {p:?}; expected idle|steady|spiky|stress|custom"
                ))
            })?;
        }
        if let Some(t) = req.cpu_target {
            if !(0.0..=100.0).contains(&t) {
                return Err(EngineError::InvalidRequest(
                    "cpu_target must be 0-100".into(),
                ));
            }
        }
        if req.profile.is_some() || req.cpu_target.is_some() {
            w.cpu_target = req.cpu_target;
        }
        if w.profile == Profile::Custom && w.cpu_target.is_none() {
            return Err(EngineError::InvalidRequest(
                "cpu_target is required for the custom profile".into(),
            ));
        }
        if let Some(rps) = req.extra_load_rps {
            if !(rps.is_finite() && rps >= 0.0) {
                return Err(EngineError::InvalidRequest(
                    "extra_load_rps must be >= 0".into(),
                ));
            }
            w.extra_load_rps = rps;
        }
        r.workload = w;
        r.touch();
        Ok(reg.view_of(id).expect("resource exists"))
    }

    // ---- scheduling -----------------------------------------------------------

    /// Schedule `c` for `id`, superseding any pending transition. With time scale 0 the
    /// completion is applied synchronously before this returns.
    pub(crate) fn schedule(&self, reg: &mut Registry, id: &str, c: Completion) {
        reg.cancel_pending(id);
        let scale = self.inner.cfg.time_scale;
        if scale == 0.0 {
            self.apply_completion(reg, id, &c);
            return;
        }
        let delay = Duration::from_secs_f64(c.delay_ms as f64 * scale / 1000.0);
        let token = self.next_token();
        match scheduler::spawn_completion(self, id, token, c.clone(), delay) {
            Some(pending) => {
                if let Some(r) = reg.get_mut(id) {
                    r.pending = Some(pending);
                }
            }
            // No tokio runtime available (e.g. synchronous benches): complete now.
            None => self.apply_completion(reg, id, &c),
        }
    }

    /// Called by the scheduler when a delayed completion fires.
    pub(crate) fn complete(&self, id: &str, token: u64, c: &Completion) {
        let mut reg = self.inner.reg.write();
        let Some(r) = reg.get_mut(id) else { return };
        match &r.pending {
            Some(p) if p.token == token => {
                r.pending = None;
            }
            _ => return, // superseded or cancelled
        }
        self.apply_completion(&mut reg, id, c);
    }

    fn apply_completion(&self, reg: &mut Registry, id: &str, c: &Completion) {
        let Some(r) = reg.get_mut(id) else { return };
        let (kind, owner, from) = (r.kind.clone(), r.owner.clone(), r.state.clone());
        let mut to = c.to;
        let mut reason = c.reason.to_string();
        if kind == "instance" && from == "pending" && c.to == "running" {
            r.boots += 1;
            let salt = r.boots;
            let fired =
                self.inner
                    .faults
                    .lock()
                    .roll_start_failure(self.inner.cfg.seed, id, &kind, salt);
            if let Some(f) = fired {
                to = "stopped";
                reason = "Server.InsufficientInstanceCapacity".to_string();
                self.emit(
                    NewEvent::new(
                        EventType::FaultTriggered,
                        format!("start_failure fault {} fired: {reason}", f.id),
                    )
                    .resource(id, &kind, &owner)
                    .fault(&f.id),
                );
            }
        }
        reg.set_state(id, to);
        self.emit(
            NewEvent::new(EventType::StateChanged, reason)
                .resource(id, &kind, &owner)
                .from_to(Some(&from), Some(to)),
        );
    }

    // ---- events ---------------------------------------------------------------

    pub fn events_page(&self, after: u64, limit: usize) -> EventsPage {
        self.inner.events.page(after, limit)
    }

    pub async fn wait_events(&self, after: u64, limit: usize, wait: Duration) -> EventsPage {
        let rx = self.inner.shutdown.subscribe();
        self.inner.events.wait_page(after, limit, wait, rx).await
    }

    /// Release long-pollers and stop background loops.
    pub fn begin_shutdown(&self) {
        self.inner.shutdown.send_replace(true);
    }

    pub fn shutdown_receiver(&self) -> watch::Receiver<bool> {
        self.inner.shutdown.subscribe()
    }

    // ---- metrics tick ---------------------------------------------------------

    /// One simulation tick: expire faults, evaluate crash faults, compute metrics.
    pub fn tick(&self) -> u64 {
        let tick = self.inner.ticks.fetch_add(1, Ordering::Relaxed) + 1;
        let seed = self.inner.cfg.seed;
        let ts = now();

        let expired = self.inner.faults.lock().expire(ts);
        for f in expired {
            self.emit(
                NewEvent::new(EventType::FaultCleared, format!("Fault {} expired", f.id))
                    .fault(&f.id)
                    .from_to(None, None),
            );
        }

        let crash = self.inner.faults.lock().active_of(FaultType::Crash);
        if !crash.is_empty() {
            let mut reg = self.inner.reg.write();
            let mut running: Vec<(String, String)> = reg
                .iter()
                .filter(|r| r.kind == "instance" && r.state == "running")
                .map(|r| (r.id.clone(), r.owner.clone()))
                .collect();
            running.sort();
            for (id, owner) in running {
                let Some(f) = crash
                    .iter()
                    .find(|f| f.matches(&id, "instance") && f.roll(seed, &id, tick))
                else {
                    continue;
                };
                self.inner.faults.lock().mark_triggered(&f.id, false);
                reg.cancel_pending(&id);
                reg.set_state(&id, "stopped");
                let reason = "Server.InternalError: simulated host failure";
                self.emit(
                    NewEvent::new(
                        EventType::FaultTriggered,
                        format!("crash fault {} fired: {reason}", f.id),
                    )
                    .resource(&id, "instance", &owner)
                    .fault(&f.id),
                );
                self.emit(
                    NewEvent::new(EventType::StateChanged, reason)
                        .resource(&id, "instance", &owner)
                        .from_to(Some("running"), Some("stopped")),
                );
            }
        }

        let mut samples: Vec<_> = {
            let reg = self.inner.reg.read();
            reg.iter()
                .filter(|r| r.kind == "instance" && r.state == "running")
                .map(|r| compute_sample(seed, tick, &r.id, &r.owner, &r.workload))
                .collect()
        };
        samples.sort_by(|a, b| a.resource_id.cmp(&b.resource_id));
        *self.inner.metrics.write() = MetricsSnapshot {
            tick,
            ts: fmt_ts(&ts),
            samples,
        };
        tick
    }

    pub fn metrics_snapshot(&self) -> MetricsSnapshot {
        self.inner.metrics.read().clone()
    }

    // ---- quotas ---------------------------------------------------------------

    pub fn quotas(&self, owner: Option<&str>) -> QuotaView {
        let reg = self.inner.reg.read();
        QuotaView {
            limits: reg.limits().clone(),
            usage: reg.usage_map(owner),
        }
    }

    pub fn set_quotas(
        &self,
        limits: &BTreeMap<String, u32>,
    ) -> EngineResult<BTreeMap<String, u32>> {
        if limits.keys().any(|k| k.trim().is_empty()) {
            return Err(EngineError::InvalidRequest(
                "quota kind must not be empty".into(),
            ));
        }
        let mut reg = self.inner.reg.write();
        reg.set_limits(limits);
        Ok(reg.limits().clone())
    }

    // ---- faults ---------------------------------------------------------------

    pub fn inject_fault(&self, req: FaultRequest) -> EngineResult<Fault> {
        FaultStore::validate(&req).map_err(EngineError::InvalidRequest)?;
        let (owner, kind_of_target) = match req.target.as_deref() {
            Some(t) => {
                let reg = self.inner.reg.read();
                let r = reg
                    .get(t)
                    .ok_or_else(|| EngineError::NotFound(format!("fault target {t} not found")))?;
                (Some(r.owner.clone()), Some(r.kind.clone()))
            }
            None => (None, None),
        };
        let f = self.inner.faults.lock().create(req, now());
        let mut ev = NewEvent::new(
            EventType::FaultInjected,
            format!(
                "Fault {} ({}) injected with probability {}",
                f.id,
                f.fault_type.as_str(),
                f.probability
            ),
        )
        .fault(&f.id);
        ev.resource_id = f.target.clone();
        ev.kind = f.kind.clone().or(kind_of_target);
        ev.owner = owner;
        self.emit(ev);
        Ok(f)
    }

    pub fn list_faults(&self) -> Vec<Fault> {
        self.inner.faults.lock().list()
    }

    pub fn clear_fault(&self, id: &str) -> EngineResult<()> {
        let f = self
            .inner
            .faults
            .lock()
            .remove(id)
            .ok_or_else(|| EngineError::NotFound(format!("fault {id} not found")))?;
        let mut ev =
            NewEvent::new(EventType::FaultCleared, format!("Fault {} cleared", f.id)).fault(&f.id);
        ev.resource_id = f.target.clone();
        ev.kind = f.kind.clone();
        self.emit(ev);
        Ok(())
    }

    // ---- load balancing -------------------------------------------------------

    pub fn lb_simulate(&self, req: &lb::LbSimRequest) -> EngineResult<lb::LbSimResult> {
        let result = lb::simulate(req, self.inner.cfg.seed).map_err(EngineError::InvalidRequest)?;
        if req.apply_load {
            let mut reg = self.inner.reg.write();
            for t in &req.targets {
                let rps = result
                    .per_target
                    .iter()
                    .find(|p| p.id == t.id)
                    .map(|p| p.requests as f64 / req.duration_s)
                    .unwrap_or(0.0);
                if let Some(r) = reg.get_mut(&t.id) {
                    if r.kind == "instance" {
                        r.workload.extra_load_rps = rps;
                        r.touch();
                    }
                }
            }
        }
        Ok(result)
    }

    pub fn lb_health(&self, req: &HealthRequest) -> HealthResponse {
        let reg = self.inner.reg.read();
        let faults = self.inner.faults.lock();
        let tick = self.inner.ticks.load(Ordering::Relaxed);
        let seed = self.inner.cfg.seed;
        let targets = req
            .targets
            .iter()
            .map(|t| {
                let (state, reason) = match reg.get(&t.id) {
                    None => ("unhealthy", Some("Target.NotRegistered")),
                    Some(r) if r.kind == "instance" => {
                        if r.state == "running" {
                            let crashed = faults
                                .active_of(FaultType::Crash)
                                .iter()
                                .any(|f| f.matches(&r.id, &r.kind));
                            let degraded = faults
                                .active_of(FaultType::Degrade)
                                .iter()
                                .any(|f| f.matches(&r.id, &r.kind) && f.roll(seed, &r.id, tick));
                            if crashed || degraded {
                                ("unhealthy", Some("Target.FailedHealthChecks"))
                            } else {
                                ("healthy", None)
                            }
                        } else {
                            ("unused", Some("Target.InvalidState"))
                        }
                    }
                    Some(r) => {
                        if matches!(r.state.as_str(), "available" | "active" | "running") {
                            ("healthy", None)
                        } else {
                            ("unused", Some("Target.InvalidState"))
                        }
                    }
                };
                HealthStatus {
                    id: t.id.clone(),
                    state: state.to_string(),
                    reason: reason.map(str::to_string),
                }
            })
            .collect();
        HealthResponse { targets }
    }

    // ---- autoscaling ----------------------------------------------------------

    pub fn autoscaling_evaluate(
        &self,
        req: &autoscaling::EvaluateRequest,
    ) -> EngineResult<autoscaling::Decision> {
        let d = autoscaling::evaluate(req).map_err(EngineError::InvalidRequest)?;
        if d.change != 0 {
            let mut ev = NewEvent::new(EventType::ScalingDecision, d.reason.clone()).from_to(
                Some(&req.group.desired.to_string()),
                Some(&d.desired.to_string()),
            );
            ev.resource_id = Some(req.group.name.clone()).filter(|n| !n.is_empty());
            ev.kind = Some("autoscaling_group".to_string());
            self.emit(ev);
        }
        Ok(d)
    }

    // ---- stats ----------------------------------------------------------------

    pub fn stats(&self) -> Stats {
        let (resources, total, pending) = {
            let reg = self.inner.reg.read();
            (
                reg.counts_by_kind(),
                reg.len() as u64,
                reg.pending_count() as u64,
            )
        };
        let active_faults = self.inner.faults.lock().active_count() as u64;
        Stats {
            uptime_seconds: self.inner.started.elapsed().as_secs(),
            resources,
            resources_total: total,
            events_emitted: self.inner.events.emitted(),
            event_seq: self.inner.events.last_seq(),
            pending_transitions: pending,
            active_faults,
            ticks: self.inner.ticks.load(Ordering::Relaxed),
            time_scale: self.inner.cfg.time_scale,
            seed: self.inner.cfg.seed,
        }
    }

    /// Run `tick()` every `ENGINE_TICK_MS` until shutdown.
    pub fn spawn_tick_loop(&self) -> tokio::task::JoinHandle<()> {
        let engine = self.clone();
        let mut shutdown = self.shutdown_receiver();
        tokio::spawn(async move {
            let mut interval =
                tokio::time::interval(Duration::from_millis(engine.inner.cfg.tick_ms));
            interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
            interval.tick().await; // first tick fires immediately; skip it
            loop {
                tokio::select! {
                    _ = interval.tick() => { engine.tick(); }
                    _ = shutdown.wait_for(|v| *v) => break,
                }
            }
        })
    }
}
