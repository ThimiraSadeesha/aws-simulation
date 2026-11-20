//! Network simulation: IPv4 CIDR utilities and VPC reachability evaluation.

pub mod cidr;
pub mod reachability;

pub use cidr::{cidr_op, Cidr, CidrRequest, CidrResponse};
pub use reachability::{
    evaluate as evaluate_reachability, ReachabilityRequest, ReachabilityResult,
};
