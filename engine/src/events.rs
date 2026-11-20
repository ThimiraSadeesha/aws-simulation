//! Event ring buffer with monotonic sequence numbers and long-poll support.

use std::collections::VecDeque;
use std::time::Duration;

use parking_lot::Mutex;
use serde::Serialize;
use tokio::sync::watch;

use crate::util::{fmt_ts, now};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum EventType {
    Registered,
    StateChanged,
    Deregistered,
    FaultInjected,
    FaultTriggered,
    FaultCleared,
    QuotaRejected,
    /// Emitted by `/v1/autoscaling/evaluate` when the decision changes capacity.
    ScalingDecision,
}

#[derive(Debug, Clone, Serialize)]
pub struct Event {
    pub seq: u64,
    pub ts: String,
    pub resource_id: Option<String>,
    pub kind: Option<String>,
    #[serde(rename = "type")]
    pub event_type: EventType,
    pub from: Option<String>,
    pub to: Option<String>,
    pub reason: String,
    pub owner: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub fault_id: Option<String>,
}

/// An event before it gets a sequence number and timestamp.
#[derive(Debug, Clone)]
pub struct NewEvent {
    pub resource_id: Option<String>,
    pub kind: Option<String>,
    pub event_type: EventType,
    pub from: Option<String>,
    pub to: Option<String>,
    pub reason: String,
    pub owner: Option<String>,
    pub fault_id: Option<String>,
}

impl NewEvent {
    pub fn new(event_type: EventType, reason: impl Into<String>) -> Self {
        NewEvent {
            resource_id: None,
            kind: None,
            event_type,
            from: None,
            to: None,
            reason: reason.into(),
            owner: None,
            fault_id: None,
        }
    }

    pub fn resource(mut self, id: &str, kind: &str, owner: &str) -> Self {
        self.resource_id = Some(id.to_string());
        self.kind = Some(kind.to_string());
        self.owner = Some(owner.to_string());
        self
    }

    pub fn from_to(mut self, from: Option<&str>, to: Option<&str>) -> Self {
        self.from = from.map(str::to_string);
        self.to = to.map(str::to_string);
        self
    }

    pub fn fault(mut self, fault_id: &str) -> Self {
        self.fault_id = Some(fault_id.to_string());
        self
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct EventsPage {
    pub events: Vec<Event>,
    pub next: u64,
    pub oldest_available: u64,
}

/// Fixed-capacity ring of events. Sequence numbers start at 1 and are contiguous,
/// so lookup by `after` is O(1).
#[derive(Debug)]
pub struct EventLog {
    buf: VecDeque<Event>,
    cap: usize,
    last_seq: u64,
    emitted: u64,
}

impl EventLog {
    pub fn new(cap: usize) -> Self {
        let cap = cap.max(1);
        EventLog {
            buf: VecDeque::with_capacity(cap.min(65_536)),
            cap,
            last_seq: 0,
            emitted: 0,
        }
    }

    pub fn push(&mut self, ev: NewEvent) -> u64 {
        self.last_seq += 1;
        self.emitted += 1;
        if self.buf.len() == self.cap {
            self.buf.pop_front();
        }
        self.buf.push_back(Event {
            seq: self.last_seq,
            ts: fmt_ts(&now()),
            resource_id: ev.resource_id,
            kind: ev.kind,
            event_type: ev.event_type,
            from: ev.from,
            to: ev.to,
            reason: ev.reason,
            owner: ev.owner,
            fault_id: ev.fault_id,
        });
        self.last_seq
    }

    pub fn last_seq(&self) -> u64 {
        self.last_seq
    }

    pub fn emitted(&self) -> u64 {
        self.emitted
    }

    pub fn len(&self) -> usize {
        self.buf.len()
    }

    pub fn is_empty(&self) -> bool {
        self.buf.is_empty()
    }

    /// Seq of the oldest event still buffered (or `last_seq + 1` when empty).
    pub fn oldest_available(&self) -> u64 {
        self.buf.front().map(|e| e.seq).unwrap_or(self.last_seq + 1)
    }

    /// Events with `seq > after`, at most `limit`.
    ///
    /// `next` is the seq of the last returned event; when nothing is returned it is
    /// `min(after, last_seq)` — so a client holding a cursor from before an engine
    /// restart (after > last_seq) is handed a cursor it can continue from.
    pub fn page(&self, after: u64, limit: usize) -> EventsPage {
        let oldest = self.oldest_available();
        let start = after.saturating_add(1).max(oldest);
        let events: Vec<Event> = if start <= self.last_seq && limit > 0 {
            let idx = (start - oldest) as usize;
            self.buf.range(idx..).take(limit).cloned().collect()
        } else {
            Vec::new()
        };
        let next = events
            .last()
            .map(|e| e.seq)
            .unwrap_or_else(|| after.min(self.last_seq));
        EventsPage {
            events,
            next,
            oldest_available: oldest,
        }
    }
}

/// Thread-safe event bus: ring buffer + a watch channel carrying the latest seq
/// so long-pollers wake up when something is emitted.
pub struct EventBus {
    log: Mutex<EventLog>,
    tx: watch::Sender<u64>,
}

impl EventBus {
    pub fn new(cap: usize) -> Self {
        let (tx, _rx) = watch::channel(0);
        EventBus {
            log: Mutex::new(EventLog::new(cap)),
            tx,
        }
    }

    pub fn emit(&self, ev: NewEvent) -> u64 {
        let mut log = self.log.lock();
        let seq = log.push(ev);
        // Sent while holding the log lock so the watched value is monotonic.
        self.tx.send_replace(seq);
        seq
    }

    pub fn page(&self, after: u64, limit: usize) -> EventsPage {
        self.log.lock().page(after, limit)
    }

    pub fn last_seq(&self) -> u64 {
        self.log.lock().last_seq()
    }

    pub fn emitted(&self) -> u64 {
        self.log.lock().emitted()
    }

    /// Long-poll: return immediately if events are available (or `wait` is zero, or the
    /// cursor is ahead of the log, e.g. after an engine restart); otherwise wait until an
    /// event arrives, the timeout elapses, or `shutdown` flips to true.
    pub async fn wait_page(
        &self,
        after: u64,
        limit: usize,
        wait: Duration,
        mut shutdown: watch::Receiver<bool>,
    ) -> EventsPage {
        let mut rx = self.tx.subscribe();
        let deadline = tokio::time::Instant::now() + wait;
        loop {
            rx.borrow_and_update();
            let page = self.page(after, limit);
            if !page.events.is_empty() || wait.is_zero() || after > self.last_seq() {
                return page;
            }
            if *shutdown.borrow() {
                return page;
            }
            tokio::select! {
                changed = rx.changed() => {
                    if changed.is_err() {
                        return page;
                    }
                }
                _ = tokio::time::sleep_until(deadline) => {
                    return self.page(after, limit);
                }
                _ = shutdown.changed() => {
                    return self.page(after, limit);
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ev(i: usize) -> NewEvent {
        NewEvent::new(EventType::Registered, format!("e{i}")).resource(&format!("r{i}"), "vpc", "o")
    }

    #[test]
    fn seq_starts_at_one_and_pages() {
        let mut log = EventLog::new(10);
        assert_eq!(log.oldest_available(), 1);
        let p = log.page(0, 100);
        assert!(p.events.is_empty());
        assert_eq!(p.next, 0);
        for i in 0..5 {
            log.push(ev(i));
        }
        let p = log.page(0, 100);
        assert_eq!(p.events.len(), 5);
        assert_eq!(p.events[0].seq, 1);
        assert_eq!(p.next, 5);
        let p = log.page(2, 2);
        assert_eq!(
            p.events.iter().map(|e| e.seq).collect::<Vec<_>>(),
            vec![3, 4]
        );
        assert_eq!(p.next, 4);
        let p = log.page(5, 10);
        assert!(p.events.is_empty());
        assert_eq!(p.next, 5);
    }

    #[test]
    fn ring_evicts_oldest() {
        let mut log = EventLog::new(3);
        for i in 0..10 {
            log.push(ev(i));
        }
        assert_eq!(log.len(), 3);
        assert_eq!(log.oldest_available(), 8);
        let p = log.page(0, 100);
        assert_eq!(
            p.events.iter().map(|e| e.seq).collect::<Vec<_>>(),
            vec![8, 9, 10]
        );
        assert_eq!(p.oldest_available, 8);
        assert_eq!(log.emitted(), 10);
    }

    #[test]
    fn cursor_ahead_of_log_is_reset() {
        let mut log = EventLog::new(3);
        log.push(ev(0));
        let p = log.page(500, 10);
        assert!(p.events.is_empty());
        assert_eq!(p.next, 1);
    }

    #[tokio::test]
    async fn long_poll_wakes_on_emit() {
        let bus = std::sync::Arc::new(EventBus::new(16));
        let (_stx, srx) = watch::channel(false);
        let b2 = bus.clone();
        let h = tokio::spawn(async move { b2.wait_page(0, 10, Duration::from_secs(5), srx).await });
        tokio::time::sleep(Duration::from_millis(20)).await;
        bus.emit(ev(1));
        let page = h.await.unwrap();
        assert_eq!(page.events.len(), 1);
        assert_eq!(page.events[0].seq, 1);
    }

    #[tokio::test]
    async fn long_poll_times_out() {
        let bus = EventBus::new(16);
        let (_stx, srx) = watch::channel(false);
        let start = std::time::Instant::now();
        let page = bus.wait_page(0, 10, Duration::from_millis(50), srx).await;
        assert!(page.events.is_empty());
        assert!(start.elapsed() >= Duration::from_millis(45));
    }
}
