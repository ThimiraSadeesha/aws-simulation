//! HTTP layer: axum router, auth middleware, error mapping and handlers.

mod error;
mod extract;
mod handlers;
mod middleware;

pub use error::ApiError;

use axum::extract::DefaultBodyLimit;
use axum::routing::{delete, get, post, put};
use axum::Router;

use crate::engine::Engine;

pub const PROTOCOL_VERSION: u32 = 1;
pub const MAX_BODY_BYTES: usize = 16 * 1024 * 1024;

/// Build the complete router (healthz + authenticated `/v1`).
pub fn router(engine: Engine) -> Router {
    let v1 = Router::new()
        .route("/stats", get(handlers::stats))
        .route("/resources", get(handlers::list_resources))
        .route(
            "/resources/{id}",
            put(handlers::register)
                .get(handlers::get_resource)
                .delete(handlers::deregister),
        )
        .route("/resources/{id}/actions", post(handlers::action))
        .route("/resources/{id}/workload", put(handlers::set_workload))
        .route("/events", get(handlers::events))
        .route("/metrics/snapshot", get(handlers::metrics_snapshot))
        .route("/network/reachability", post(handlers::reachability))
        .route("/network/cidr", post(handlers::cidr))
        .route(
            "/quotas",
            get(handlers::get_quotas).put(handlers::put_quotas),
        )
        .route("/lb/simulate", post(handlers::lb_simulate))
        .route("/lb/health", post(handlers::lb_health))
        .route(
            "/autoscaling/evaluate",
            post(handlers::autoscaling_evaluate),
        )
        .route(
            "/storage/lifecycle/evaluate",
            post(handlers::storage_lifecycle),
        )
        .route("/storage/simulate", post(handlers::storage_simulate))
        .route(
            "/faults",
            get(handlers::list_faults).post(handlers::inject_fault),
        )
        .route("/faults/{id}", delete(handlers::clear_fault))
        .fallback(handlers::not_found)
        .layer(axum::middleware::from_fn_with_state(
            engine.clone(),
            middleware::require_token,
        ));

    Router::new()
        .route("/healthz", get(handlers::healthz))
        .nest("/v1", v1)
        .fallback(handlers::not_found)
        .method_not_allowed_fallback(handlers::method_not_allowed)
        .layer(DefaultBodyLimit::max(MAX_BODY_BYTES))
        .layer(axum::middleware::from_fn(middleware::access_log))
        .layer(axum::middleware::map_response(middleware::protocol_header))
        .with_state(engine)
}
