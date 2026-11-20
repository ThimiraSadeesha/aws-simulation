use std::collections::{BTreeMap, HashMap};
use std::time::Duration;

use axum::extract::rejection::QueryRejection;
use axum::extract::{Path, Query, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde::Deserialize;
use serde_json::json;

use super::error::ApiError;
use super::extract::ApiJson;
use super::PROTOCOL_VERSION;
use crate::autoscaling::EvaluateRequest;
use crate::engine::{ActionRequest, Engine, HealthRequest, RegisterRequest, WorkloadRequest};
use crate::faults::FaultRequest;
use crate::lb::LbSimRequest;
use crate::network::{self, CidrRequest, ReachabilityRequest};
use crate::storage;

type ApiResult = Result<Response, ApiError>;
type QueryMap = Result<Query<HashMap<String, String>>, QueryRejection>;

pub const DEFAULT_EVENT_LIMIT: usize = 100;
pub const MAX_EVENT_LIMIT: usize = 1000;
pub const MAX_WAIT_MS: u64 = 30_000;

fn query(q: QueryMap) -> Result<HashMap<String, String>, ApiError> {
    q.map(|Query(m)| m)
        .map_err(|e| ApiError::invalid(format!("invalid query string: {e}")))
}

fn num_param(q: &HashMap<String, String>, name: &str) -> Result<Option<u64>, ApiError> {
    match q.get(name).map(|s| s.trim()).filter(|s| !s.is_empty()) {
        None => Ok(None),
        Some(v) => v
            .parse::<u64>()
            .map(Some)
            .map_err(|_| ApiError::invalid(format!("{name} must be a non-negative integer"))),
    }
}

pub async fn healthz() -> Response {
    Json(json!({
        "status": "ok",
        "protocol": PROTOCOL_VERSION,
        "version": env!("CARGO_PKG_VERSION"),
    }))
    .into_response()
}

pub async fn not_found() -> ApiError {
    ApiError::not_found("no such endpoint")
}

pub async fn method_not_allowed() -> ApiError {
    ApiError::new(
        StatusCode::METHOD_NOT_ALLOWED,
        "invalid_request",
        "method not allowed for this endpoint",
    )
}

pub async fn stats(State(engine): State<Engine>) -> Response {
    Json(engine.stats()).into_response()
}

// ---- resources --------------------------------------------------------------------

pub async fn register(
    State(engine): State<Engine>,
    Path(id): Path<String>,
    ApiJson(req): ApiJson<RegisterRequest>,
) -> ApiResult {
    let (view, created) = engine.register(&id, req)?;
    let status = if created {
        StatusCode::CREATED
    } else {
        StatusCode::OK
    };
    Ok((status, Json(view)).into_response())
}

pub async fn get_resource(State(engine): State<Engine>, Path(id): Path<String>) -> ApiResult {
    engine
        .get(&id)
        .map(|v| Json(v).into_response())
        .ok_or_else(|| ApiError::not_found(format!("resource {id} not found")))
}

pub async fn list_resources(State(engine): State<Engine>, q: QueryMap) -> ApiResult {
    let q = query(q)?;
    let items = engine.list(
        q.get("kind").map(String::as_str).filter(|s| !s.is_empty()),
        q.get("owner").map(String::as_str).filter(|s| !s.is_empty()),
    );
    Ok(Json(json!({ "items": items })).into_response())
}

pub async fn action(
    State(engine): State<Engine>,
    Path(id): Path<String>,
    ApiJson(req): ApiJson<ActionRequest>,
) -> ApiResult {
    let view = engine.action(&id, &req.action, req.reason.as_deref())?;
    Ok((StatusCode::ACCEPTED, Json(view)).into_response())
}

pub async fn deregister(State(engine): State<Engine>, Path(id): Path<String>) -> ApiResult {
    engine.deregister(&id)?;
    Ok(StatusCode::NO_CONTENT.into_response())
}

pub async fn set_workload(
    State(engine): State<Engine>,
    Path(id): Path<String>,
    ApiJson(req): ApiJson<WorkloadRequest>,
) -> ApiResult {
    Ok(Json(engine.set_workload(&id, req)?).into_response())
}

// ---- events & metrics ---------------------------------------------------------------

pub async fn events(State(engine): State<Engine>, q: QueryMap) -> ApiResult {
    let q = query(q)?;
    let after = num_param(&q, "after")?.unwrap_or(0);
    let limit = match num_param(&q, "limit")? {
        None => DEFAULT_EVENT_LIMIT,
        Some(0) => return Err(ApiError::invalid("limit must be between 1 and 1000")),
        Some(n) => (n as usize).min(MAX_EVENT_LIMIT),
    };
    let wait_ms = num_param(&q, "wait_ms")?.unwrap_or(0).min(MAX_WAIT_MS);
    let page = engine
        .wait_events(after, limit, Duration::from_millis(wait_ms))
        .await;
    Ok(Json(page).into_response())
}

pub async fn metrics_snapshot(State(engine): State<Engine>) -> Response {
    Json(engine.metrics_snapshot()).into_response()
}

// ---- network ---------------------------------------------------------------------------

pub async fn reachability(
    State(engine): State<Engine>,
    ApiJson(req): ApiJson<ReachabilityRequest>,
) -> ApiResult {
    let r =
        network::evaluate_reachability(&req, engine.config().seed).map_err(ApiError::invalid)?;
    Ok(Json(r).into_response())
}

pub async fn cidr(ApiJson(req): ApiJson<CidrRequest>) -> ApiResult {
    Ok(Json(network::cidr_op(&req).map_err(ApiError::invalid)?).into_response())
}

// ---- quotas ------------------------------------------------------------------------------

pub async fn get_quotas(State(engine): State<Engine>, q: QueryMap) -> ApiResult {
    let q = query(q)?;
    let owner = q.get("owner").map(String::as_str).filter(|s| !s.is_empty());
    Ok(Json(engine.quotas(owner)).into_response())
}

#[derive(Deserialize)]
pub struct QuotaUpdate {
    limits: BTreeMap<String, u32>,
}

pub async fn put_quotas(
    State(engine): State<Engine>,
    ApiJson(req): ApiJson<QuotaUpdate>,
) -> ApiResult {
    let limits = engine.set_quotas(&req.limits)?;
    Ok(Json(json!({ "limits": limits })).into_response())
}

// ---- load balancing / autoscaling / storage ------------------------------------------------

pub async fn lb_simulate(
    State(engine): State<Engine>,
    ApiJson(req): ApiJson<LbSimRequest>,
) -> ApiResult {
    Ok(Json(engine.lb_simulate(&req)?).into_response())
}

pub async fn lb_health(
    State(engine): State<Engine>,
    ApiJson(req): ApiJson<HealthRequest>,
) -> ApiResult {
    Ok(Json(engine.lb_health(&req)).into_response())
}

pub async fn autoscaling_evaluate(
    State(engine): State<Engine>,
    ApiJson(req): ApiJson<EvaluateRequest>,
) -> ApiResult {
    Ok(Json(engine.autoscaling_evaluate(&req)?).into_response())
}

pub async fn storage_lifecycle(ApiJson(req): ApiJson<storage::LifecycleRequest>) -> ApiResult {
    Ok(Json(storage::evaluate_lifecycle(&req).map_err(ApiError::invalid)?).into_response())
}

pub async fn storage_simulate(ApiJson(req): ApiJson<storage::SimRequest>) -> ApiResult {
    Ok(Json(storage::simulate(&req).map_err(ApiError::invalid)?).into_response())
}

// ---- faults --------------------------------------------------------------------------------

pub async fn inject_fault(
    State(engine): State<Engine>,
    ApiJson(req): ApiJson<FaultRequest>,
) -> ApiResult {
    let f = engine.inject_fault(req)?;
    Ok((StatusCode::CREATED, Json(f)).into_response())
}

pub async fn list_faults(State(engine): State<Engine>) -> Response {
    Json(json!({ "items": engine.list_faults() })).into_response()
}

pub async fn clear_fault(State(engine): State<Engine>, Path(id): Path<String>) -> ApiResult {
    engine.clear_fault(&id)?;
    Ok(StatusCode::NO_CONTENT.into_response())
}
