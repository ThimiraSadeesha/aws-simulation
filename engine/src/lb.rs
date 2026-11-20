//! Load balancer request-distribution simulation (pure, seeded).

use rand::Rng;
use serde::{Deserialize, Serialize};

use crate::util::{round_to, SeedMix};

pub const MAX_REQUESTS: u64 = 5_000_000;
/// Service time of an unloaded target.
pub const BASE_LATENCY_MS: f64 = 5.0;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Algorithm {
    RoundRobin,
    LeastOutstandingRequests,
    WeightedRandom,
}

#[derive(Debug, Clone, Deserialize)]
pub struct LbTarget {
    pub id: String,
    #[serde(default = "yes")]
    pub healthy: bool,
    #[serde(default = "one")]
    pub weight: f64,
    #[serde(default)]
    pub capacity_rps: Option<f64>,
}

fn yes() -> bool {
    true
}
fn one() -> f64 {
    1.0
}
fn sixty() -> f64 {
    60.0
}

#[derive(Debug, Clone, Deserialize)]
pub struct LbSimRequest {
    pub algorithm: Algorithm,
    pub requests: u64,
    #[serde(default)]
    pub targets: Vec<LbTarget>,
    #[serde(default)]
    pub seed: Option<u64>,
    #[serde(default)]
    pub apply_load: bool,
    #[serde(default = "sixty")]
    pub duration_s: f64,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct TargetStats {
    pub id: String,
    pub requests: u64,
    pub percent: f64,
    pub avg_latency_ms: f64,
    pub p99_latency_ms: f64,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct LbSimResult {
    pub total: u64,
    pub served: u64,
    pub dropped: u64,
    pub per_target: Vec<TargetStats>,
    pub avg_latency_ms: f64,
}

pub fn validate(req: &LbSimRequest) -> Result<(), String> {
    if req.requests > MAX_REQUESTS {
        return Err(format!("requests must be <= {MAX_REQUESTS}"));
    }
    if !(req.duration_s.is_finite() && req.duration_s > 0.0) {
        return Err("duration_s must be > 0".to_string());
    }
    for t in &req.targets {
        if !(t.weight.is_finite() && t.weight >= 0.0) {
            return Err(format!("target {}: weight must be >= 0", t.id));
        }
        if let Some(c) = t.capacity_rps {
            if !(c.is_finite() && c > 0.0) {
                return Err(format!("target {}: capacity_rps must be > 0", t.id));
            }
        }
    }
    Ok(())
}

/// Assign `n` requests to `targets` (all healthy). Returns the target index per request.
fn assign(
    alg: Algorithm,
    n: u64,
    targets: &[&LbTarget],
    duration_s: f64,
    rng: &mut impl Rng,
) -> Vec<u32> {
    let k = targets.len();
    let mut out = Vec::with_capacity(n as usize);
    match alg {
        Algorithm::RoundRobin => {
            for i in 0..n {
                out.push((i % k as u64) as u32);
            }
        }
        Algorithm::WeightedRandom => {
            let mut weights: Vec<f64> = targets.iter().map(|t| t.weight).collect();
            if weights.iter().all(|w| *w <= 0.0) {
                weights.iter_mut().for_each(|w| *w = 1.0);
            }
            let total: f64 = weights.iter().sum();
            let mut cumulative = Vec::with_capacity(k);
            let mut acc = 0.0;
            for w in &weights {
                acc += w;
                cumulative.push(acc);
            }
            for _ in 0..n {
                let x = rng.gen::<f64>() * total;
                let idx = cumulative.partition_point(|c| *c <= x).min(k - 1);
                out.push(idx as u32);
            }
        }
        Algorithm::LeastOutstandingRequests => {
            // Discrete simulation: requests arrive evenly over `duration_s`; each target
            // drains its outstanding queue at `capacity_rps`. The request goes to the
            // target with the fewest outstanding requests; ties rotate.
            let dt = if n > 0 { duration_s / n as f64 } else { 0.0 };
            let drain: Vec<f64> = targets
                .iter()
                .map(|t| t.capacity_rps.map_or(f64::INFINITY, |c| c * dt))
                .collect();
            let mut outstanding = vec![0.0f64; k];
            let mut rot = 0usize;
            for _ in 0..n {
                for (o, d) in outstanding.iter_mut().zip(&drain) {
                    *o = (*o - d).max(0.0);
                }
                let mut best = rot % k;
                for j in 0..k {
                    let idx = (rot + j) % k;
                    if outstanding[idx] < outstanding[best] {
                        best = idx;
                    }
                }
                outstanding[best] += 1.0;
                rot = (best + 1) % k;
                out.push(best as u32);
            }
        }
    }
    out
}

fn p99(samples: &mut [f64]) -> f64 {
    if samples.is_empty() {
        return 0.0;
    }
    let idx = ((samples.len() as f64) * 0.99).ceil() as usize;
    let idx = idx.clamp(1, samples.len()) - 1;
    let (_, v, _) = samples.select_nth_unstable_by(idx, |a, b| a.total_cmp(b));
    *v
}

/// Simulate distributing `req.requests` over the healthy targets.
pub fn simulate(req: &LbSimRequest, default_seed: u64) -> Result<LbSimResult, String> {
    validate(req)?;
    let healthy: Vec<&LbTarget> = req.targets.iter().filter(|t| t.healthy).collect();
    if healthy.is_empty() {
        return Ok(LbSimResult {
            total: req.requests,
            served: 0,
            dropped: req.requests,
            per_target: Vec::new(),
            avg_latency_ms: 0.0,
        });
    }
    let seed = req.seed.unwrap_or(default_seed);
    let mut rng = SeedMix::new(seed).str("lb").rng();
    let assignment = assign(
        req.algorithm,
        req.requests,
        &healthy,
        req.duration_s,
        &mut rng,
    );

    let mut counts = vec![0u64; healthy.len()];
    for a in &assignment {
        counts[*a as usize] += 1;
    }
    // Mean latency per target from utilisation (M/M/1-style 1/(1-u), u capped at 0.95).
    let means: Vec<f64> = healthy
        .iter()
        .zip(&counts)
        .map(|(t, c)| {
            let rps = *c as f64 / req.duration_s;
            let u = t.capacity_rps.map_or(0.0, |cap| rps / cap).min(0.95);
            BASE_LATENCY_MS / (1.0 - u)
        })
        .collect();
    let mut samples: Vec<Vec<f64>> = counts
        .iter()
        .map(|c| Vec::with_capacity(*c as usize))
        .collect();
    let mut total_latency = 0.0;
    for a in &assignment {
        let i = *a as usize;
        let mut l = means[i] * rng.gen_range(0.6..1.4);
        if rng.gen::<f64>() < 0.01 {
            l *= 3.0; // tail
        }
        total_latency += l;
        samples[i].push(l);
    }
    let per_target = healthy
        .iter()
        .zip(counts.iter())
        .zip(samples.iter_mut())
        .map(|((t, c), s)| {
            let avg = if s.is_empty() {
                0.0
            } else {
                s.iter().sum::<f64>() / s.len() as f64
            };
            TargetStats {
                id: t.id.clone(),
                requests: *c,
                percent: if req.requests == 0 {
                    0.0
                } else {
                    round_to(*c as f64 * 100.0 / req.requests as f64, 2)
                },
                avg_latency_ms: round_to(avg, 2),
                p99_latency_ms: round_to(p99(s), 2),
            }
        })
        .collect();
    Ok(LbSimResult {
        total: req.requests,
        served: req.requests,
        dropped: 0,
        per_target,
        avg_latency_ms: if req.requests == 0 {
            0.0
        } else {
            round_to(total_latency / req.requests as f64, 2)
        },
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn target(id: &str, healthy: bool, weight: f64, cap: Option<f64>) -> LbTarget {
        LbTarget {
            id: id.into(),
            healthy,
            weight,
            capacity_rps: cap,
        }
    }

    fn req(alg: Algorithm, n: u64, targets: Vec<LbTarget>, seed: Option<u64>) -> LbSimRequest {
        LbSimRequest {
            algorithm: alg,
            requests: n,
            targets,
            seed,
            apply_load: false,
            duration_s: 60.0,
        }
    }

    #[test]
    fn round_robin_even_split_skips_unhealthy() {
        let r = simulate(
            &req(
                Algorithm::RoundRobin,
                1000,
                vec![
                    target("a", true, 1.0, Some(100.0)),
                    target("b", false, 1.0, None),
                    target("c", true, 1.0, Some(100.0)),
                ],
                Some(7),
            ),
            42,
        )
        .unwrap();
        assert_eq!(r.per_target.len(), 2);
        assert_eq!(r.per_target[0].requests, 500);
        assert_eq!(r.per_target[1].requests, 500);
        assert_eq!(r.per_target[0].percent, 50.0);
        assert_eq!(r.served, 1000);
        assert!(r.avg_latency_ms > 0.0);
    }

    #[test]
    fn no_healthy_targets_drops_all() {
        let r = simulate(
            &req(
                Algorithm::RoundRobin,
                10,
                vec![target("a", false, 1.0, None)],
                None,
            ),
            1,
        )
        .unwrap();
        assert_eq!(r.dropped, 10);
        assert_eq!(r.served, 0);
    }

    #[test]
    fn weighted_random_respects_weights_and_seed() {
        let mk = || {
            req(
                Algorithm::WeightedRandom,
                10_000,
                vec![target("a", true, 3.0, None), target("b", true, 1.0, None)],
                Some(9),
            )
        };
        let r1 = simulate(&mk(), 1).unwrap();
        let r2 = simulate(&mk(), 1).unwrap();
        assert_eq!(r1, r2);
        let a = r1.per_target[0].requests as f64;
        assert!((7_200.0..7_800.0).contains(&a), "a={a}");
    }

    #[test]
    fn least_outstanding_favours_capacity() {
        let r = simulate(
            &req(
                Algorithm::LeastOutstandingRequests,
                60_000,
                vec![
                    target("big", true, 1.0, Some(900.0)),
                    target("small", true, 1.0, Some(300.0)),
                ],
                Some(3),
            ),
            1,
        )
        .unwrap();
        assert_eq!(r.per_target[0].requests + r.per_target[1].requests, 60_000);
        assert!(r.per_target[0].requests > r.per_target[1].requests);
        // Under light load it is an even rotation.
        let r = simulate(
            &req(
                Algorithm::LeastOutstandingRequests,
                1000,
                vec![target("a", true, 1.0, None), target("b", true, 1.0, None)],
                Some(3),
            ),
            1,
        )
        .unwrap();
        assert_eq!(r.per_target[0].requests, 500);
    }

    #[test]
    fn validation_errors() {
        let mut r = req(Algorithm::RoundRobin, 10, vec![], None);
        r.duration_s = 0.0;
        assert!(simulate(&r, 1).is_err());
        let r = req(Algorithm::RoundRobin, MAX_REQUESTS + 1, vec![], None);
        assert!(simulate(&r, 1).is_err());
    }

    #[test]
    fn p99_of_samples() {
        let mut v: Vec<f64> = (1..=100).map(f64::from).collect();
        assert_eq!(p99(&mut v), 99.0);
        assert_eq!(p99(&mut []), 0.0);
    }
}
