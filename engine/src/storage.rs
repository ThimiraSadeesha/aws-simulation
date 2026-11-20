//! Object storage simulation: lifecycle-rule evaluation and a latency/throughput model.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

use crate::util::{now, parse_ts, round_to};

// ---- lifecycle ----------------------------------------------------------------

#[derive(Debug, Clone, Deserialize)]
pub struct TransitionRule {
    pub days: u32,
    pub storage_class: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Rule {
    #[serde(default)]
    pub id: String,
    #[serde(default = "enabled")]
    pub status: String,
    #[serde(default)]
    pub prefix: Option<String>,
    #[serde(default)]
    pub expiration_days: Option<u32>,
    #[serde(default)]
    pub noncurrent_expiration_days: Option<u32>,
    #[serde(default)]
    pub transitions: Vec<TransitionRule>,
    #[serde(default)]
    pub abort_incomplete_multipart_days: Option<u32>,
}

fn enabled() -> String {
    "Enabled".to_string()
}

#[derive(Debug, Clone, Deserialize)]
pub struct Object {
    pub key: String,
    #[serde(default)]
    pub version_id: Option<String>,
    #[serde(default = "yes")]
    pub is_latest: bool,
    pub last_modified: String,
    #[serde(default)]
    pub size: u64,
    #[serde(default)]
    pub storage_class: Option<String>,
    #[serde(default)]
    pub noncurrent_since: Option<String>,
}

fn yes() -> bool {
    true
}

#[derive(Debug, Clone, Deserialize)]
pub struct MultipartUpload {
    pub upload_id: String,
    pub key: String,
    pub initiated: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct LifecycleRequest {
    #[serde(default)]
    pub now: Option<String>,
    #[serde(default)]
    pub rules: Vec<Rule>,
    #[serde(default)]
    pub objects: Vec<Object>,
    #[serde(default)]
    pub multipart_uploads: Vec<MultipartUpload>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct LifecycleAction {
    #[serde(rename = "type")]
    pub action_type: String,
    pub key: String,
    pub version_id: Option<String>,
    pub upload_id: Option<String>,
    pub storage_class: Option<String>,
    pub rule_id: String,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct LifecycleResult {
    pub actions: Vec<LifecycleAction>,
}

/// Storage classes from warmest to coldest. Transitions only move colder.
pub fn class_rank(class: &str) -> Option<u8> {
    Some(match class {
        "STANDARD" => 0,
        "INTELLIGENT_TIERING" => 1,
        "STANDARD_IA" => 2,
        "ONEZONE_IA" => 3,
        "GLACIER_IR" => 4,
        "GLACIER" => 5,
        "DEEP_ARCHIVE" => 6,
        _ => return None,
    })
}

fn age_days(now: &DateTime<Utc>, t: &DateTime<Utc>) -> i64 {
    (*now - *t).num_seconds().div_euclid(86_400)
}

fn applies(rule: &Rule, key: &str) -> bool {
    rule.status.eq_ignore_ascii_case("enabled")
        && rule.prefix.as_deref().is_none_or(|p| key.starts_with(p))
}

pub fn evaluate_lifecycle(req: &LifecycleRequest) -> Result<LifecycleResult, String> {
    let now = match req.now.as_deref() {
        Some(s) => parse_ts(s)?,
        None => now(),
    };
    for r in &req.rules {
        for t in &r.transitions {
            if class_rank(&t.storage_class).is_none() {
                return Err(format!(
                    "rule {}: unknown storage class {:?}",
                    r.id, t.storage_class
                ));
            }
        }
    }
    let mut actions = Vec::new();
    for o in &req.objects {
        let rules: Vec<&Rule> = req.rules.iter().filter(|r| applies(r, &o.key)).collect();
        if rules.is_empty() {
            continue;
        }
        let modified = parse_ts(&o.last_modified)?;
        let mk = |t: &str, rule: &Rule, class: Option<String>| LifecycleAction {
            action_type: t.to_string(),
            key: o.key.clone(),
            version_id: o.version_id.clone(),
            upload_id: None,
            storage_class: class,
            rule_id: rule.id.clone(),
        };
        if o.is_latest {
            let age = age_days(&now, &modified);
            if let Some(rule) = rules
                .iter()
                .find(|r| r.expiration_days.is_some_and(|d| age >= i64::from(d)))
            {
                actions.push(mk("expire", rule, None));
                continue;
            }
            let current = o.storage_class.as_deref().unwrap_or("STANDARD");
            let current_rank = class_rank(current).unwrap_or(0);
            // Coldest eligible class wins; ties go to the first rule.
            let mut best: Option<(&Rule, &TransitionRule, u8)> = None;
            for rule in &rules {
                for t in &rule.transitions {
                    let rank = class_rank(&t.storage_class).unwrap_or(0);
                    if age >= i64::from(t.days)
                        && rank > current_rank
                        && best.is_none_or(|(_, _, b)| rank > b)
                    {
                        best = Some((rule, t, rank));
                    }
                }
            }
            if let Some((rule, t, _)) = best {
                actions.push(mk("transition", rule, Some(t.storage_class.clone())));
            }
        } else {
            let since = match o.noncurrent_since.as_deref() {
                Some(s) => parse_ts(s)?,
                None => modified,
            };
            let age = age_days(&now, &since);
            if let Some(rule) = rules.iter().find(|r| {
                r.noncurrent_expiration_days
                    .is_some_and(|d| age >= i64::from(d))
            }) {
                actions.push(mk("expire_noncurrent", rule, None));
            }
        }
    }
    for u in &req.multipart_uploads {
        let initiated = parse_ts(&u.initiated)?;
        let age = age_days(&now, &initiated);
        if let Some(rule) = req.rules.iter().find(|r| {
            applies(r, &u.key)
                && r.abort_incomplete_multipart_days
                    .is_some_and(|d| age >= i64::from(d))
        }) {
            actions.push(LifecycleAction {
                action_type: "abort_multipart".to_string(),
                key: u.key.clone(),
                version_id: None,
                upload_id: Some(u.upload_id.clone()),
                storage_class: None,
                rule_id: rule.id.clone(),
            });
        }
    }
    Ok(LifecycleResult { actions })
}

// ---- latency model -----------------------------------------------------------------

#[derive(Debug, Clone, Deserialize)]
pub struct SimRequest {
    pub op: String,
    #[serde(default)]
    pub size_bytes: u64,
    #[serde(default)]
    pub storage_class: Option<String>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct SimResult {
    pub latency_ms: f64,
    /// Effective throughput in megabits per second (size * 8 / 1e6 / latency).
    pub throughput_mbps: f64,
    pub first_byte_ms: f64,
    pub transfer_ms: f64,
}

/// Bytes per second of the transfer phase (100 MB/s).
pub const TRANSFER_BYTES_PER_SEC: f64 = 100_000_000.0;

pub fn simulate(req: &SimRequest) -> Result<SimResult, String> {
    let class = req.storage_class.as_deref().unwrap_or("STANDARD");
    if class_rank(class).is_none() {
        return Err(format!("unknown storage class {class:?}"));
    }
    let first_byte_ms = match (req.op.as_str(), class) {
        ("put", _) => 10.0,
        ("get", "GLACIER") => 3_600_000.0,
        ("get", "DEEP_ARCHIVE") => 43_200_000.0,
        ("get", _) => 10.0,
        (other, _) => return Err(format!("unknown op {other:?}; expected put|get")),
    };
    let transfer_ms = req.size_bytes as f64 / TRANSFER_BYTES_PER_SEC * 1000.0;
    let latency_ms = first_byte_ms + transfer_ms;
    let throughput_mbps = if req.size_bytes == 0 {
        0.0
    } else {
        (req.size_bytes as f64 * 8.0 / 1e6) / (latency_ms / 1000.0)
    };
    Ok(SimResult {
        latency_ms: round_to(latency_ms, 3),
        throughput_mbps: round_to(throughput_mbps, 3),
        first_byte_ms,
        transfer_ms: round_to(transfer_ms, 3),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn obj(key: &str, latest: bool, modified: &str, class: &str) -> Object {
        Object {
            key: key.into(),
            version_id: Some("v1".into()),
            is_latest: latest,
            last_modified: modified.into(),
            size: 10,
            storage_class: Some(class.into()),
            noncurrent_since: None,
        }
    }

    fn rule() -> Rule {
        Rule {
            id: "r".into(),
            status: "Enabled".into(),
            prefix: Some("logs/".into()),
            expiration_days: Some(90),
            noncurrent_expiration_days: Some(7),
            transitions: vec![
                TransitionRule {
                    days: 30,
                    storage_class: "STANDARD_IA".into(),
                },
                TransitionRule {
                    days: 60,
                    storage_class: "GLACIER".into(),
                },
            ],
            abort_incomplete_multipart_days: Some(7),
        }
    }

    fn run(objects: Vec<Object>, rules: Vec<Rule>) -> Vec<LifecycleAction> {
        evaluate_lifecycle(&LifecycleRequest {
            now: Some("2026-11-15T00:00:00Z".into()),
            rules,
            objects,
            multipart_uploads: vec![],
        })
        .unwrap()
        .actions
    }

    #[test]
    fn lifecycle_rules() {
        let a = run(
            vec![
                obj("logs/new.txt", true, "2026-11-01T00:00:00Z", "STANDARD"),
                obj("logs/a.txt", true, "2026-10-01T00:00:00Z", "STANDARD"),
                obj("logs/b.txt", true, "2026-09-01T00:00:00Z", "STANDARD"),
                obj("logs/c.txt", true, "2026-06-01T00:00:00Z", "STANDARD"),
                obj("data/d.txt", true, "2020-01-01T00:00:00Z", "STANDARD"),
                obj("logs/g.txt", true, "2026-09-01T00:00:00Z", "GLACIER"),
            ],
            vec![rule()],
        );
        let summary: Vec<(String, String, Option<String>)> = a
            .iter()
            .map(|x| {
                (
                    x.action_type.clone(),
                    x.key.clone(),
                    x.storage_class.clone(),
                )
            })
            .collect();
        assert_eq!(
            summary,
            vec![
                (
                    "transition".into(),
                    "logs/a.txt".into(),
                    Some("STANDARD_IA".into())
                ),
                (
                    "transition".into(),
                    "logs/b.txt".into(),
                    Some("GLACIER".into())
                ),
                ("expire".into(), "logs/c.txt".into(), None),
            ]
        );
    }

    #[test]
    fn noncurrent_and_disabled_and_multipart() {
        let mut o = obj("logs/x", false, "2026-01-01T00:00:00Z", "STANDARD");
        o.noncurrent_since = Some("2026-11-10T00:00:00Z".into());
        assert!(run(vec![o.clone()], vec![rule()]).is_empty());
        o.noncurrent_since = Some("2026-11-01T00:00:00Z".into());
        let a = run(vec![o.clone()], vec![rule()]);
        assert_eq!(a[0].action_type, "expire_noncurrent");
        let mut r = rule();
        r.status = "Disabled".into();
        assert!(run(vec![o], vec![r]).is_empty());

        let res = evaluate_lifecycle(&LifecycleRequest {
            now: Some("2026-11-15T00:00:00Z".into()),
            rules: vec![rule()],
            objects: vec![],
            multipart_uploads: vec![
                MultipartUpload {
                    upload_id: "u1".into(),
                    key: "logs/big".into(),
                    initiated: "2026-11-01T00:00:00Z".into(),
                },
                MultipartUpload {
                    upload_id: "u2".into(),
                    key: "logs/big2".into(),
                    initiated: "2026-11-14T00:00:00Z".into(),
                },
            ],
        })
        .unwrap();
        assert_eq!(res.actions.len(), 1);
        assert_eq!(res.actions[0].upload_id.as_deref(), Some("u1"));
    }

    #[test]
    fn latency_model() {
        let r = simulate(&SimRequest {
            op: "put".into(),
            size_bytes: 100_000_000,
            storage_class: None,
        })
        .unwrap();
        assert_eq!(r.latency_ms, 1010.0);
        let r = simulate(&SimRequest {
            op: "get".into(),
            size_bytes: 0,
            storage_class: Some("GLACIER".into()),
        })
        .unwrap();
        assert_eq!(r.latency_ms, 3_600_000.0);
        assert!(simulate(&SimRequest {
            op: "delete".into(),
            size_bytes: 0,
            storage_class: None
        })
        .is_err());
    }
}
