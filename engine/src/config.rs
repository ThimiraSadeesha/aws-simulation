//! Engine configuration loaded from environment variables (see `docs/engine-protocol.md`).

use std::net::SocketAddr;

pub const DEFAULT_ADDR: &str = "0.0.0.0:7070";

#[derive(Clone, Debug)]
pub struct Config {
    /// `ENGINE_ADDR` — listen address.
    pub addr: SocketAddr,
    /// `ENGINE_TOKEN` — shared secret, required and non-empty.
    pub token: String,
    /// `ENGINE_TIME_SCALE` — multiplier for all transition delays; `0` completes them immediately.
    pub time_scale: f64,
    /// `ENGINE_SEED` — seed for every pseudo-random decision.
    pub seed: u64,
    /// `ENGINE_TICK_MS` — metrics simulation tick.
    pub tick_ms: u64,
    /// `ENGINE_EVENT_BUFFER` — event ring-buffer capacity.
    pub event_buffer: usize,
}

impl Config {
    pub fn from_env() -> Result<Self, String> {
        Self::from_lookup(|k| std::env::var(k).ok())
    }

    /// Build a config from an arbitrary key lookup (used by tests).
    pub fn from_lookup(get: impl Fn(&str) -> Option<String>) -> Result<Self, String> {
        let non_empty = |k: &str| {
            get(k)
                .map(|v| v.trim().to_string())
                .filter(|v| !v.is_empty())
        };

        let addr_raw = non_empty("ENGINE_ADDR").unwrap_or_else(|| DEFAULT_ADDR.to_string());
        let addr: SocketAddr = addr_raw
            .parse()
            .map_err(|e| format!("invalid ENGINE_ADDR {addr_raw:?}: {e}"))?;

        let token = get("ENGINE_TOKEN").unwrap_or_default();
        if token.trim().is_empty() {
            return Err("ENGINE_TOKEN must be set to a non-empty value".to_string());
        }

        let time_scale = match non_empty("ENGINE_TIME_SCALE") {
            None => 1.0,
            Some(v) => v
                .parse::<f64>()
                .map_err(|e| format!("invalid ENGINE_TIME_SCALE {v:?}: {e}"))?,
        };
        if !time_scale.is_finite() || time_scale < 0.0 {
            return Err(format!(
                "ENGINE_TIME_SCALE must be a finite number >= 0, got {time_scale}"
            ));
        }

        let seed = match non_empty("ENGINE_SEED") {
            None => 42,
            Some(v) => v
                .parse::<u64>()
                .map_err(|e| format!("invalid ENGINE_SEED {v:?}: {e}"))?,
        };

        let tick_ms = match non_empty("ENGINE_TICK_MS") {
            None => 1000,
            Some(v) => v
                .parse::<u64>()
                .map_err(|e| format!("invalid ENGINE_TICK_MS {v:?}: {e}"))?,
        };
        if tick_ms == 0 {
            return Err("ENGINE_TICK_MS must be >= 1".to_string());
        }

        let event_buffer = match non_empty("ENGINE_EVENT_BUFFER") {
            None => 10_000,
            Some(v) => v
                .parse::<usize>()
                .map_err(|e| format!("invalid ENGINE_EVENT_BUFFER {v:?}: {e}"))?,
        };
        if event_buffer == 0 {
            return Err("ENGINE_EVENT_BUFFER must be >= 1".to_string());
        }

        Ok(Config {
            addr,
            token,
            time_scale,
            seed,
            tick_ms,
            event_buffer,
        })
    }

    /// Convenience config for tests and benches: time scale 0, seed 42.
    pub fn for_tests(token: &str) -> Self {
        Config {
            addr: "127.0.0.1:0".parse().expect("static addr"),
            token: token.to_string(),
            time_scale: 0.0,
            seed: 42,
            tick_ms: 1000,
            event_buffer: 10_000,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    fn cfg(pairs: &[(&str, &str)]) -> Result<Config, String> {
        let m: HashMap<String, String> = pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect();
        Config::from_lookup(|k| m.get(k).cloned())
    }

    #[test]
    fn defaults_apply() {
        let c = cfg(&[("ENGINE_TOKEN", "s3cret")]).unwrap();
        assert_eq!(c.addr.to_string(), "0.0.0.0:7070");
        assert_eq!(c.time_scale, 1.0);
        assert_eq!(c.seed, 42);
        assert_eq!(c.tick_ms, 1000);
        assert_eq!(c.event_buffer, 10_000);
    }

    #[test]
    fn empty_token_is_rejected() {
        assert!(cfg(&[]).is_err());
        assert!(cfg(&[("ENGINE_TOKEN", "   ")]).is_err());
    }

    #[test]
    fn invalid_values_are_rejected() {
        assert!(cfg(&[("ENGINE_TOKEN", "t"), ("ENGINE_TIME_SCALE", "-1")]).is_err());
        assert!(cfg(&[("ENGINE_TOKEN", "t"), ("ENGINE_SEED", "abc")]).is_err());
        assert!(cfg(&[("ENGINE_TOKEN", "t"), ("ENGINE_TICK_MS", "0")]).is_err());
        assert!(cfg(&[("ENGINE_TOKEN", "t"), ("ENGINE_ADDR", "nope")]).is_err());
        let c = cfg(&[("ENGINE_TOKEN", "t"), ("ENGINE_TIME_SCALE", "0")]).unwrap();
        assert_eq!(c.time_scale, 0.0);
    }
}
