//! Auto Scaling group decisions (pure).

use std::cmp::Ordering;

use serde::{Deserialize, Serialize};

/// Scale-in only happens when average CPU is below this fraction of the target.
pub const SCALE_IN_HYSTERESIS: f64 = 0.8;

#[derive(Debug, Clone, Deserialize)]
pub struct Group {
    #[serde(default)]
    pub name: String,
    pub min: u32,
    pub max: u32,
    pub desired: u32,
    #[serde(default)]
    pub cooldown_s: f64,
    #[serde(default)]
    pub seconds_since_last_activity: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Policy {
    #[serde(default)]
    pub name: String,
    #[serde(rename = "type")]
    pub policy_type: String,
    #[serde(default)]
    pub target_value: Option<f64>,
    #[serde(default)]
    pub adjustment: Option<i64>,
    #[serde(default)]
    pub threshold: Option<f64>,
    #[serde(default)]
    pub comparison: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct AsgInstance {
    pub id: String,
    pub state: String,
    #[serde(default)]
    pub cpu_percent: Option<f64>,
    #[serde(default = "yes")]
    pub healthy: bool,
}

fn yes() -> bool {
    true
}

#[derive(Debug, Clone, Deserialize)]
pub struct EvaluateRequest {
    pub group: Group,
    #[serde(default)]
    pub policies: Vec<Policy>,
    #[serde(default)]
    pub instances: Vec<AsgInstance>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Decision {
    pub desired: u32,
    pub change: i64,
    pub reason: String,
    pub replace: Vec<String>,
}

fn compare(op: &str, value: f64, threshold: f64) -> Result<bool, String> {
    Ok(match op {
        ">=" | "GreaterThanOrEqualToThreshold" => value >= threshold,
        ">" | "GreaterThanThreshold" => value > threshold,
        "<=" | "LessThanOrEqualToThreshold" => value <= threshold,
        "<" | "LessThanThreshold" => value < threshold,
        other => return Err(format!("unknown comparison {other:?}")),
    })
}

struct Candidate {
    desired: i64,
    text: String,
}

fn validate(req: &EvaluateRequest) -> Result<(), String> {
    let g = &req.group;
    if g.min > g.max {
        return Err(format!(
            "group.min ({}) must be <= group.max ({})",
            g.min, g.max
        ));
    }
    for p in &req.policies {
        match p.policy_type.as_str() {
            "target_tracking" => {
                if !p.target_value.is_some_and(|t| t > 0.0 && t.is_finite()) {
                    return Err(format!("policy {}: target_value must be > 0", p.name));
                }
            }
            "step" => {
                if p.adjustment.is_none() || p.threshold.is_none() {
                    return Err(format!(
                        "policy {}: step policies need adjustment and threshold",
                        p.name
                    ));
                }
                compare(p.comparison.as_deref().unwrap_or(">="), 0.0, 0.0)?;
            }
            other => return Err(format!("policy {}: unknown type {other:?}", p.name)),
        }
    }
    Ok(())
}

fn clamp_note(raw: i64, clamped: u32, min: u32, max: u32) -> String {
    match raw.cmp(&i64::from(clamped)) {
        Ordering::Greater => format!(" (capped by max {max})"),
        Ordering::Less => format!(" (raised to min {min})"),
        Ordering::Equal => String::new(),
    }
}

pub fn evaluate(req: &EvaluateRequest) -> Result<Decision, String> {
    validate(req)?;
    let g = &req.group;
    let clamp = |v: i64| v.clamp(i64::from(g.min), i64::from(g.max)) as u32;

    let replace: Vec<String> = req
        .instances
        .iter()
        .filter(|i| !i.healthy || (i.state != "running" && i.state != "pending"))
        .map(|i| i.id.clone())
        .collect();

    let current = i64::from(g.desired);
    let finish = |raw: i64, reason: String| {
        let desired = clamp(raw);
        Decision {
            desired,
            change: i64::from(desired) - current,
            reason,
            replace: replace.clone(),
        }
    };

    if let Some(since) = g.seconds_since_last_activity {
        if since < g.cooldown_s {
            let d = clamp(current);
            return Ok(finish(
                current,
                format!(
                    "In cooldown: {since:.0}s since last scaling activity < cooldown {:.0}s; no scaling{}",
                    g.cooldown_s,
                    clamp_note(current, d, g.min, g.max)
                ),
            ));
        }
    }

    let running: Vec<&AsgInstance> = req
        .instances
        .iter()
        .filter(|i| i.state == "running")
        .collect();
    let cpus: Vec<f64> = running.iter().filter_map(|i| i.cpu_percent).collect();
    if cpus.is_empty() {
        let d = clamp(current);
        return Ok(finish(
            current,
            format!(
                "No running instances reporting CPU; no policy evaluated{}",
                clamp_note(current, d, g.min, g.max)
            ),
        ));
    }
    let avg = cpus.iter().sum::<f64>() / cpus.len() as f64;
    let n_running = running.len() as f64;

    let mut ups: Vec<Candidate> = Vec::new();
    let mut downs: Vec<Candidate> = Vec::new();
    let mut holds: Vec<String> = Vec::new();
    for p in &req.policies {
        match p.policy_type.as_str() {
            "target_tracking" => {
                let target = p.target_value.unwrap_or(1.0);
                let raw = (n_running * avg / target).ceil() as i64;
                match raw.cmp(&current) {
                    Ordering::Greater => ups.push(Candidate {
                        desired: raw,
                        text: format!(
                            "Policy {}: average CPU {avg:.1}% > target {target:.1}%",
                            p.name
                        ),
                    }),
                    Ordering::Less if avg < SCALE_IN_HYSTERESIS * target => {
                        downs.push(Candidate {
                            desired: raw,
                            text: format!(
                                "Policy {}: average CPU {avg:.1}% < {:.1}% (80% of target {target:.1}%)",
                                p.name,
                                SCALE_IN_HYSTERESIS * target
                            ),
                        });
                    }
                    Ordering::Less => holds.push(format!(
                        "Policy {}: average CPU {avg:.1}% within hysteresis band of target {target:.1}%",
                        p.name
                    )),
                    Ordering::Equal => holds.push(format!(
                        "Policy {}: average CPU {avg:.1}% on target {target:.1}%",
                        p.name
                    )),
                }
            }
            "step" => {
                let th = p.threshold.unwrap_or(0.0);
                let op = p.comparison.as_deref().unwrap_or(">=");
                let adj = p.adjustment.unwrap_or(0);
                if compare(op, avg, th)? && adj != 0 {
                    let c = Candidate {
                        desired: (current + adj).max(0),
                        text: format!(
                            "Policy {}: average CPU {avg:.1}% {op} threshold {th:.1}%, adjustment {adj:+}",
                            p.name
                        ),
                    };
                    if adj > 0 {
                        ups.push(c);
                    } else {
                        downs.push(c);
                    }
                }
            }
            _ => {}
        }
    }

    // Scale-out wins (largest capacity); otherwise the most conservative scale-in.
    let chosen = if !ups.is_empty() {
        ups.into_iter().max_by_key(|c| c.desired)
    } else {
        downs.into_iter().max_by_key(|c| c.desired)
    };
    Ok(match chosen {
        Some(c) => {
            let d = clamp(c.desired);
            let verb = if c.desired > current {
                "scaling to"
            } else {
                "scaling in to"
            };
            finish(
                c.desired,
                format!(
                    "{}, {verb} {d}{}",
                    c.text,
                    clamp_note(c.desired, d, g.min, g.max)
                ),
            )
        }
        None => {
            let d = clamp(current);
            let why = if holds.is_empty() {
                format!("No policy triggered (average CPU {avg:.1}%)")
            } else {
                holds.join("; ")
            };
            finish(
                current,
                format!("{why}; no scaling{}", clamp_note(current, d, g.min, g.max)),
            )
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn inst(id: &str, state: &str, cpu: f64, healthy: bool) -> AsgInstance {
        AsgInstance {
            id: id.into(),
            state: state.into(),
            cpu_percent: Some(cpu),
            healthy,
        }
    }

    fn tt(target: f64) -> Policy {
        Policy {
            name: "cpu50".into(),
            policy_type: "target_tracking".into(),
            target_value: Some(target),
            adjustment: None,
            threshold: None,
            comparison: None,
        }
    }

    fn group(min: u32, max: u32, desired: u32, since: Option<f64>) -> Group {
        Group {
            name: "web".into(),
            min,
            max,
            desired,
            cooldown_s: 60.0,
            seconds_since_last_activity: since,
        }
    }

    #[test]
    fn target_tracking_scale_out() {
        let d = evaluate(&EvaluateRequest {
            group: group(1, 10, 2, Some(120.0)),
            policies: vec![tt(50.0)],
            instances: vec![
                inst("i-1", "running", 90.0, true),
                inst("i-2", "running", 90.0, true),
            ],
        })
        .unwrap();
        assert_eq!(d.desired, 4);
        assert_eq!(d.change, 2);
        assert!(d
            .reason
            .starts_with("Policy cpu50: average CPU 90.0% > target 50.0%, scaling to 4"));
    }

    #[test]
    fn clamp_and_replace() {
        let d = evaluate(&EvaluateRequest {
            group: group(1, 3, 2, None),
            policies: vec![tt(10.0)],
            instances: vec![
                inst("i-1", "running", 90.0, true),
                inst("i-2", "running", 90.0, false),
                inst("i-3", "stopped", 0.0, true),
                inst("i-4", "pending", 0.0, true),
            ],
        })
        .unwrap();
        assert_eq!(d.desired, 3);
        assert!(d.reason.contains("(capped by max 3)"));
        assert_eq!(d.replace, vec!["i-2", "i-3"]);
    }

    #[test]
    fn hysteresis_and_scale_in() {
        // avg 45 with target 50: ceil(4*45/50)=4 == desired → hold
        let req = |cpu: f64| EvaluateRequest {
            group: group(1, 10, 4, Some(600.0)),
            policies: vec![tt(50.0)],
            instances: (0..4)
                .map(|i| inst(&format!("i-{i}"), "running", cpu, true))
                .collect(),
        };
        assert_eq!(evaluate(&req(45.0)).unwrap().change, 0);
        // avg 42: raw ceil(3.36)=4 → hold
        assert_eq!(evaluate(&req(42.0)).unwrap().change, 0);
        // avg 30 < 40: raw ceil(2.4)=3 → scale in
        let d = evaluate(&req(30.0)).unwrap();
        assert_eq!(d.desired, 3);
        assert_eq!(d.change, -1);
        // avg 5: raw 1 → scale in to min 1
        assert_eq!(evaluate(&req(5.0)).unwrap().desired, 1);
    }

    #[test]
    fn cooldown_blocks_but_replaces() {
        let d = evaluate(&EvaluateRequest {
            group: group(1, 10, 2, Some(10.0)),
            policies: vec![tt(50.0)],
            instances: vec![inst("i-1", "running", 99.0, false)],
        })
        .unwrap();
        assert_eq!(d.change, 0);
        assert!(d.reason.contains("cooldown"));
        assert_eq!(d.replace, vec!["i-1"]);
    }

    #[test]
    fn step_policy() {
        let step = Policy {
            name: "step-up".into(),
            policy_type: "step".into(),
            target_value: None,
            adjustment: Some(1),
            threshold: Some(80.0),
            comparison: Some(">=".into()),
        };
        let d = evaluate(&EvaluateRequest {
            group: group(1, 4, 2, Some(120.0)),
            policies: vec![step],
            instances: vec![inst("i-1", "running", 91.0, true)],
        })
        .unwrap();
        assert_eq!(d.desired, 3);
        assert!(d.reason.starts_with("Policy step-up"));
    }

    #[test]
    fn invalid() {
        assert!(evaluate(&EvaluateRequest {
            group: group(5, 1, 2, None),
            policies: vec![],
            instances: vec![],
        })
        .is_err());
    }
}
