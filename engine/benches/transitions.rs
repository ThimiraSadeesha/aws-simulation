use criterion::{black_box, criterion_group, criterion_main, Criterion, Throughput};

use cloudsim_engine::engine::RegisterRequest;
use cloudsim_engine::lifecycle;
use cloudsim_engine::{Config, Engine};

fn pure_transitions(c: &mut Criterion) {
    let mut g = c.benchmark_group("lifecycle");
    g.throughput(Throughput::Elements(4));
    g.bench_function("apply_action_instance_cycle", |b| {
        b.iter(|| {
            let t1 = lifecycle::apply_action(black_box("instance"), "running", "stop").unwrap();
            let t2 = lifecycle::apply_action("instance", "stopped", "start").unwrap();
            let t3 = lifecycle::apply_action("instance", "running", "reboot").unwrap();
            let t4 = lifecycle::apply_action("instance", "running", "terminate").unwrap();
            black_box((t1, t2, t3, t4))
        })
    });
    g.finish();
}

fn engine_transitions(c: &mut Criterion) {
    // time_scale 0: every action completes synchronously (state change + completion + events).
    let mut cfg = Config::for_tests("bench");
    cfg.event_buffer = 100_000;
    let engine = Engine::new(cfg);
    let n = 1_000;
    for i in 0..n {
        engine
            .register(
                &format!("i-{i}"),
                RegisterRequest {
                    kind: "instance".into(),
                    owner: format!("o{}", i % 100),
                    state: Some("running".into()),
                    depends_on: None,
                    attributes: None,
                },
            )
            .unwrap();
    }
    let mut g = c.benchmark_group("engine");
    g.throughput(Throughput::Elements(2));
    let mut i = 0usize;
    g.bench_function("stop_start_cycle_time_scale_0", |b| {
        b.iter(|| {
            let id = format!("i-{}", i % n);
            i += 1;
            engine.action(&id, "stop", None).unwrap();
            engine.action(&id, "start", None).unwrap();
        })
    });
    g.finish();
}

criterion_group!(benches, pure_transitions, engine_transitions);
criterion_main!(benches);
