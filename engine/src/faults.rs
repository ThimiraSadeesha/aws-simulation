//! Fault injection store and deterministic fault rolls.

use chrono::{DateTime, Utc};
use rand::Rng;
use serde::{Deserialize, Serialize};

use crate::util::{fmt_ts, SeedMix};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum FaultType {
    Crash,
    Degrade,
    StartFailure,
}

impl FaultType {
    pub fn as_str(self) -> &'static str {
        match self {
            FaultType::Crash => "crash",
            FaultType::Degrade => "degrade",
            FaultType::StartFailure => "start_failure",
        }
    }
}

#[derive(Debug, Clone, Deserialize)]
pub struct FaultRequest {
    #[serde(default)]
    pub target: Option<String>,
    #[serde(default)]
    pub kind: Option<String>,
    #[serde(rename = "type")]
    pub fault_type: FaultType,
    #[serde(default = "one")]
    pub probability: f64,
    #[serde(default)]
    pub duration_ms: Option<u64>,
}

fn one() -> f64 {
    1.0
}

#[derive(Debug, Clone, Serialize)]
pub struct Fault {
    pub id: String,
    pub target: Option<String>,
    pub kind: Option<String>,
    #[serde(rename = "type")]
    pub fault_type: FaultType,
    pub probability: f64,
    pub duration_ms: Option<u64>,
    pub created_at: String,
    pub expires_at: Option<String>,
    /// `false` once a one-shot `start_failure` fault has fired.
    pub active: bool,
    pub triggered_count: u64,
    #[serde(skip)]
    pub expires: Option<DateTime<Utc>>,
}

impl Fault {
    /// A fault with neither target nor kind applies to every instance.
    pub fn matches(&self, id: &str, kind: &str) -> bool {
        self.active
            && self.target.as_deref().is_none_or(|t| t == id)
            && self.kind.as_deref().is_none_or(|k| k == kind)
    }

    /// Deterministic Bernoulli roll for this fault against `resource_id` with `salt`.
    pub fn roll(&self, seed: u64, resource_id: &str, salt: u64) -> bool {
        if self.probability >= 1.0 {
            return true;
        }
        if self.probability <= 0.0 {
            return false;
        }
        let mut rng = SeedMix::new(seed)
            .str("fault")
            .str(&self.id)
            .str(resource_id)
            .u64(salt)
            .rng();
        rng.gen::<f64>() < self.probability
    }
}

#[derive(Debug, Default)]
pub struct FaultStore {
    next_id: u64,
    faults: Vec<Fault>,
}

impl FaultStore {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn validate(req: &FaultRequest) -> Result<(), String> {
        if !(0.0..=1.0).contains(&req.probability) || req.probability.is_nan() {
            return Err(format!(
                "probability must be between 0.0 and 1.0, got {}",
                req.probability
            ));
        }
        if req.duration_ms == Some(0) {
            return Err("duration_ms must be > 0 when given".to_string());
        }
        Ok(())
    }

    pub fn create(&mut self, req: FaultRequest, now: DateTime<Utc>) -> Fault {
        self.next_id += 1;
        let expires = req
            .duration_ms
            .map(|ms| now + chrono::Duration::milliseconds(ms.min(i64::MAX as u64) as i64));
        let f = Fault {
            id: format!("flt-{}", self.next_id),
            target: req.target,
            kind: req.kind,
            fault_type: req.fault_type,
            probability: req.probability,
            duration_ms: req.duration_ms,
            created_at: fmt_ts(&now),
            expires_at: expires.as_ref().map(fmt_ts),
            active: true,
            triggered_count: 0,
            expires,
        };
        self.faults.push(f.clone());
        f
    }

    pub fn list(&self) -> Vec<Fault> {
        self.faults.clone()
    }

    pub fn remove(&mut self, id: &str) -> Option<Fault> {
        let pos = self.faults.iter().position(|f| f.id == id)?;
        Some(self.faults.remove(pos))
    }

    /// Remove faults whose duration elapsed.
    pub fn expire(&mut self, now: DateTime<Utc>) -> Vec<Fault> {
        let (expired, keep): (Vec<Fault>, Vec<Fault>) = std::mem::take(&mut self.faults)
            .into_iter()
            .partition(|f| f.expires.is_some_and(|e| e <= now));
        self.faults = keep;
        expired
    }

    pub fn active_count(&self) -> usize {
        self.faults.iter().filter(|f| f.active).count()
    }

    pub fn has_active(&self, t: FaultType) -> bool {
        self.faults.iter().any(|f| f.active && f.fault_type == t)
    }

    pub fn active_of(&self, t: FaultType) -> Vec<Fault> {
        self.faults
            .iter()
            .filter(|f| f.active && f.fault_type == t)
            .cloned()
            .collect()
    }

    pub fn mark_triggered(&mut self, id: &str, one_shot: bool) {
        if let Some(f) = self.faults.iter_mut().find(|f| f.id == id) {
            f.triggered_count += 1;
            if one_shot {
                f.active = false;
            }
        }
    }

    /// Evaluate `start_failure` faults for a boot of `resource_id`. On a hit the fault is
    /// consumed (one-shot: "next start/boot") and returned.
    pub fn roll_start_failure(
        &mut self,
        seed: u64,
        resource_id: &str,
        kind: &str,
        salt: u64,
    ) -> Option<Fault> {
        let hit = self
            .faults
            .iter()
            .find(|f| {
                f.fault_type == FaultType::StartFailure
                    && f.matches(resource_id, kind)
                    && f.roll(seed, resource_id, salt)
            })?
            .id
            .clone();
        self.mark_triggered(&hit, true);
        self.faults.iter().find(|f| f.id == hit).cloned()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::util::now;

    fn req(t: FaultType, p: f64, target: Option<&str>) -> FaultRequest {
        FaultRequest {
            target: target.map(String::from),
            kind: None,
            fault_type: t,
            probability: p,
            duration_ms: None,
        }
    }

    #[test]
    fn matching_and_rolls() {
        let mut s = FaultStore::new();
        let f = s.create(req(FaultType::Crash, 1.0, Some("i-1")), now());
        assert_eq!(f.id, "flt-1");
        assert!(f.matches("i-1", "instance"));
        assert!(!f.matches("i-2", "instance"));
        assert!(f.roll(1, "i-1", 0));
        let g = s.create(req(FaultType::Crash, 0.0, None), now());
        assert!(!g.roll(1, "i-1", 0));
        let h = s.create(req(FaultType::Crash, 0.5, None), now());
        let hits = (0..1000).filter(|i| h.roll(7, "i-9", *i)).count();
        assert!((400..600).contains(&hits), "hits={hits}");
        assert_eq!(
            (0..50).map(|i| h.roll(7, "x", i)).collect::<Vec<_>>(),
            (0..50).map(|i| h.roll(7, "x", i)).collect::<Vec<_>>()
        );
    }

    #[test]
    fn start_failure_is_one_shot() {
        let mut s = FaultStore::new();
        s.create(req(FaultType::StartFailure, 1.0, None), now());
        assert!(s.roll_start_failure(1, "i-1", "instance", 1).is_some());
        assert!(s.roll_start_failure(1, "i-1", "instance", 2).is_none());
        assert_eq!(s.active_count(), 0);
        assert_eq!(s.list().len(), 1);
    }

    #[test]
    fn expiry() {
        let mut s = FaultStore::new();
        let mut r = req(FaultType::Degrade, 1.0, None);
        r.duration_ms = Some(10);
        let t0 = now();
        s.create(r, t0);
        assert!(s.expire(t0).is_empty());
        assert_eq!(s.expire(t0 + chrono::Duration::milliseconds(11)).len(), 1);
        assert!(s.list().is_empty());
    }

    #[test]
    fn validation() {
        assert!(FaultStore::validate(&req(FaultType::Crash, 1.5, None)).is_err());
        assert!(FaultStore::validate(&req(FaultType::Crash, 0.3, None)).is_ok());
    }
}
