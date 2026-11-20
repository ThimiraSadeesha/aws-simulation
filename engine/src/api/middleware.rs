use std::time::Instant;

use axum::extract::{Request, State};
use axum::http::{HeaderValue, StatusCode};
use axum::middleware::Next;
use axum::response::{IntoResponse, Response};

use super::error::ApiError;
use crate::engine::Engine;

pub const TOKEN_HEADER: &str = "x-engine-token";
pub const PROTOCOL_HEADER: &str = "x-engine-protocol";

fn constant_time_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    a.iter().zip(b).fold(0u8, |acc, (x, y)| acc | (x ^ y)) == 0
}

/// Reject `/v1/*` requests without the correct `X-Engine-Token`.
pub async fn require_token(State(engine): State<Engine>, req: Request, next: Next) -> Response {
    let ok = req
        .headers()
        .get(TOKEN_HEADER)
        .is_some_and(|v| constant_time_eq(v.as_bytes(), engine.config().token.as_bytes()));
    if !ok {
        return ApiError::unauthorized().into_response();
    }
    next.run(req).await
}

/// Add `X-Engine-Protocol: 1` to every response.
pub async fn protocol_header(mut res: Response) -> Response {
    res.headers_mut()
        .insert(PROTOCOL_HEADER, HeaderValue::from_static("1"));
    res
}

/// One structured log line per request (path only; no bodies or query strings).
pub async fn access_log(req: Request, next: Next) -> Response {
    let method = req.method().clone();
    let path = req.uri().path().to_string();
    let start = Instant::now();
    let res = next.run(req).await;
    let status = res.status();
    let latency_ms = start.elapsed().as_secs_f64() * 1000.0;
    if path == "/healthz" {
        tracing::debug!(%method, %path, status = status.as_u16(), latency_ms, "request");
    } else if status.is_server_error() {
        tracing::error!(%method, %path, status = status.as_u16(), latency_ms, "request");
    } else if status == StatusCode::UNAUTHORIZED {
        tracing::warn!(%method, %path, status = status.as_u16(), latency_ms, "unauthorized request");
    } else {
        tracing::info!(%method, %path, status = status.as_u16(), latency_ms, "request");
    }
    res
}
