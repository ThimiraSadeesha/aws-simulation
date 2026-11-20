//! Extractors that report failures in the protocol's error format.

use axum::body::Bytes;
use axum::extract::{FromRequest, Request};
use serde::de::DeserializeOwned;

use super::error::ApiError;

/// JSON body extractor. Parses the body regardless of `Content-Type` and maps any
/// failure to `422 invalid_request`.
pub struct ApiJson<T>(pub T);

impl<S, T> FromRequest<S> for ApiJson<T>
where
    S: Send + Sync,
    T: DeserializeOwned,
{
    type Rejection = ApiError;

    async fn from_request(req: Request, state: &S) -> Result<Self, Self::Rejection> {
        let bytes = Bytes::from_request(req, state)
            .await
            .map_err(|e| ApiError::invalid(format!("could not read request body: {e}")))?;
        if bytes.is_empty() {
            return Err(ApiError::invalid("request body must be a JSON document"));
        }
        serde_json::from_slice(&bytes)
            .map(ApiJson)
            .map_err(|e| ApiError::invalid(format!("invalid JSON body: {e}")))
    }
}
