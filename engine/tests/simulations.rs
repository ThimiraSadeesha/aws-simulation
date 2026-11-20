//! LB distribution, ASG decisions and storage lifecycle rules (pure + HTTP).

mod common;

use common::{app, app_with, assert_error};
use serde_json::{json, Value};

use cloudsim_engine::autoscaling::{evaluate, EvaluateRequest};
use cloudsim_engine::lb::{simulate, LbSimRequest};

fn lb_req(alg: &str, requests: u64, seed: Option<u64>) -> Value {
    json!({
        "algorithm": alg,
        "requests": requests,
        "seed": seed,
        "targets": [
            {"id":"i-1","healthy":true,"weight":1,"capacity_rps":100},
            {"id":"i-2","healthy":true,"weight":2,"capacity_rps":100},
            {"id":"i-3","healthy":true,"weight":1,"capacity_rps":100},
            {"id":"i-4","healthy":false,"weight":5,"capacity_rps":100}
        ],
        "apply_load": false,
        "duration_s": 60
    })
}

fn lb(v: Value) -> Value {
    let r: LbSimRequest = serde_json::from_value(v).unwrap();
    serde_json::to_value(simulate(&r, 42).unwrap()).unwrap()
}

fn counts(r: &Value) -> Vec<u64> {
    r["per_target"]
        .as_array()
        .unwrap()
        .iter()
        .map(|t| t["requests"].as_u64().unwrap())
        .collect()
}

#[test]
fn round_robin_exact_even_split() {
    let r = lb(lb_req("round_robin", 3000, Some(7)));
    assert_eq!(counts(&r), vec![1000, 1000, 1000]);
    assert_eq!(r["total"], 3000);
    assert_eq!(r["served"], 3000);
    assert_eq!(r["dropped"], 0);
    let ids: Vec<&str> = r["per_target"]
        .as_array()
        .unwrap()
        .iter()
        .map(|t| t["id"].as_str().unwrap())
        .collect();
    assert_eq!(
        ids,
        vec!["i-1", "i-2", "i-3"],
        "unhealthy targets are excluded"
    );
    for t in r["per_target"].as_array().unwrap() {
        assert!((t["percent"].as_f64().unwrap() - 33.33).abs() < 0.01);
        assert!(t["p99_latency_ms"].as_f64().unwrap() >= t["avg_latency_ms"].as_f64().unwrap());
    }
    // Remainders go to the first targets; split never differs by more than one.
    let c = counts(&lb(lb_req("round_robin", 1001, Some(7))));
    assert_eq!(c, vec![334, 334, 333]);
}

#[test]
fn least_outstanding_requests_distribution() {
    let r = lb(lb_req("least_outstanding_requests", 6000, Some(7)));
    let c = counts(&r);
    assert_eq!(c.iter().sum::<u64>(), 6000);
    // Identical capacities -> balanced within 1%.
    for n in &c {
        assert!((1940..=2060).contains(n), "{c:?}");
    }
}

#[test]
fn weighted_random_follows_weights() {
    let r = lb(lb_req("weighted_random", 40_000, Some(7)));
    let c = counts(&r);
    // weights 1:2:1 among healthy targets
    assert!((9_400..=10_600).contains(&c[0]), "{c:?}");
    assert!((19_200..=20_800).contains(&c[1]), "{c:?}");
    assert!((9_400..=10_600).contains(&c[2]), "{c:?}");
}

#[test]
fn determinism_with_same_seed() {
    for alg in [
        "round_robin",
        "least_outstanding_requests",
        "weighted_random",
    ] {
        let a = lb(lb_req(alg, 20_000, Some(11)));
        let b = lb(lb_req(alg, 20_000, Some(11)));
        assert_eq!(a, b, "{alg}");
    }
    let a = lb(lb_req("weighted_random", 20_000, Some(11)));
    let b = lb(lb_req("weighted_random", 20_000, Some(12)));
    assert_ne!(a, b);
    // Seed falls back to ENGINE_SEED when not given.
    let a = lb(lb_req("weighted_random", 5_000, None));
    let b = lb(lb_req("weighted_random", 5_000, None));
    assert_eq!(a, b);
}

#[tokio::test]
async fn lb_over_http_engine_seed() {
    let a = app_with(|c| c.seed = 99);
    let b = app_with(|c| c.seed = 99);
    let ra = a
        .post("/v1/lb/simulate", lb_req("weighted_random", 1000, None))
        .await;
    let rb = b
        .post("/v1/lb/simulate", lb_req("weighted_random", 1000, None))
        .await;
    assert_eq!(ra.status, 200);
    assert_eq!(ra.body, rb.body);
    let r = a
        .post(
            "/v1/lb/simulate",
            json!({"algorithm":"round_robin","requests":10,"targets":[{"id":"x","healthy":false}]}),
        )
        .await;
    assert_eq!(r.body["dropped"], 10);
    assert_eq!(r.body["served"], 0);
    assert_error(
        &a.post(
            "/v1/lb/simulate",
            json!({"algorithm":"random","requests":10,"targets":[]}),
        )
        .await,
        422,
        "invalid_request",
    );
}

// ---- autoscaling ------------------------------------------------------------

fn asg(
    desired: u32,
    min: u32,
    max: u32,
    since: Option<f64>,
    cpus: &[f64],
    target: f64,
) -> EvaluateRequest {
    serde_json::from_value(json!({
        "group": {"name":"web-asg","min":min,"max":max,"desired":desired,"cooldown_s":60,"seconds_since_last_activity":since},
        "policies": [{"name":"cpu50","type":"target_tracking","target_value":target}],
        "instances": cpus.iter().enumerate().map(|(i, c)| json!({"id": format!("i-{i}"), "state":"running","cpu_percent":c,"healthy":true})).collect::<Vec<_>>()
    }))
    .unwrap()
}

#[test]
fn asg_target_tracking_scale_out() {
    // ceil(2 * 75 / 50) = 3
    let d = evaluate(&asg(2, 1, 10, Some(300.0), &[75.0, 75.0], 50.0)).unwrap();
    assert_eq!(d.desired, 3);
    assert_eq!(d.change, 1);
    assert_eq!(
        d.reason,
        "Policy cpu50: average CPU 75.0% > target 50.0%, scaling to 3"
    );
}

#[test]
fn asg_target_tracking_scale_in_with_hysteresis() {
    // avg 35 < 40 (0.8*50): ceil(4*35/50)=3
    let d = evaluate(&asg(4, 1, 10, Some(300.0), &[35.0; 4], 50.0)).unwrap();
    assert_eq!((d.desired, d.change), (3, -1));
    assert!(d.reason.contains("scaling in to 3"));
    // avg 44 >= 40: ceil(4*44/50)=4 → hold
    let d = evaluate(&asg(4, 1, 10, Some(300.0), &[44.0; 4], 50.0)).unwrap();
    assert_eq!(d.change, 0);
    // avg 49: raw 4 → still within band
    let d = evaluate(&asg(5, 1, 10, Some(300.0), &[49.0; 4], 50.0)).unwrap();
    assert_eq!(d.change, 0, "{}", d.reason);
    assert!(d.reason.contains("hysteresis"));
}

#[test]
fn asg_cooldown() {
    let d = evaluate(&asg(2, 1, 10, Some(30.0), &[99.0, 99.0], 50.0)).unwrap();
    assert_eq!(d.change, 0);
    assert_eq!(d.desired, 2);
    assert!(d.reason.to_lowercase().contains("cooldown"));
    // Exactly at the cooldown boundary scaling is allowed.
    let d = evaluate(&asg(2, 1, 10, Some(60.0), &[99.0, 99.0], 50.0)).unwrap();
    assert_eq!(d.desired, 4);
}

#[test]
fn asg_clamp() {
    let d = evaluate(&asg(2, 1, 4, None, &[100.0, 100.0, 100.0], 10.0)).unwrap();
    assert_eq!(d.desired, 4);
    assert!(d.reason.ends_with("(capped by max 4)"));
    let d = evaluate(&asg(3, 2, 10, Some(600.0), &[1.0, 1.0, 1.0], 50.0)).unwrap();
    assert_eq!(d.desired, 2);
    assert!(d.reason.contains("(raised to min 2)"));
    // Out-of-range desired is clamped even without a policy firing (and during cooldown).
    let d = evaluate(&asg(12, 1, 10, Some(5.0), &[50.0], 50.0)).unwrap();
    assert_eq!((d.desired, d.change), (10, -2));
}

#[test]
fn asg_replace_list() {
    let req: EvaluateRequest = serde_json::from_value(json!({
        "group": {"name":"g","min":1,"max":4,"desired":3,"cooldown_s":0},
        "policies": [],
        "instances": [
            {"id":"i-ok","state":"running","cpu_percent":40.0,"healthy":true},
            {"id":"i-sick","state":"running","cpu_percent":40.0,"healthy":false},
            {"id":"i-boot","state":"pending","healthy":true},
            {"id":"i-dead","state":"stopped","healthy":true},
            {"id":"i-term","state":"terminated","healthy":true}
        ]
    }))
    .unwrap();
    let d = evaluate(&req).unwrap();
    assert_eq!(d.replace, vec!["i-sick", "i-dead", "i-term"]);
    assert_eq!(d.change, 0);
}

// ---- storage lifecycle ---------------------------------------------------------

#[tokio::test]
async fn lifecycle_rule_evaluation() {
    let a = app();
    let r = a
        .post(
            "/v1/storage/lifecycle/evaluate",
            json!({"now":"2026-11-15T00:00:00Z",
              "rules":[
                {"id":"to-ia","status":"Enabled","prefix":"data/","transitions":[{"days":30,"storage_class":"STANDARD_IA"},{"days":90,"storage_class":"GLACIER"}]},
                {"id":"tmp","status":"Enabled","prefix":"tmp/","expiration_days":1,"abort_incomplete_multipart_days":1},
                {"id":"off","status":"Disabled","prefix":"","expiration_days":1}
              ],
              "objects":[
                {"key":"data/fresh","version_id":null,"is_latest":true,"last_modified":"2026-11-10T00:00:00Z","size":1,"storage_class":"STANDARD"},
                {"key":"data/month","version_id":null,"is_latest":true,"last_modified":"2026-10-10T00:00:00Z","size":1,"storage_class":"STANDARD"},
                {"key":"data/old","version_id":null,"is_latest":true,"last_modified":"2026-01-10T00:00:00Z","size":1,"storage_class":"STANDARD_IA"},
                {"key":"tmp/x","version_id":"v1","is_latest":true,"last_modified":"2026-11-13T00:00:00Z","size":1,"storage_class":"STANDARD"},
                {"key":"tmp/y","version_id":"v1","is_latest":true,"last_modified":"2026-11-14T12:00:00Z","size":1,"storage_class":"STANDARD"}
              ],
              "multipart_uploads":[
                {"upload_id":"u-old","key":"tmp/big","initiated":"2026-11-10T00:00:00Z"},
                {"upload_id":"u-data","key":"data/big","initiated":"2026-01-01T00:00:00Z"}
              ]}),
        )
        .await;
    assert_eq!(r.status, 200, "{}", r.body);
    let got: Vec<(String, String, Value, String)> = r.body["actions"]
        .as_array()
        .unwrap()
        .iter()
        .map(|a| {
            (
                a["type"].as_str().unwrap().to_string(),
                a["key"].as_str().unwrap().to_string(),
                a["storage_class"].clone(),
                a["rule_id"].as_str().unwrap().to_string(),
            )
        })
        .collect();
    assert_eq!(
        got,
        vec![
            (
                "transition".into(),
                "data/month".into(),
                json!("STANDARD_IA"),
                "to-ia".into()
            ),
            (
                "transition".into(),
                "data/old".into(),
                json!("GLACIER"),
                "to-ia".into()
            ),
            ("expire".into(), "tmp/x".into(), Value::Null, "tmp".into()),
            (
                "abort_multipart".into(),
                "tmp/big".into(),
                Value::Null,
                "tmp".into()
            ),
        ]
    );
    assert_error(
        &a.post(
            "/v1/storage/lifecycle/evaluate",
            json!({"now":"not-a-date","rules":[],"objects":[]}),
        )
        .await,
        422,
        "invalid_request",
    );
}
