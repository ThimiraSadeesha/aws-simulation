use criterion::{black_box, criterion_group, criterion_main, Criterion, Throughput};

use cloudsim_engine::lb::{simulate, Algorithm, LbSimRequest, LbTarget};

fn req(alg: Algorithm) -> LbSimRequest {
    LbSimRequest {
        algorithm: alg,
        requests: 100_000,
        targets: (0..10)
            .map(|i| LbTarget {
                id: format!("i-{i}"),
                healthy: i != 9,
                weight: 1.0 + f64::from(i % 3),
                capacity_rps: Some(200.0 + 50.0 * f64::from(i)),
            })
            .collect(),
        seed: Some(7),
        apply_load: false,
        duration_s: 60.0,
    }
}

fn bench(c: &mut Criterion) {
    let mut g = c.benchmark_group("lb_100k_requests_10_targets");
    g.throughput(Throughput::Elements(100_000));
    for (name, alg) in [
        ("round_robin", Algorithm::RoundRobin),
        (
            "least_outstanding_requests",
            Algorithm::LeastOutstandingRequests,
        ),
        ("weighted_random", Algorithm::WeightedRandom),
    ] {
        let r = req(alg);
        g.bench_function(name, |b| {
            b.iter(|| black_box(simulate(black_box(&r), 42).unwrap()))
        });
    }
    g.finish();
}

criterion_group!(benches, bench);
criterion_main!(benches);
