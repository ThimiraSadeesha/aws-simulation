//! Engine-level error type. The HTTP layer maps it to status codes and the
//! `{"error":{"code","message"}}` body (see `api::ApiError`).

use std::fmt;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum EngineError {
    NotFound(String),
    InvalidRequest(String),
    InvalidTransition(String),
    DependencyViolation(String),
    QuotaExceeded(String),
    Conflict(String),
}

impl EngineError {
    pub fn code(&self) -> &'static str {
        match self {
            EngineError::NotFound(_) => "not_found",
            EngineError::InvalidRequest(_) => "invalid_request",
            EngineError::InvalidTransition(_) => "invalid_transition",
            EngineError::DependencyViolation(_) => "dependency_violation",
            EngineError::QuotaExceeded(_) => "quota_exceeded",
            EngineError::Conflict(_) => "conflict",
        }
    }

    pub fn message(&self) -> &str {
        match self {
            EngineError::NotFound(m)
            | EngineError::InvalidRequest(m)
            | EngineError::InvalidTransition(m)
            | EngineError::DependencyViolation(m)
            | EngineError::QuotaExceeded(m)
            | EngineError::Conflict(m) => m,
        }
    }
}

impl fmt::Display for EngineError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.code(), self.message())
    }
}

impl std::error::Error for EngineError {}

pub type EngineResult<T> = Result<T, EngineError>;
