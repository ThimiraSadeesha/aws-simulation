use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde_json::json;

use crate::error::EngineError;

/// Error response: non-2xx with `{"error":{"code","message"}}`.
#[derive(Debug, Clone)]
pub struct ApiError {
    pub status: StatusCode,
    pub code: &'static str,
    pub message: String,
}

impl ApiError {
    pub fn new(status: StatusCode, code: &'static str, message: impl Into<String>) -> Self {
        ApiError {
            status,
            code,
            message: message.into(),
        }
    }

    pub fn invalid(message: impl Into<String>) -> Self {
        Self::new(StatusCode::UNPROCESSABLE_ENTITY, "invalid_request", message)
    }

    pub fn unauthorized() -> Self {
        Self::new(
            StatusCode::UNAUTHORIZED,
            "unauthorized",
            "missing or invalid X-Engine-Token header",
        )
    }

    pub fn not_found(message: impl Into<String>) -> Self {
        Self::new(StatusCode::NOT_FOUND, "not_found", message)
    }
}

impl From<EngineError> for ApiError {
    fn from(e: EngineError) -> Self {
        let status = match e {
            EngineError::NotFound(_) => StatusCode::NOT_FOUND,
            EngineError::InvalidRequest(_) => StatusCode::UNPROCESSABLE_ENTITY,
            EngineError::InvalidTransition(_)
            | EngineError::DependencyViolation(_)
            | EngineError::Conflict(_) => StatusCode::CONFLICT,
            EngineError::QuotaExceeded(_) => StatusCode::TOO_MANY_REQUESTS,
        };
        ApiError::new(status, e.code(), e.message().to_string())
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (
            self.status,
            Json(json!({"error": {"code": self.code, "message": self.message}})),
        )
            .into_response()
    }
}
