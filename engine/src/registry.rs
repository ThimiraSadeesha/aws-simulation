//! Resource registry: resources, reverse dependency index and per-owner quota usage.
//!
//! The registry is plain data; synchronisation and event emission are done by `Engine`.

use std::collections::{BTreeMap, BTreeSet, HashMap};

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

use crate::lifecycle::is_terminal;
use crate::util::{fmt_ts, now};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "snake_case")]
pub enum Profile {
    Idle,
    #[default]
    Steady,
    Spiky,
    Stress,
    Custom,
}

impl Profile {
    pub fn parse(s: &str) -> Option<Profile> {
        match s {
            "idle" => Some(Profile::Idle),
            "steady" => Some(Profile::Steady),
            "spiky" => Some(Profile::Spiky),
            "stress" => Some(Profile::Stress),
            "custom" => Some(Profile::Custom),
            _ => None,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Serialize, Default)]
pub struct Workload {
    pub profile: Profile,
    pub cpu_target: Option<f64>,
    pub extra_load_rps: f64,
}

impl Workload {
    /// Derive a workload from registration attributes (`workload`, `cpu_target`).
    /// Unknown profile names fall back to `steady`.
    pub fn from_attributes(attrs: &Map<String, Value>) -> Workload {
        let profile = attrs
            .get("workload")
            .and_then(Value::as_str)
            .and_then(Profile::parse)
            .unwrap_or_default();
        let cpu_target = attrs
            .get("cpu_target")
            .and_then(Value::as_f64)
            .map(|v| v.clamp(0.0, 100.0));
        Workload {
            profile,
            cpu_target,
            extra_load_rps: 0.0,
        }
    }
}

#[derive(Debug)]
pub struct Pending {
    pub token: u64,
    pub to: &'static str,
    pub due_at: DateTime<Utc>,
    pub abort: Option<tokio::task::AbortHandle>,
}

impl Pending {
    pub fn cancel(self) {
        if let Some(a) = self.abort {
            a.abort();
        }
    }
}

#[derive(Debug)]
pub struct Resource {
    pub id: String,
    pub kind: String,
    pub owner: String,
    pub state: String,
    pub depends_on: Vec<String>,
    pub attributes: Map<String, Value>,
    pub workload: Workload,
    pub version: u64,
    pub updated_at: DateTime<Utc>,
    pub pending: Option<Pending>,
    /// Number of boot completions evaluated (salt for start_failure rolls).
    pub boots: u64,
}

impl Resource {
    pub fn is_active(&self) -> bool {
        !is_terminal(&self.state)
    }

    pub fn touch(&mut self) {
        self.version += 1;
        self.updated_at = now();
    }
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct PendingView {
    pub to: String,
    pub completes_at: String,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct ResourceView {
    pub id: String,
    pub kind: String,
    pub owner: String,
    pub state: String,
    pub depends_on: Vec<String>,
    pub dependents: Vec<String>,
    pub attributes: Value,
    pub workload: Workload,
    pub version: u64,
    pub updated_at: String,
    pub pending_transition: Option<PendingView>,
}

pub const DEFAULT_QUOTA: u32 = 1000;

pub fn default_limits() -> BTreeMap<String, u32> {
    [
        ("instance", 20),
        ("vpc", 5),
        ("subnet", 50),
        ("bucket", 100),
        ("nat_gateway", 5),
        ("load_balancer", 10),
        ("autoscaling_group", 10),
        ("security_group", 100),
    ]
    .into_iter()
    .map(|(k, v)| (k.to_string(), v))
    .collect()
}

#[derive(Debug)]
pub struct Registry {
    resources: HashMap<String, Resource>,
    /// id -> ids of resources that list it in `depends_on`.
    rdeps: HashMap<String, BTreeSet<String>>,
    /// owner -> kind -> number of active (non-terminal) resources.
    usage: HashMap<String, HashMap<String, u32>>,
    limits: BTreeMap<String, u32>,
}

impl Default for Registry {
    fn default() -> Self {
        Self::new()
    }
}

impl Registry {
    pub fn new() -> Self {
        Registry {
            resources: HashMap::new(),
            rdeps: HashMap::new(),
            usage: HashMap::new(),
            limits: default_limits(),
        }
    }

    pub fn len(&self) -> usize {
        self.resources.len()
    }

    pub fn is_empty(&self) -> bool {
        self.resources.is_empty()
    }

    pub fn get(&self, id: &str) -> Option<&Resource> {
        self.resources.get(id)
    }

    pub fn get_mut(&mut self, id: &str) -> Option<&mut Resource> {
        self.resources.get_mut(id)
    }

    pub fn contains(&self, id: &str) -> bool {
        self.resources.contains_key(id)
    }

    pub fn iter(&self) -> impl Iterator<Item = &Resource> {
        self.resources.values()
    }

    /// Ids of registered, non-terminal resources that depend on `id` (sorted).
    pub fn active_dependents(&self, id: &str) -> Vec<String> {
        self.rdeps
            .get(id)
            .map(|set| {
                set.iter()
                    .filter(|d| self.resources.get(*d).is_some_and(Resource::is_active))
                    .cloned()
                    .collect()
            })
            .unwrap_or_default()
    }

    pub fn view(&self, r: &Resource) -> ResourceView {
        ResourceView {
            id: r.id.clone(),
            kind: r.kind.clone(),
            owner: r.owner.clone(),
            state: r.state.clone(),
            depends_on: r.depends_on.clone(),
            dependents: self.active_dependents(&r.id),
            attributes: Value::Object(r.attributes.clone()),
            workload: r.workload,
            version: r.version,
            updated_at: fmt_ts(&r.updated_at),
            pending_transition: r.pending.as_ref().map(|p| PendingView {
                to: p.to.to_string(),
                completes_at: fmt_ts(&p.due_at),
            }),
        }
    }

    pub fn view_of(&self, id: &str) -> Option<ResourceView> {
        self.resources.get(id).map(|r| self.view(r))
    }

    // ---- quotas -------------------------------------------------------------

    pub fn limits(&self) -> &BTreeMap<String, u32> {
        &self.limits
    }

    pub fn set_limits(&mut self, updates: &BTreeMap<String, u32>) {
        for (k, v) in updates {
            self.limits.insert(k.clone(), *v);
        }
    }

    pub fn limit_for(&self, kind: &str) -> u32 {
        self.limits.get(kind).copied().unwrap_or(DEFAULT_QUOTA)
    }

    pub fn usage(&self, owner: &str, kind: &str) -> u32 {
        self.usage
            .get(owner)
            .and_then(|m| m.get(kind))
            .copied()
            .unwrap_or(0)
    }

    /// Usage per kind for one owner, or summed over all owners when `owner` is None.
    pub fn usage_map(&self, owner: Option<&str>) -> BTreeMap<String, u32> {
        let mut out = BTreeMap::new();
        for (o, kinds) in &self.usage {
            if owner.is_some_and(|want| want != o) {
                continue;
            }
            for (k, n) in kinds {
                if *n > 0 {
                    *out.entry(k.clone()).or_insert(0) += *n;
                }
            }
        }
        out
    }

    fn adjust_usage(&mut self, owner: &str, kind: &str, delta: i64) {
        let m = self.usage.entry(owner.to_string()).or_default();
        let e = m.entry(kind.to_string()).or_insert(0);
        *e = (i64::from(*e) + delta).max(0) as u32;
    }

    // ---- mutation -----------------------------------------------------------

    /// Insert a new resource (caller guarantees the id is not registered).
    pub fn insert(&mut self, r: Resource) {
        for d in &r.depends_on {
            self.rdeps
                .entry(d.clone())
                .or_default()
                .insert(r.id.clone());
        }
        if r.is_active() {
            self.adjust_usage(&r.owner, &r.kind, 1);
        }
        self.resources.insert(r.id.clone(), r);
    }

    /// Remove a resource, cancelling any pending transition.
    pub fn remove(&mut self, id: &str) -> Option<Resource> {
        let mut r = self.resources.remove(id)?;
        for d in &r.depends_on {
            if let Some(set) = self.rdeps.get_mut(d) {
                set.remove(id);
                if set.is_empty() {
                    self.rdeps.remove(d);
                }
            }
        }
        if r.is_active() {
            self.adjust_usage(&r.owner, &r.kind, -1);
        }
        if let Some(p) = r.pending.take() {
            p.cancel();
        }
        Some(r)
    }

    /// Change a resource's state, keeping quota usage consistent. Returns the old state.
    pub fn set_state(&mut self, id: &str, new_state: &str) -> Option<String> {
        let (owner, kind, was_active, old) = {
            let r = self.resources.get_mut(id)?;
            let old = std::mem::replace(&mut r.state, new_state.to_string());
            r.touch();
            (r.owner.clone(), r.kind.clone(), !is_terminal(&old), old)
        };
        let now_active = !is_terminal(new_state);
        if was_active != now_active {
            self.adjust_usage(&owner, &kind, if now_active { 1 } else { -1 });
        }
        Some(old)
    }

    pub fn set_depends_on(&mut self, id: &str, deps: Vec<String>) {
        let Some(r) = self.resources.get_mut(id) else {
            return;
        };
        let old = std::mem::replace(&mut r.depends_on, deps.clone());
        for d in &old {
            if let Some(set) = self.rdeps.get_mut(d) {
                set.remove(id);
                if set.is_empty() {
                    self.rdeps.remove(d);
                }
            }
        }
        for d in deps {
            self.rdeps.entry(d).or_default().insert(id.to_string());
        }
    }

    /// Cancel and clear the pending transition of `id`, if any.
    pub fn cancel_pending(&mut self, id: &str) {
        if let Some(p) = self.resources.get_mut(id).and_then(|r| r.pending.take()) {
            p.cancel();
        }
    }

    pub fn counts_by_kind(&self) -> BTreeMap<String, u64> {
        let mut out = BTreeMap::new();
        for r in self.resources.values() {
            *out.entry(r.kind.clone()).or_insert(0) += 1;
        }
        out
    }

    pub fn pending_count(&self) -> usize {
        self.resources
            .values()
            .filter(|r| r.pending.is_some())
            .count()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn res(id: &str, kind: &str, state: &str, deps: &[&str]) -> Resource {
        Resource {
            id: id.into(),
            kind: kind.into(),
            owner: "o".into(),
            state: state.into(),
            depends_on: deps.iter().map(|s| s.to_string()).collect(),
            attributes: Map::new(),
            workload: Workload::default(),
            version: 1,
            updated_at: now(),
            pending: None,
            boots: 0,
        }
    }

    #[test]
    fn dependents_and_usage_tracking() {
        let mut reg = Registry::new();
        reg.insert(res("vpc-1", "vpc", "available", &[]));
        reg.insert(res("subnet-1", "subnet", "available", &["vpc-1"]));
        reg.insert(res("i-1", "instance", "running", &["subnet-1"]));
        assert_eq!(reg.active_dependents("vpc-1"), vec!["subnet-1"]);
        assert_eq!(reg.usage("o", "instance"), 1);
        reg.set_state("i-1", "terminated");
        assert_eq!(reg.usage("o", "instance"), 0);
        assert!(reg.active_dependents("subnet-1").is_empty());
        reg.set_state("i-1", "running");
        assert_eq!(reg.usage("o", "instance"), 1);
        reg.remove("i-1");
        assert_eq!(reg.usage("o", "instance"), 0);
        assert!(reg.active_dependents("subnet-1").is_empty());
        reg.set_depends_on("subnet-1", vec![]);
        assert!(reg.active_dependents("vpc-1").is_empty());
    }

    #[test]
    fn quotas_default_and_override() {
        let mut reg = Registry::new();
        assert_eq!(reg.limit_for("instance"), 20);
        assert_eq!(reg.limit_for("queue"), DEFAULT_QUOTA);
        reg.set_limits(&BTreeMap::from([("instance".to_string(), 5)]));
        assert_eq!(reg.limit_for("instance"), 5);
        assert_eq!(reg.limit_for("vpc"), 5);
    }

    #[test]
    fn workload_from_attributes() {
        let attrs: Map<String, Value> =
            serde_json::from_str(r#"{"workload":"stress","cpu_target":150}"#).unwrap();
        let w = Workload::from_attributes(&attrs);
        assert_eq!(w.profile, Profile::Stress);
        assert_eq!(w.cpu_target, Some(100.0));
        assert_eq!(
            Workload::from_attributes(&Map::new()).profile,
            Profile::Steady
        );
    }
}
