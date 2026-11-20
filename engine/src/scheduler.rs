//! Delayed transition completion on tokio timers.
//!
//! Each scheduled completion carries a unique token stored in the resource's `pending`
//! slot. A new action cancels the slot (aborting the timer task); a timer that fires
//! anyway is ignored because its token no longer matches.

use std::sync::Arc;
use std::time::Duration;

use chrono::Utc;

use crate::engine::Engine;
use crate::lifecycle::Completion;
use crate::registry::Pending;

/// Spawn a timer that completes `id`'s transition after `delay`. Returns `None` when no
/// tokio runtime is available (the caller then completes synchronously).
pub(crate) fn spawn_completion(
    engine: &Engine,
    id: &str,
    token: u64,
    c: Completion,
    delay: Duration,
) -> Option<Pending> {
    let handle = tokio::runtime::Handle::try_current().ok()?;
    let weak = Arc::downgrade(&engine.inner);
    let id_owned = id.to_string();
    let to = c.to;
    let task = handle.spawn(async move {
        tokio::time::sleep(delay).await;
        if let Some(inner) = weak.upgrade() {
            Engine::from_inner(inner).complete(&id_owned, token, &c);
        }
    });
    let due_at =
        Utc::now() + chrono::Duration::from_std(delay).unwrap_or_else(|_| chrono::Duration::zero());
    Some(Pending {
        token,
        to,
        due_at,
        abort: Some(task.abort_handle()),
    })
}
