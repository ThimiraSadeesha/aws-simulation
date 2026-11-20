//! Small shared helpers: timestamps, deterministic seed derivation, rounding.

use chrono::{DateTime, SecondsFormat, Utc};
use rand::SeedableRng;
use rand_chacha::ChaCha8Rng;

pub fn now() -> DateTime<Utc> {
    Utc::now()
}

/// RFC 3339 with millisecond precision and `Z` suffix, e.g. `2026-11-15T10:00:00.000Z`.
pub fn fmt_ts(t: &DateTime<Utc>) -> String {
    t.to_rfc3339_opts(SecondsFormat::Millis, true)
}

pub fn parse_ts(s: &str) -> Result<DateTime<Utc>, String> {
    DateTime::parse_from_rfc3339(s)
        .map(|d| d.with_timezone(&Utc))
        .map_err(|e| format!("invalid RFC 3339 timestamp {s:?}: {e}"))
}

pub fn round_to(x: f64, decimals: i32) -> f64 {
    let f = 10f64.powi(decimals);
    (x * f).round() / f
}

fn splitmix64(mut z: u64) -> u64 {
    z = z.wrapping_add(0x9E37_79B9_7F4A_7C15);
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
    z ^ (z >> 31)
}

/// Deterministic seed derivation. Every pseudo-random decision in the engine derives
/// its own RNG from `ENGINE_SEED` plus stable inputs (resource id, tick, ...), so results
/// do not depend on scheduling order or hash-map iteration order.
#[derive(Clone, Copy, Debug)]
pub struct SeedMix(u64);

impl SeedMix {
    pub fn new(seed: u64) -> Self {
        SeedMix(splitmix64(seed))
    }

    pub fn str(self, s: &str) -> Self {
        let mut h: u64 = 0xcbf2_9ce4_8422_2325 ^ self.0;
        for b in s.bytes() {
            h ^= u64::from(b);
            h = h.wrapping_mul(0x0000_0100_0000_01b3);
        }
        SeedMix(splitmix64(h ^ (s.len() as u64)))
    }

    pub fn u64(self, v: u64) -> Self {
        SeedMix(splitmix64(self.0 ^ splitmix64(v)))
    }

    pub fn value(self) -> u64 {
        self.0
    }

    pub fn rng(self) -> ChaCha8Rng {
        ChaCha8Rng::seed_from_u64(self.0)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rand::Rng;

    #[test]
    fn seed_mix_is_deterministic_and_input_sensitive() {
        let a = SeedMix::new(42).str("x").u64(1).value();
        let b = SeedMix::new(42).str("x").u64(1).value();
        let c = SeedMix::new(42).str("y").u64(1).value();
        let d = SeedMix::new(43).str("x").u64(1).value();
        assert_eq!(a, b);
        assert_ne!(a, c);
        assert_ne!(a, d);
        let r1: f64 = SeedMix::new(1).rng().gen();
        let r2: f64 = SeedMix::new(1).rng().gen();
        assert_eq!(r1, r2);
    }

    #[test]
    fn timestamp_format() {
        let t = parse_ts("2026-11-15T10:00:00Z").unwrap();
        assert_eq!(fmt_ts(&t), "2026-11-15T10:00:00.000Z");
        assert!(parse_ts("yesterday").is_err());
    }
}
