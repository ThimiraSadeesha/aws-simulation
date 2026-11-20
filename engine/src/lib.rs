//! CloudSim simulation engine.
//!
//! An in-memory, deterministic (seeded) simulator of AWS-style resource lifecycles,
//! VPC reachability, load balancing, auto scaling, object storage and fault injection.
//! It is driven exclusively by the Go backend over the HTTP protocol described in
//! `docs/engine-protocol.md`. It never executes shell commands or user-supplied code.

pub mod api;
pub mod autoscaling;
pub mod config;
pub mod engine;
pub mod error;
pub mod events;
pub mod faults;
pub mod healthcheck;
pub mod lb;
pub mod lifecycle;
pub mod metrics;
pub mod network;
pub mod registry;
mod scheduler;
pub mod storage;
pub mod util;

pub use api::router;
pub use config::Config;
pub use engine::Engine;
pub use error::EngineError;
