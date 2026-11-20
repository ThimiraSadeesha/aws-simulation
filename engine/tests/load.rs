//! Concurrency and stress tests.

mod common;

use std::collections::HashSet;
use std::time::{Duration, Instant};

use common::{app, app_with};
use serde_json::json;

use cloudsim_engine::engine::RegisterRequest;
use cloudsim_engine::{Config, Engine};

#[tokio::test(flavor = "multi_thread", worker_threads = 8)]
async fn concurrent_registration_and_actions_are_consistent() {
    let a = app();
    a.put("/v1/quotas", json!({"limits":{"instance":10000}}))
        .await;
    a.register("vpc-1", "vpc", &[]).await;

    let tasks = 32;
    let per_task = 25; // 800 instances
    let mut handles = Vec::new();
    for t in 0..tasks {
        let router = a.router.clone();
        let engine = a.engine.clone();
        handles.push(tokio::spawn(async move {
            let a = common::App { engine, router };
            for i in 0..per_task {
                let id = format!("i-{t}-{i}");
                let r = a
                    .put(
                        &format!("/v1/resources/{id}"),
                        json!({"kind":"instance","owner":format!("owner-{}", t % 4),"depends_on":["vpc-1"]}),
                    )
                    .await;
                assert_eq!(r.status, 201);
                assert_eq!(a.act(&id, "stop").await.status, 202);
                if i % 2 == 0 {
                    assert_eq!(a.act(&id, "start").await.status, 202);
                }
                if i % 5 == 0 {
                    assert_eq!(a.act(&id, "terminate").await.status, 202);
                }
                // Re-sync of the same id is idempotent.
                let r = a
                    .put(
                        &format!("/v1/resources/{id}"),
                        json!({"kind":"instance","owner":format!("owner-{}", t % 4),"depends_on":["vpc-1"]}),
                    )
                    .await;
                assert_eq!(r.status, 200);
            }
        }));
    }
    // Readers hammering the API concurrently.
    let mut readers = Vec::new();
    for _ in 0..4 {
        let router = a.router.clone();
        let engine = a.engine.clone();
        readers.push(tokio::spawn(async move {
            let a = common::App { engine, router };
            for _ in 0..50 {
                assert_eq!(a.get("/v1/resources?kind=instance").await.status, 200);
                assert_eq!(a.get("/v1/events?after=0&limit=1000").await.status, 200);
                a.engine.tick();
            }
        }));
    }
    for h in handles.into_iter().chain(readers) {
        h.await.unwrap();
    }

    let total = tasks * per_task;
    let items = a.get("/v1/resources?kind=instance").await.body["items"]
        .as_array()
        .unwrap()
        .clone();
    assert_eq!(items.len(), total);
    let mut running = 0;
    let mut stopped = 0;
    let mut terminated = 0;
    for it in &items {
        match it["state"].as_str().unwrap() {
            "running" => running += 1,
            "stopped" => stopped += 1,
            "terminated" => terminated += 1,
            s => panic!("unexpected state {s}"),
        }
    }
    // i%5==0 -> terminated (5 per task); else even -> running (10); else stopped (10)
    assert_eq!(terminated, tasks * 5);
    assert_eq!(running, tasks * 10);
    assert_eq!(stopped, tasks * 10);

    let stats = a.engine.stats();
    assert_eq!(stats.resources_total as usize, total + 1);
    // Every event has a unique, contiguous seq.
    let mut after = 0;
    let mut seen = HashSet::new();
    loop {
        let page = a.engine.events_page(after, 1000);
        if page.events.is_empty() {
            break;
        }
        for e in &page.events {
            assert!(seen.insert(e.seq));
        }
        after = page.next;
    }
    assert_eq!(seen.len() as u64, stats.event_seq);
    assert_eq!(*seen.iter().max().unwrap(), stats.event_seq);

    // Quota usage per owner equals the non-terminated count.
    let mut usage_sum = 0;
    for o in 0..4 {
        let q = a.get(&format!("/v1/quotas?owner=owner-{o}")).await;
        usage_sum += q.body["usage"]["instance"].as_u64().unwrap_or(0);
    }
    assert_eq!(usage_sum as usize, running + stopped);

    // The VPC cannot be deleted while active instances depend on it.
    assert_eq!(a.delete("/v1/resources/vpc-1").await.status, 409);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn concurrent_timed_transitions_settle() {
    // Real timers: 300 instances booting with time_scale 0.01 (30 ms), half terminated
    // mid-boot to exercise supersede/cancel races.
    let a = app_with(|c| c.time_scale = 0.01);
    a.put("/v1/quotas", json!({"limits":{"instance":1000}}))
        .await;
    let mut hs = Vec::new();
    for t in 0..10 {
        let engine = a.engine.clone();
        hs.push(tokio::spawn(async move {
            for i in 0..30 {
                let id = format!("i-{t}-{i}");
                engine
                    .register(
                        &id,
                        RegisterRequest {
                            kind: "instance".into(),
                            owner: "o".into(),
                            state: None,
                            depends_on: None,
                            attributes: None,
                        },
                    )
                    .unwrap();
                if i % 2 == 1 {
                    engine.action(&id, "terminate", None).unwrap();
                }
            }
        }));
    }
    for h in hs {
        h.await.unwrap();
    }
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        if a.engine.stats().pending_transitions == 0 {
            break;
        }
        assert!(Instant::now() < deadline, "transitions did not settle");
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    let items = a.engine.list(Some("instance"), None);
    assert_eq!(items.len(), 300);
    assert_eq!(items.iter().filter(|v| v.state == "running").count(), 150);
    assert_eq!(
        items.iter().filter(|v| v.state == "terminated").count(),
        150
    );
}

#[test]
fn stress_10k_resources_50k_events() {
    let mut cfg = Config::for_tests("t");
    cfg.event_buffer = 100_000;
    let engine = Engine::new(cfg);
    engine
        .set_quotas(
            &[
                ("instance".to_string(), 100_000),
                ("bucket".to_string(), 100_000),
            ]
            .into_iter()
            .collect(),
        )
        .unwrap();
    let start = Instant::now();
    let n = 10_000;
    for i in 0..n {
        let kind = if i % 2 == 0 { "instance" } else { "bucket" };
        engine
            .register(
                &format!("r-{i}"),
                RegisterRequest {
                    kind: kind.into(),
                    owner: format!("o{}", i % 50),
                    state: None,
                    depends_on: None,
                    attributes: None,
                },
            )
            .unwrap();
    }
    // 5k instances × (registered + boot) + 5k buckets × registered = 15k events so far.
    // Each stop/start cycle emits 4 events (2 transitions + 2 completions).
    for i in 0..(50_000 - 15_000) / 4 {
        let id = format!("r-{}", (i * 2) % n);
        engine.action(&id, "stop", None).unwrap();
        engine.action(&id, "start", None).unwrap();
    }
    for _ in 0..5 {
        engine.tick();
    }
    let stats = engine.stats();
    assert_eq!(stats.resources_total, n as u64);
    assert_eq!(stats.event_seq, 50_000);
    assert_eq!(engine.metrics_snapshot().samples.len(), n / 2);
    let mut read = 0;
    let mut after = 0;
    loop {
        let p = engine.events_page(after, 1000);
        if p.events.is_empty() {
            break;
        }
        read += p.events.len();
        after = p.next;
    }
    assert_eq!(read as u64, stats.event_seq);
    let elapsed = start.elapsed();
    // Generous bound for unoptimised debug builds on CI.
    assert!(elapsed < Duration::from_secs(30), "stress took {elapsed:?}");
    eprintln!(
        "stress: {n} resources, {} events in {elapsed:?}",
        stats.event_seq
    );
}
