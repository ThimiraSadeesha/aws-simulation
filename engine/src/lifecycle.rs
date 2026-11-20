//! Resource lifecycle state machines as pure functions.
//!
//! `apply_action(kind, state, action)` returns the transition to perform (the immediate
//! target state plus an optional engine-driven completion after a delay). Nothing in this
//! module touches shared state or time.

use std::fmt;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Class {
    Instance,
    NatGateway,
    LoadBalancer,
    DbInstance,
    /// Every other kind (`vpc`, `subnet`, `bucket`, ...): `available` immediately,
    /// `delete` → `deleted` immediately.
    Simple,
}

pub fn class_of(kind: &str) -> Class {
    match kind {
        "instance" => Class::Instance,
        "nat_gateway" => Class::NatGateway,
        "load_balancer" => Class::LoadBalancer,
        "db_instance" => Class::DbInstance,
        _ => Class::Simple,
    }
}

/// An engine-driven completion: after `delay_ms` (scaled by `ENGINE_TIME_SCALE`)
/// the resource moves to `to` with `reason`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Completion {
    pub to: &'static str,
    pub delay_ms: u64,
    pub reason: &'static str,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Transition {
    /// State entered immediately.
    pub to: &'static str,
    /// Completion scheduled after entering `to` (for transitional states).
    pub completion: Option<Completion>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum TransitionError {
    UnsupportedAction {
        kind: String,
        action: String,
    },
    InvalidState {
        kind: String,
        action: String,
        state: String,
    },
}

impl fmt::Display for TransitionError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            TransitionError::UnsupportedAction { kind, action } => {
                write!(f, "action '{action}' is not supported for kind '{kind}'")
            }
            TransitionError::InvalidState {
                kind,
                action,
                state,
            } => write!(f, "cannot '{action}' a {kind} in state '{state}'"),
        }
    }
}

impl std::error::Error for TransitionError {}

const fn c(to: &'static str, delay_ms: u64, reason: &'static str) -> Completion {
    Completion {
        to,
        delay_ms,
        reason,
    }
}

/// Unspecified by the protocol; chosen to be in line with the other DB timings.
pub const DB_STOP_MS: u64 = 3000;
pub const DB_START_MS: u64 = 4000;
pub const DB_DELETE_MS: u64 = 3000;

/// Initial state (and initial completion) for a newly created resource.
pub fn initial(kind: &str) -> Transition {
    let to = match class_of(kind) {
        Class::Instance => "pending",
        Class::NatGateway => "pending",
        Class::LoadBalancer => "provisioning",
        Class::DbInstance => "creating",
        Class::Simple => "available",
    };
    Transition {
        to,
        completion: completion_for(kind, to),
    }
}

/// Completion scheduled for a resource sitting in `state`, if `state` is transitional.
/// Also used to re-schedule completions when a resource is restored in a transitional state.
pub fn completion_for(kind: &str, state: &str) -> Option<Completion> {
    match (class_of(kind), state) {
        (Class::Instance, "pending") => Some(c("running", 3000, "Instance boot completed")),
        (Class::Instance, "stopping") => Some(c("stopped", 2000, "Instance stop completed")),
        (Class::Instance, "rebooting") => Some(c("running", 2000, "Instance reboot completed")),
        (Class::Instance, "shutting-down") => {
            Some(c("terminated", 2000, "Instance termination completed"))
        }
        (Class::NatGateway, "pending") => Some(c("available", 2000, "NAT gateway is available")),
        (Class::NatGateway, "deleting") => Some(c("deleted", 1500, "NAT gateway deleted")),
        (Class::LoadBalancer, "provisioning") => {
            Some(c("active", 2000, "Load balancer provisioning completed"))
        }
        (Class::LoadBalancer, "deleting") => Some(c("deleted", 1000, "Load balancer deleted")),
        (Class::DbInstance, "creating") => {
            Some(c("available", 4000, "DB instance creation completed"))
        }
        (Class::DbInstance, "stopping") => {
            Some(c("stopped", DB_STOP_MS, "DB instance stop completed"))
        }
        (Class::DbInstance, "starting") => {
            Some(c("available", DB_START_MS, "DB instance start completed"))
        }
        (Class::DbInstance, "deleting") => Some(c("deleted", DB_DELETE_MS, "DB instance deleted")),
        _ => None,
    }
}

/// `true` for states that no longer count against quotas or dependency checks.
pub fn is_terminal(state: &str) -> bool {
    state == "terminated" || state == "deleted"
}

/// Validate `action` against `(kind, state)` and return the transition to perform.
pub fn apply_action(kind: &str, state: &str, action: &str) -> Result<Transition, TransitionError> {
    let invalid = || TransitionError::InvalidState {
        kind: kind.to_string(),
        action: action.to_string(),
        state: state.to_string(),
    };
    let require = |ok: bool, to: &'static str| if ok { Ok(to) } else { Err(invalid()) };

    let to: &'static str = match (class_of(kind), action) {
        (Class::Instance, "stop") => require(state == "running", "stopping")?,
        (Class::Instance, "start") => require(state == "stopped", "pending")?,
        (Class::Instance, "reboot") => require(state == "running", "rebooting")?,
        (Class::Instance, "terminate") => require(state != "terminated", "shutting-down")?,

        (Class::NatGateway, "delete") => {
            require(state == "available" || state == "pending", "deleting")?
        }

        (Class::LoadBalancer, "delete") => {
            require(state != "deleted" && state != "deleting", "deleting")?
        }

        (Class::DbInstance, "stop") => require(state == "available", "stopping")?,
        (Class::DbInstance, "start") => require(state == "stopped", "starting")?,
        (Class::DbInstance, "delete") => {
            require(state != "deleted" && state != "deleting", "deleting")?
        }

        (Class::Simple, "delete") => require(state != "deleted", "deleted")?,

        _ => {
            return Err(TransitionError::UnsupportedAction {
                kind: kind.to_string(),
                action: action.to_string(),
            })
        }
    };
    Ok(Transition {
        to,
        completion: completion_for(kind, to),
    })
}

pub fn default_action_reason(action: &str) -> String {
    format!("User initiated ({action})")
}

#[cfg(test)]
mod tests {
    use super::*;

    fn to(kind: &str, state: &str, action: &str) -> Result<&'static str, TransitionError> {
        apply_action(kind, state, action).map(|t| t.to)
    }

    #[test]
    fn initial_states() {
        assert_eq!(initial("instance").to, "pending");
        assert_eq!(initial("instance").completion.unwrap().to, "running");
        assert_eq!(initial("instance").completion.unwrap().delay_ms, 3000);
        assert_eq!(initial("nat_gateway").completion.unwrap().delay_ms, 2000);
        assert_eq!(initial("load_balancer").to, "provisioning");
        assert_eq!(initial("db_instance").to, "creating");
        assert_eq!(initial("db_instance").completion.unwrap().delay_ms, 4000);
        assert_eq!(initial("vpc").to, "available");
        assert!(initial("bucket").completion.is_none());
    }

    #[test]
    fn instance_machine() {
        assert_eq!(to("instance", "running", "stop").unwrap(), "stopping");
        assert_eq!(
            apply_action("instance", "running", "stop")
                .unwrap()
                .completion
                .unwrap(),
            Completion {
                to: "stopped",
                delay_ms: 2000,
                reason: "Instance stop completed"
            }
        );
        assert_eq!(to("instance", "stopped", "start").unwrap(), "pending");
        assert_eq!(to("instance", "running", "reboot").unwrap(), "rebooting");
        for s in [
            "pending",
            "running",
            "stopping",
            "stopped",
            "rebooting",
            "shutting-down",
        ] {
            assert_eq!(to("instance", s, "terminate").unwrap(), "shutting-down");
        }
        assert!(matches!(
            to("instance", "terminated", "terminate"),
            Err(TransitionError::InvalidState { .. })
        ));
        assert!(to("instance", "stopped", "stop").is_err());
        assert!(to("instance", "running", "start").is_err());
        assert!(to("instance", "stopped", "reboot").is_err());
        assert!(matches!(
            to("instance", "running", "explode"),
            Err(TransitionError::UnsupportedAction { .. })
        ));
    }

    #[test]
    fn nat_lb_db_machines() {
        assert_eq!(
            to("nat_gateway", "available", "delete").unwrap(),
            "deleting"
        );
        assert_eq!(to("nat_gateway", "pending", "delete").unwrap(), "deleting");
        assert!(to("nat_gateway", "deleted", "delete").is_err());
        assert_eq!(
            apply_action("nat_gateway", "available", "delete")
                .unwrap()
                .completion
                .unwrap()
                .delay_ms,
            1500
        );
        assert_eq!(to("load_balancer", "active", "delete").unwrap(), "deleting");
        assert_eq!(
            to("load_balancer", "provisioning", "delete").unwrap(),
            "deleting"
        );
        assert!(to("load_balancer", "deleted", "delete").is_err());
        assert_eq!(to("db_instance", "available", "stop").unwrap(), "stopping");
        assert_eq!(to("db_instance", "stopped", "start").unwrap(), "starting");
        assert_eq!(to("db_instance", "stopped", "delete").unwrap(), "deleting");
        assert!(to("db_instance", "creating", "stop").is_err());
    }

    #[test]
    fn simple_kinds() {
        let t = apply_action("vpc", "available", "delete").unwrap();
        assert_eq!(t.to, "deleted");
        assert!(t.completion.is_none());
        assert!(to("vpc", "deleted", "delete").is_err());
        assert!(to("bucket", "available", "stop").is_err());
    }

    #[test]
    fn transitional_restore_and_terminal() {
        assert!(completion_for("instance", "stopping").is_some());
        assert!(completion_for("instance", "running").is_none());
        assert!(completion_for("vpc", "pending").is_none());
        assert!(is_terminal("terminated"));
        assert!(is_terminal("deleted"));
        assert!(!is_terminal("stopped"));
    }
}
