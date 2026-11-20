use criterion::{black_box, criterion_group, criterion_main, BatchSize, Criterion, Throughput};

use cloudsim_engine::events::{EventBus, EventLog, EventType, NewEvent};

fn ev(i: u64) -> NewEvent {
    NewEvent::new(EventType::StateChanged, "Instance boot completed")
        .resource(&format!("i-{i}"), "instance", "123456789012")
        .from_to(Some("pending"), Some("running"))
}

fn ingestion(c: &mut Criterion) {
    let mut g = c.benchmark_group("events");
    g.throughput(Throughput::Elements(10_000));
    g.bench_function("ingest_10k_into_ring_10k", |b| {
        b.iter_batched(
            || EventLog::new(10_000),
            |mut log| {
                for i in 0..10_000 {
                    log.push(ev(i));
                }
                black_box(log.last_seq())
            },
            BatchSize::LargeInput,
        )
    });
    g.bench_function("ingest_10k_via_bus", |b| {
        let bus = EventBus::new(10_000);
        b.iter(|| {
            for i in 0..10_000 {
                bus.emit(ev(i));
            }
        })
    });
    g.finish();

    let mut g = c.benchmark_group("events_read");
    let mut log = EventLog::new(10_000);
    for i in 0..50_000 {
        log.push(ev(i));
    }
    g.throughput(Throughput::Elements(1000));
    g.bench_function("page_1000_from_full_ring", |b| {
        b.iter(|| black_box(log.page(black_box(45_000), 1000)))
    });
    g.finish();
}

criterion_group!(benches, ingestion);
criterion_main!(benches);
