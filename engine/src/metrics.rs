//! Workload profiles → per-tick instance metrics (pure, seeded).

use rand::Rng;
use serde::Serialize;

use crate::registry::{Profile, Workload};
use crate::util::{round_to, SeedMix};

/// Spiky profile: a burst lasts `SPIKY_BURST_TICKS` out of every `SPIKY_PERIOD_TICKS`.
pub const SPIKY_PERIOD_TICKS: u64 = 10;
pub const SPIKY_BURST_TICKS: u64 = 2;

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Sample {
    pub resource_id: String,
    pub owner: String,
    pub cpu_percent: f64,
    pub memory_percent: f64,
    pub network_in_bytes: u64,
    pub network_out_bytes: u64,
    pub disk_read_bytes: u64,
    pub disk_write_bytes: u64,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct MetricsSnapshot {
    pub tick: u64,
    pub ts: String,
    pub samples: Vec<Sample>,
}

/// Compute one sample for a running instance. Deterministic in (seed, id, tick, workload).
pub fn compute_sample(seed: u64, tick: u64, id: &str, owner: &str, wl: &Workload) -> Sample {
    let mut rng = SeedMix::new(seed).str("metrics").str(id).u64(tick).rng();
    let phase = SeedMix::new(seed).str("phase").str(id).value() % SPIKY_PERIOD_TICKS;

    let base_cpu: f64 = match wl.profile {
        Profile::Idle => rng.gen_range(2.0..=5.0),
        Profile::Steady => rng.gen_range(20.0..=35.0),
        Profile::Spiky => {
            if (tick + phase) % SPIKY_PERIOD_TICKS < SPIKY_BURST_TICKS {
                rng.gen_range(80.0..=95.0)
            } else {
                15.0 + rng.gen_range(-3.0..=3.0)
            }
        }
        Profile::Stress => rng.gen_range(90.0..=100.0),
        Profile::Custom => wl.cpu_target.unwrap_or(50.0) + rng.gen_range(-3.0..=3.0),
    };
    let cpu = (base_cpu + wl.extra_load_rps.max(0.0) * 0.5).clamp(0.0, 100.0);

    let mem_base = match wl.profile {
        Profile::Idle => 10.0,
        Profile::Steady => 40.0,
        Profile::Spiky => 45.0,
        Profile::Stress => 85.0,
        Profile::Custom => 20.0 + wl.cpu_target.unwrap_or(50.0) * 0.5,
    };
    let memory = (mem_base + rng.gen_range(-2.0..=2.0)).clamp(0.0, 100.0);

    let net_noise: f64 = rng.gen_range(0.9..=1.1);
    let network_in = (cpu * 1_200.0 + wl.extra_load_rps.max(0.0) * 2_048.0) * net_noise;
    let network_out = network_in * 0.7;
    let disk_noise: f64 = rng.gen_range(0.9..=1.1);
    let disk_read = cpu * 400.0 * disk_noise;
    let disk_write = cpu * 250.0 * disk_noise;

    Sample {
        resource_id: id.to_string(),
        owner: owner.to_string(),
        cpu_percent: round_to(cpu, 1),
        memory_percent: round_to(memory, 1),
        network_in_bytes: network_in.round() as u64,
        network_out_bytes: network_out.round() as u64,
        disk_read_bytes: disk_read.round() as u64,
        disk_write_bytes: disk_write.round() as u64,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn wl(p: Profile) -> Workload {
        Workload {
            profile: p,
            cpu_target: None,
            extra_load_rps: 0.0,
        }
    }

    #[test]
    fn profiles_stay_in_band() {
        for tick in 0..200 {
            let s = compute_sample(42, tick, "i-1", "o", &wl(Profile::Idle));
            assert!((2.0..=5.0).contains(&s.cpu_percent), "{s:?}");
            let s = compute_sample(42, tick, "i-1", "o", &wl(Profile::Steady));
            assert!((20.0..=35.0).contains(&s.cpu_percent));
            let s = compute_sample(42, tick, "i-1", "o", &wl(Profile::Stress));
            assert!((90.0..=100.0).contains(&s.cpu_percent));
            assert!((83.0..=87.0).contains(&s.memory_percent));
        }
    }

    #[test]
    fn spiky_has_bursts() {
        let cpus: Vec<f64> = (0..20)
            .map(|t| compute_sample(42, t, "i-1", "o", &wl(Profile::Spiky)).cpu_percent)
            .collect();
        assert_eq!(cpus.iter().filter(|c| **c >= 80.0).count(), 4);
        assert!(cpus
            .iter()
            .all(|c| *c >= 80.0 || (*c >= 12.0 && *c <= 18.0)));
    }

    #[test]
    fn custom_and_extra_load() {
        let w = Workload {
            profile: Profile::Custom,
            cpu_target: Some(60.0),
            extra_load_rps: 0.0,
        };
        let s = compute_sample(1, 1, "i", "o", &w);
        assert!((57.0..=63.0).contains(&s.cpu_percent));
        let w = Workload {
            profile: Profile::Idle,
            cpu_target: None,
            extra_load_rps: 1000.0,
        };
        assert_eq!(compute_sample(1, 1, "i", "o", &w).cpu_percent, 100.0);
        let w = Workload {
            profile: Profile::Idle,
            cpu_target: None,
            extra_load_rps: 20.0,
        };
        let s = compute_sample(1, 1, "i", "o", &w);
        assert!((12.0..=15.0).contains(&s.cpu_percent));
    }

    #[test]
    fn deterministic() {
        let a = compute_sample(9, 3, "i-1", "o", &wl(Profile::Steady));
        let b = compute_sample(9, 3, "i-1", "o", &wl(Profile::Steady));
        assert_eq!(a, b);
    }
}
