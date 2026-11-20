mod common;

use std::time::Duration;

use axum::http::Method;
use common::{app, app_with, assert_error, TOKEN};
use serde_json::json;

#[tokio::test]
async fn healthz_is_public_and_versioned() {
    let a = app();
    let r = a.raw(Method::GET, "/healthz", None, None).await;
    assert_eq!(r.status, 200);
    assert_eq!(r.body["status"], "ok");
    assert_eq!(r.body["protocol"], 1);
    assert_eq!(r.body["version"], env!("CARGO_PKG_VERSION"));
    assert_eq!(r.headers["x-engine-protocol"], "1");
}

#[tokio::test]
async fn auth_rejection() {
    let a = app();
    let r = a.raw(Method::GET, "/v1/stats", None, None).await;
    assert_error(&r, 401, "unauthorized");
    assert_eq!(r.headers["x-engine-protocol"], "1");
    let r = a.raw(Method::GET, "/v1/stats", None, Some("wrong")).await;
    assert_error(&r, 401, "unauthorized");
    let r = a.raw(Method::GET, "/v1/does-not-exist", None, None).await;
    assert_error(&r, 401, "unauthorized");
    let r = a
        .raw(
            Method::PUT,
            "/v1/resources/vpc-1",
            Some(json!({"kind":"vpc"})),
            Some("nope"),
        )
        .await;
    assert_error(&r, 401, "unauthorized");
    assert!(a.engine.get("vpc-1").is_none());
    let r = a.raw(Method::GET, "/v1/stats", None, Some(TOKEN)).await;
    assert_eq!(r.status, 200);
    assert_eq!(r.headers["x-engine-protocol"], "1");
}

#[tokio::test]
async fn unknown_routes_and_bad_bodies() {
    let a = app();
    assert_error(&a.get("/v1/nope").await, 404, "not_found");
    assert_error(&a.get("/v1/resources/missing").await, 404, "not_found");
    let r = a
        .call(Method::PUT, "/v1/resources/x", Some(json!({"owner":"o"})))
        .await;
    assert_error(&r, 422, "invalid_request");
    let r = a.call(Method::PUT, "/v1/resources/x", None).await;
    assert_error(&r, 422, "invalid_request");
    assert_error(&a.get("/v1/events?after=abc").await, 422, "invalid_request");
}

#[tokio::test]
async fn full_instance_lifecycle() {
    let a = app();
    a.register("vpc-1", "vpc", &[]).await;
    a.register("subnet-1", "subnet", &["vpc-1"]).await;
    let r = a
        .put(
            "/v1/resources/i-0abc",
            json!({"kind":"instance","owner":"123456789012","state":null,"depends_on":["subnet-1"],
                   "attributes":{"instance_type":"t3.micro","vcpu":2,"memory_mib":1024,"workload":"steady"}}),
        )
        .await;
    assert_eq!(r.status, 201);
    // time_scale 0: the boot already completed synchronously.
    assert_eq!(r.body["state"], "running");
    assert_eq!(r.body["depends_on"], json!(["subnet-1"]));
    assert_eq!(r.body["workload"]["profile"], "steady");
    assert_eq!(r.body["attributes"]["instance_type"], "t3.micro");
    assert!(r.body["updated_at"].as_str().unwrap().ends_with('Z'));
    assert_eq!(a.state("i-0abc").await, "running");
    let sub = a.get("/v1/resources/subnet-1").await;
    assert_eq!(sub.body["dependents"], json!(["i-0abc"]));

    let r = a.act("i-0abc", "stop").await;
    assert_eq!(r.status, 202);
    assert_eq!(a.state("i-0abc").await, "stopped");
    assert_error(&a.act("i-0abc", "stop").await, 409, "invalid_transition");
    assert_error(&a.act("i-0abc", "reboot").await, 409, "invalid_transition");
    assert_error(&a.act("i-0abc", "fly").await, 409, "invalid_transition");
    assert_eq!(a.act("i-0abc", "start").await.status, 202);
    assert_eq!(a.state("i-0abc").await, "running");
    assert_error(&a.act("i-0abc", "start").await, 409, "invalid_transition");
    assert_eq!(a.act("i-0abc", "reboot").await.status, 202);
    assert_eq!(a.state("i-0abc").await, "running");
    assert_eq!(a.act("i-0abc", "terminate").await.status, 202);
    assert_eq!(a.state("i-0abc").await, "terminated");
    assert_error(
        &a.act("i-0abc", "terminate").await,
        409,
        "invalid_transition",
    );
    assert_error(&a.act("missing", "stop").await, 404, "not_found");

    // Event trail for the instance, in order.
    let trail: Vec<(String, String, String)> = a
        .events(0)
        .await
        .into_iter()
        .filter(|e| e["resource_id"] == "i-0abc")
        .map(|e| {
            (
                e["type"].as_str().unwrap().to_string(),
                e["from"].as_str().unwrap_or("-").to_string(),
                e["to"].as_str().unwrap_or("-").to_string(),
            )
        })
        .collect();
    let expect = [
        ("registered", "-", "pending"),
        ("state_changed", "pending", "running"),
        ("state_changed", "running", "stopping"),
        ("state_changed", "stopping", "stopped"),
        ("state_changed", "stopped", "pending"),
        ("state_changed", "pending", "running"),
        ("state_changed", "running", "rebooting"),
        ("state_changed", "rebooting", "running"),
        ("state_changed", "running", "shutting-down"),
        ("state_changed", "shutting-down", "terminated"),
    ];
    assert_eq!(trail.len(), expect.len(), "{trail:?}");
    for (got, want) in trail.iter().zip(expect) {
        assert_eq!((got.0.as_str(), got.1.as_str(), got.2.as_str()), want);
    }

    // Terminated instances no longer block deletion of the subnet.
    assert_eq!(a.delete("/v1/resources/i-0abc").await.status, 204);
    assert_error(&a.get("/v1/resources/i-0abc").await, 404, "not_found");
    assert_error(&a.delete("/v1/resources/i-0abc").await, 404, "not_found");
}

#[tokio::test]
async fn other_kinds_lifecycles() {
    let a = app();
    assert_eq!(
        a.register("nat-1", "nat_gateway", &[]).await.body["state"],
        "available"
    );
    assert_eq!(a.act("nat-1", "delete").await.body["state"], "deleted");
    assert_error(&a.act("nat-1", "delete").await, 409, "invalid_transition");
    assert_eq!(
        a.register("lb-1", "load_balancer", &[]).await.body["state"],
        "active"
    );
    assert_eq!(
        a.register("db-1", "db_instance", &[]).await.body["state"],
        "available"
    );
    assert_eq!(a.act("db-1", "stop").await.body["state"], "stopped");
    assert_eq!(a.act("db-1", "start").await.body["state"], "available");
    assert_eq!(
        a.register("b-1", "bucket", &[]).await.body["state"],
        "available"
    );
    assert_eq!(a.act("b-1", "delete").await.body["state"], "deleted");
    assert_error(&a.act("b-1", "stop").await, 409, "invalid_transition");
}

#[tokio::test]
async fn timed_transitions_and_supersede() {
    // time_scale 0.01: boot 3000ms -> 30ms.
    let a = app_with(|c| c.time_scale = 0.01);
    let r = a.register("i-1", "instance", &[]).await;
    assert_eq!(r.body["state"], "pending");
    assert_eq!(r.body["pending_transition"]["to"], "running");
    assert_eq!(a.engine.stats().pending_transitions, 1);
    tokio::time::sleep(Duration::from_millis(150)).await;
    assert_eq!(a.state("i-1").await, "running");
    assert_eq!(a.engine.stats().pending_transitions, 0);

    // terminate while pending supersedes the boot.
    a.register("i-2", "instance", &[]).await;
    assert_eq!(
        a.act("i-2", "terminate").await.body["state"],
        "shutting-down"
    );
    tokio::time::sleep(Duration::from_millis(150)).await;
    assert_eq!(a.state("i-2").await, "terminated");
    let transitions: Vec<String> = a
        .events(0)
        .await
        .into_iter()
        .filter(|e| e["resource_id"] == "i-2" && e["type"] == "state_changed")
        .map(|e| {
            format!(
                "{}->{}",
                e["from"].as_str().unwrap(),
                e["to"].as_str().unwrap()
            )
        })
        .collect();
    assert_eq!(
        transitions,
        vec!["pending->shutting-down", "shutting-down->terminated"]
    );
}

#[tokio::test]
async fn restart_recovery_reregistration() {
    let a = app_with(|c| c.time_scale = 0.01);
    a.register("vpc-1", "vpc", &[]).await;
    // Explicit stable state is restored exactly, no transition scheduled.
    let r = a
        .put(
            "/v1/resources/i-1",
            json!({"kind":"instance","owner":"123456789012","state":"stopped","depends_on":["vpc-1"],"attributes":{}}),
        )
        .await;
    assert_eq!(r.status, 201);
    assert_eq!(r.body["state"], "stopped");
    assert!(r.body["pending_transition"].is_null());
    let ev = a.events(0).await;
    let last = ev.last().unwrap();
    assert_eq!(last["type"], "registered");
    assert_eq!(last["to"], "stopped");

    // Explicit transitional state re-schedules its completion.
    let r = a
        .put(
            "/v1/resources/i-2",
            json!({"kind":"instance","owner":"123456789012","state":"stopping"}),
        )
        .await;
    assert_eq!(r.body["state"], "stopping");
    tokio::time::sleep(Duration::from_millis(120)).await;
    assert_eq!(a.state("i-2").await, "stopped");

    // Idempotent re-PUT: 200, attributes/depends_on updated, version bumped.
    let r = a
        .put(
            "/v1/resources/i-1",
            json!({"kind":"instance","owner":"123456789012","state":"stopped","depends_on":[],"attributes":{"tag":"x"}}),
        )
        .await;
    assert_eq!(r.status, 200);
    assert_eq!(r.body["attributes"]["tag"], "x");
    assert_eq!(r.body["depends_on"], json!([]));
    assert!(r.body["version"].as_u64().unwrap() >= 2);
    let r = a
        .put(
            "/v1/resources/i-1",
            json!({"kind":"instance","owner":"123456789012","state":"running"}),
        )
        .await;
    assert_eq!(r.status, 200);
    assert_eq!(r.body["state"], "running");
    assert_eq!(r.body["attributes"]["tag"], "x");

    // Kind mismatch is a conflict; unknown dependency is invalid.
    let r = a
        .put(
            "/v1/resources/i-1",
            json!({"kind":"vpc","owner":"123456789012"}),
        )
        .await;
    assert_error(&r, 409, "conflict");
    let r = a
        .put(
            "/v1/resources/i-3",
            json!({"kind":"instance","owner":"123456789012","depends_on":["subnet-404"]}),
        )
        .await;
    assert_error(&r, 422, "invalid_request");
}

#[tokio::test]
async fn dependency_violation_on_delete() {
    let a = app();
    a.register("vpc-1", "vpc", &[]).await;
    a.register("subnet-1", "subnet", &["vpc-1"]).await;
    a.register("subnet-2", "subnet", &["vpc-1"]).await;
    let r = a.delete("/v1/resources/vpc-1").await;
    assert_error(&r, 409, "dependency_violation");
    let msg = r.body["error"]["message"].as_str().unwrap();
    assert!(
        msg.contains("subnet-1") && msg.contains("subnet-2"),
        "{msg}"
    );
    assert_eq!(a.delete("/v1/resources/subnet-1").await.status, 204);
    // A deleted (terminal) dependent no longer blocks.
    a.act("subnet-2", "delete").await;
    assert_eq!(a.delete("/v1/resources/vpc-1").await.status, 204);
    let last = a.events(0).await.last().cloned().unwrap();
    assert_eq!(last["type"], "deregistered");
    assert_eq!(last["resource_id"], "vpc-1");
}

#[tokio::test]
async fn quota_exceeded() {
    let a = app();
    let r = a.put("/v1/quotas", json!({"limits":{"instance":2}})).await;
    assert_eq!(r.status, 200);
    assert_eq!(r.body["limits"]["instance"], 2);
    assert_eq!(r.body["limits"]["vpc"], 5);
    assert_eq!(a.register("i-1", "instance", &[]).await.status, 201);
    assert_eq!(a.register("i-2", "instance", &[]).await.status, 201);
    let r = a.register("i-3", "instance", &[]).await;
    assert_error(&r, 429, "quota_exceeded");
    assert!(a.engine.get("i-3").is_none());
    let last = a.events(0).await.last().cloned().unwrap();
    assert_eq!(last["type"], "quota_rejected");
    assert_eq!(last["resource_id"], "i-3");

    // Another owner has its own quota.
    let r = a
        .put(
            "/v1/resources/i-9",
            json!({"kind":"instance","owner":"other"}),
        )
        .await;
    assert_eq!(r.status, 201);

    // Terminated instances do not count.
    a.act("i-1", "terminate").await;
    assert_eq!(a.register("i-3", "instance", &[]).await.status, 201);

    let q = a.get("/v1/quotas?owner=123456789012").await;
    assert_eq!(q.body["limits"]["instance"], 2);
    assert_eq!(q.body["limits"]["subnet"], 50);
    assert_eq!(q.body["usage"]["instance"], 2);

    // Restore with an explicit state bypasses quotas (restart recovery).
    let r = a
        .put(
            "/v1/resources/i-4",
            json!({"kind":"instance","owner":"123456789012","state":"running"}),
        )
        .await;
    assert_eq!(r.status, 201);

    // Defaults.
    let fresh = app();
    let q = fresh.get("/v1/quotas").await;
    for (k, v) in [
        ("instance", 20),
        ("vpc", 5),
        ("subnet", 50),
        ("bucket", 100),
        ("nat_gateway", 5),
        ("load_balancer", 10),
        ("autoscaling_group", 10),
        ("security_group", 100),
    ] {
        assert_eq!(q.body["limits"][k], v, "{k}");
    }
}

#[tokio::test]
async fn event_long_poll_ordering() {
    let a = app();
    let r = a.get("/v1/events?after=0").await;
    assert_eq!(r.body["events"], json!([]));
    assert_eq!(r.body["next"], 0);
    assert_eq!(r.body["oldest_available"], 1);

    // A long-poll waiting for events wakes when one is emitted.
    let router = a.router.clone();
    let waiter = tokio::spawn(async move {
        use tower::ServiceExt;
        let req = axum::http::Request::builder()
            .uri("/v1/events?after=0&wait_ms=5000")
            .header("X-Engine-Token", TOKEN)
            .body(axum::body::Body::empty())
            .unwrap();
        let start = std::time::Instant::now();
        let res = router.oneshot(req).await.unwrap();
        let bytes = http_body_util::BodyExt::collect(res.into_body())
            .await
            .unwrap()
            .to_bytes();
        (
            start.elapsed(),
            serde_json::from_slice::<serde_json::Value>(&bytes).unwrap(),
        )
    });
    tokio::time::sleep(Duration::from_millis(50)).await;
    a.register("vpc-1", "vpc", &[]).await;
    let (elapsed, body) = waiter.await.unwrap();
    assert!(elapsed < Duration::from_secs(4));
    assert_eq!(body["events"][0]["seq"], 1);
    assert_eq!(body["events"][0]["type"], "registered");

    for i in 0..20 {
        a.register(&format!("sg-{i}"), "security_group", &[]).await;
    }
    // Paging walks the log in strict seq order with no gaps.
    let mut after = 0;
    let mut seqs = vec![];
    loop {
        let r = a.get(&format!("/v1/events?after={after}&limit=7")).await;
        let evs = r.body["events"].as_array().unwrap().clone();
        if evs.is_empty() {
            assert_eq!(r.body["next"], after);
            break;
        }
        seqs.extend(evs.iter().map(|e| e["seq"].as_u64().unwrap()));
        after = r.body["next"].as_u64().unwrap();
    }
    assert_eq!(seqs, (1..=21).collect::<Vec<u64>>());

    // Timeout path returns empty.
    let start = std::time::Instant::now();
    let r = a.get("/v1/events?after=21&wait_ms=100").await;
    assert!(start.elapsed() >= Duration::from_millis(90));
    assert_eq!(r.body["events"], json!([]));
    assert_eq!(r.body["next"], 21);
}

#[tokio::test]
async fn event_ring_overflow_reports_oldest() {
    let a = app_with(|c| c.event_buffer = 5);
    for i in 0..10 {
        a.register(&format!("sg-{i}"), "security_group", &[]).await;
    }
    let r = a.get("/v1/events?after=0").await;
    assert_eq!(r.body["oldest_available"], 6);
    assert_eq!(r.body["events"][0]["seq"], 6);
    assert_eq!(r.body["next"], 10);
}

#[tokio::test]
async fn fault_crash() {
    let a = app();
    a.register("i-1", "instance", &[]).await;
    a.register("i-2", "instance", &[]).await;
    let r = a
        .post(
            "/v1/faults",
            json!({"target":"i-1","kind":null,"type":"crash","probability":1.0,"duration_ms":null}),
        )
        .await;
    assert_eq!(r.status, 201);
    assert_eq!(r.body["id"], "flt-1");
    assert_eq!(r.body["type"], "crash");
    let fault_id = r.body["id"].as_str().unwrap().to_string();

    // LB health reports a crash-faulted target as unhealthy even before the tick.
    let h = a
        .post(
            "/v1/lb/health",
            json!({"targets":[{"id":"i-1","port":80},{"id":"i-2","port":80}]}),
        )
        .await;
    assert_eq!(h.body["targets"][0]["state"], "unhealthy");
    assert_eq!(h.body["targets"][1]["state"], "healthy");

    a.engine.tick();
    assert_eq!(a.state("i-1").await, "stopped");
    assert_eq!(a.state("i-2").await, "running");
    let evs = a.events(0).await;
    let crash = evs
        .iter()
        .find(|e| e["resource_id"] == "i-1" && e["type"] == "state_changed" && e["to"] == "stopped")
        .unwrap();
    assert_eq!(
        crash["reason"],
        "Server.InternalError: simulated host failure"
    );
    assert!(evs
        .iter()
        .any(|e| e["type"] == "fault_triggered" && e["resource_id"] == "i-1"));
    assert!(evs.iter().any(|e| e["type"] == "fault_injected"));

    let list = a.get("/v1/faults").await;
    assert_eq!(list.body["items"][0]["triggered_count"], 1);
    assert_eq!(a.engine.stats().active_faults, 1);
    assert_eq!(
        a.delete(&format!("/v1/faults/{fault_id}")).await.status,
        204
    );
    assert_error(
        &a.delete(&format!("/v1/faults/{fault_id}")).await,
        404,
        "not_found",
    );
    assert!(a
        .events(0)
        .await
        .iter()
        .any(|e| e["type"] == "fault_cleared"));
    assert_eq!(a.engine.stats().active_faults, 0);
    // Restarted instance stays up once the fault is cleared.
    a.act("i-1", "start").await;
    a.engine.tick();
    assert_eq!(a.state("i-1").await, "running");
}

#[tokio::test]
async fn fault_start_failure_and_degrade() {
    let a = app();
    let r = a
        .post(
            "/v1/faults",
            json!({"target":null,"kind":"instance","type":"start_failure","probability":1.0}),
        )
        .await;
    assert_eq!(r.status, 201);
    let r = a.register("i-1", "instance", &[]).await;
    assert_eq!(r.body["state"], "stopped");
    let ev = a.events(0).await;
    let last = ev.last().unwrap();
    assert_eq!(last["from"], "pending");
    assert_eq!(last["to"], "stopped");
    assert_eq!(last["reason"], "Server.InsufficientInstanceCapacity");
    // One-shot: the next start succeeds.
    assert_eq!(a.act("i-1", "start").await.body["state"], "running");

    let r = a
        .post(
            "/v1/faults",
            json!({"target":"i-1","type":"degrade","probability":1.0}),
        )
        .await;
    assert_eq!(r.status, 201);
    let h = a
        .post(
            "/v1/lb/health",
            json!({"targets":[{"id":"i-1","port":80},{"id":"i-404","port":80}],"healthy_threshold":2,"unhealthy_threshold":2}),
        )
        .await;
    assert_eq!(h.body["targets"][0]["state"], "unhealthy");
    assert_eq!(h.body["targets"][0]["reason"], "Target.FailedHealthChecks");
    assert_eq!(h.body["targets"][1]["state"], "unhealthy");
    assert_eq!(h.body["targets"][1]["reason"], "Target.NotRegistered");
    a.act("i-1", "stop").await;
    let h = a
        .post("/v1/lb/health", json!({"targets":[{"id":"i-1"}]}))
        .await;
    assert_eq!(h.body["targets"][0]["state"], "unused");

    assert_error(
        &a.post("/v1/faults", json!({"type":"crash","probability":2.0}))
            .await,
        422,
        "invalid_request",
    );
    assert_error(
        &a.post("/v1/faults", json!({"type":"meteor"})).await,
        422,
        "invalid_request",
    );
    assert_error(
        &a.post("/v1/faults", json!({"type":"crash","target":"i-404"}))
            .await,
        404,
        "not_found",
    );
}

#[tokio::test]
async fn fault_duration_expires_on_tick() {
    let a = app();
    let r = a
        .post(
            "/v1/faults",
            json!({"type":"degrade","probability":1.0,"duration_ms":1}),
        )
        .await;
    assert!(r.body["expires_at"].is_string());
    tokio::time::sleep(Duration::from_millis(5)).await;
    a.engine.tick();
    assert_eq!(a.get("/v1/faults").await.body["items"], json!([]));
    let last = a.events(0).await.last().cloned().unwrap();
    assert_eq!(last["type"], "fault_cleared");
}

#[tokio::test]
async fn workload_metrics_and_lb_apply_load() {
    let a = app();
    let snap = a.get("/v1/metrics/snapshot").await;
    assert_eq!(snap.body["tick"], 0);
    assert_eq!(snap.body["samples"], json!([]));

    a.put(
        "/v1/resources/i-1",
        json!({"kind":"instance","owner":"o","attributes":{"workload":"idle"}}),
    )
    .await;
    a.register("i-2", "instance", &[]).await;
    a.register("i-3", "instance", &[]).await;
    a.act("i-3", "stop").await;
    let r = a
        .put(
            "/v1/resources/i-2/workload",
            json!({"profile":"stress","cpu_target":null,"extra_load_rps":0}),
        )
        .await;
    assert_eq!(r.status, 200);
    assert_eq!(r.body["workload"]["profile"], "stress");
    assert_error(
        &a.put("/v1/resources/i-2/workload", json!({"profile":"custom"}))
            .await,
        422,
        "invalid_request",
    );
    assert_error(
        &a.put("/v1/resources/i-2/workload", json!({"profile":"warp"}))
            .await,
        422,
        "invalid_request",
    );

    a.engine.tick();
    let snap = a.get("/v1/metrics/snapshot").await;
    assert_eq!(snap.body["tick"], 1);
    let samples = snap.body["samples"].as_array().unwrap();
    assert_eq!(
        samples.len(),
        2,
        "stopped instance must not produce samples"
    );
    assert_eq!(samples[0]["resource_id"], "i-1");
    assert_eq!(samples[0]["owner"], "o");
    let idle_cpu = samples[0]["cpu_percent"].as_f64().unwrap();
    assert!((2.0..=5.0).contains(&idle_cpu));
    assert!(samples[1]["cpu_percent"].as_f64().unwrap() >= 90.0);
    for k in [
        "memory_percent",
        "network_in_bytes",
        "network_out_bytes",
        "disk_read_bytes",
        "disk_write_bytes",
    ] {
        assert!(samples[0][k].is_number(), "{k}");
    }

    // LB simulation with apply_load raises CPU on the registered target.
    let r = a
        .post(
            "/v1/lb/simulate",
            json!({"algorithm":"round_robin","requests":6000,"targets":[{"id":"i-1","healthy":true,"weight":1,"capacity_rps":100}],
                   "seed":7,"apply_load":true,"duration_s":60}),
        )
        .await;
    assert_eq!(r.status, 200);
    assert_eq!(r.body["per_target"][0]["requests"], 6000);
    assert_eq!(
        a.get("/v1/resources/i-1").await.body["workload"]["extra_load_rps"],
        100.0
    );
    a.engine.tick();
    let snap = a.get("/v1/metrics/snapshot").await;
    assert!(snap.body["samples"][0]["cpu_percent"].as_f64().unwrap() >= 52.0);
}

#[tokio::test]
async fn stats_reflect_state() {
    let a = app_with(|c| c.seed = 7);
    a.register("vpc-1", "vpc", &[]).await;
    a.register("i-1", "instance", &[]).await;
    a.register("i-2", "instance", &[]).await;
    a.engine.tick();
    let s = a.get("/v1/stats").await.body;
    assert_eq!(s["resources"]["instance"], 2);
    assert_eq!(s["resources"]["vpc"], 1);
    assert_eq!(s["resources_total"], 3);
    assert_eq!(s["events_emitted"], 5);
    assert_eq!(s["event_seq"], 5);
    assert_eq!(s["pending_transitions"], 0);
    assert_eq!(s["active_faults"], 0);
    assert_eq!(s["ticks"], 1);
    assert_eq!(s["time_scale"], 0.0);
    assert_eq!(s["seed"], 7);
    assert!(s["uptime_seconds"].is_u64());
}

#[tokio::test]
async fn list_resources_filters() {
    let a = app();
    a.register("vpc-1", "vpc", &[]).await;
    a.register("i-1", "instance", &[]).await;
    a.put(
        "/v1/resources/i-2",
        json!({"kind":"instance","owner":"someone-else"}),
    )
    .await;
    let r = a.get("/v1/resources?kind=instance").await;
    assert_eq!(r.body["items"].as_array().unwrap().len(), 2);
    let r = a
        .get("/v1/resources?kind=instance&owner=123456789012")
        .await;
    let items = r.body["items"].as_array().unwrap();
    assert_eq!(items.len(), 1);
    assert_eq!(items[0]["id"], "i-1");
    assert_eq!(
        a.get("/v1/resources").await.body["items"]
            .as_array()
            .unwrap()
            .len(),
        3
    );
}

#[tokio::test]
async fn autoscaling_endpoint_emits_decision() {
    let a = app();
    let r = a
        .post(
            "/v1/autoscaling/evaluate",
            json!({"group":{"name":"web-asg","min":1,"max":4,"desired":2,"cooldown_s":60,"seconds_since_last_activity":120},
                   "policies":[{"name":"cpu50","type":"target_tracking","target_value":50.0}],
                   "instances":[{"id":"i-1","state":"running","cpu_percent":91.0,"healthy":true},
                                {"id":"i-2","state":"running","cpu_percent":91.0,"healthy":true},
                                {"id":"i-x","state":"stopped","cpu_percent":0.0,"healthy":true}]}),
        )
        .await;
    assert_eq!(r.status, 200);
    assert_eq!(r.body["desired"], 4);
    assert_eq!(r.body["change"], 2);
    assert_eq!(r.body["replace"], json!(["i-x"]));
    let last = a.events(0).await.last().cloned().unwrap();
    assert_eq!(last["type"], "scaling_decision");
    assert_eq!(last["resource_id"], "web-asg");
    assert_error(
        &a.post(
            "/v1/autoscaling/evaluate",
            json!({"group":{"min":3,"max":1,"desired":1}}),
        )
        .await,
        422,
        "invalid_request",
    );
}

#[tokio::test]
async fn storage_endpoints() {
    let a = app();
    let r = a
        .post(
            "/v1/storage/lifecycle/evaluate",
            json!({"now":"2026-11-15T00:00:00Z",
                   "rules":[{"id":"expire-logs","status":"Enabled","prefix":"logs/","expiration_days":30,
                             "noncurrent_expiration_days":7,"transitions":[{"days":30,"storage_class":"GLACIER"}],
                             "abort_incomplete_multipart_days":7}],
                   "objects":[{"key":"logs/a.txt","version_id":"v1","is_latest":true,"last_modified":"2026-09-01T00:00:00Z","size":10,"storage_class":"STANDARD","noncurrent_since":null},
                              {"key":"logs/b.txt","version_id":"v0","is_latest":false,"last_modified":"2026-09-01T00:00:00Z","size":10,"storage_class":"STANDARD","noncurrent_since":"2026-11-01T00:00:00Z"},
                              {"key":"img/c.png","version_id":"v1","is_latest":true,"last_modified":"2020-01-01T00:00:00Z","size":10,"storage_class":"STANDARD","noncurrent_since":null}],
                   "multipart_uploads":[{"upload_id":"u1","key":"logs/x","initiated":"2026-11-01T00:00:00Z"}]}),
        )
        .await;
    assert_eq!(r.status, 200, "{}", r.body);
    let actions = r.body["actions"].as_array().unwrap();
    assert_eq!(actions.len(), 3);
    assert_eq!(actions[0]["type"], "expire");
    assert_eq!(actions[0]["key"], "logs/a.txt");
    assert_eq!(actions[0]["rule_id"], "expire-logs");
    assert_eq!(actions[1]["type"], "expire_noncurrent");
    assert_eq!(actions[1]["version_id"], "v0");
    assert_eq!(actions[2]["type"], "abort_multipart");
    assert_eq!(actions[2]["upload_id"], "u1");

    let r = a
        .post(
            "/v1/storage/simulate",
            json!({"op":"put","size_bytes":1048576,"storage_class":"STANDARD"}),
        )
        .await;
    assert_eq!(r.status, 200);
    let lat = r.body["latency_ms"].as_f64().unwrap();
    assert!((20.0..21.0).contains(&lat), "{lat}");
    assert!(r.body["throughput_mbps"].as_f64().unwrap() > 0.0);
    let r = a
        .post(
            "/v1/storage/simulate",
            json!({"op":"get","size_bytes":10,"storage_class":"GLACIER"}),
        )
        .await;
    assert!(r.body["latency_ms"].as_f64().unwrap() >= 3_600_000.0);
}
