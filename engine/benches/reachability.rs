use criterion::{black_box, criterion_group, criterion_main, Criterion};
use serde_json::{json, Value};

use cloudsim_engine::network::{evaluate_reachability, ReachabilityRequest};

/// 2 VPCs x 25 subnets (odd subnets private behind a NAT), 4 endpoints per subnet,
/// SG with 20 rules, NACLs with 10 entries.
fn topology() -> Value {
    let mut vpcs = vec![];
    let mut subnets = vec![];
    let mut rts = vec![];
    let mut igws = vec![];
    let mut nats = vec![];
    let mut sgs = vec![];
    let mut acls = vec![];
    let mut eps = vec![];
    for v in 0..2 {
        let vpc = format!("vpc-{v}");
        vpcs.push(json!({"id": vpc, "cidr": format!("10.{v}.0.0/16")}));
        igws.push(json!({"id": format!("igw-{v}"), "vpc_id": vpc}));
        rts.push(
            json!({"id": format!("rtb-pub-{v}"), "vpc_id": vpc, "routes": [
            {"destination": format!("10.{v}.0.0/16"), "target": "local"},
            {"destination": "0.0.0.0/0", "target": format!("igw-{v}")}]}),
        );
        rts.push(
            json!({"id": format!("rtb-priv-{v}"), "vpc_id": vpc, "routes": [
            {"destination": format!("10.{v}.0.0/16"), "target": "local"},
            {"destination": "0.0.0.0/0", "target": format!("nat-{v}")}]}),
        );
        let mut ingress: Vec<Value> = (0..19)
            .map(|p| json!({"protocol":"tcp","from_port":8000+p,"to_port":8000+p,"cidr":"10.0.0.0/8"}))
            .collect();
        ingress.push(json!({"protocol":"tcp","from_port":22,"to_port":22,"cidr":"0.0.0.0/0"}));
        sgs.push(
            json!({"id": format!("sg-{v}"), "vpc_id": vpc, "ingress": ingress,
            "egress": [{"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0"}]}),
        );
        let mut entries: Vec<Value> = (0..4)
            .map(|i| json!({"rule_number": 10 + i, "egress": false, "protocol": "tcp", "from_port": 3000+i, "to_port": 3000+i, "cidr": "192.0.2.0/24", "action": "deny"}))
            .collect();
        entries.push(json!({"rule_number":100,"egress":false,"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","action":"allow"}));
        entries.extend((0..4).map(|i| json!({"rule_number": 10 + i, "egress": true, "protocol": "udp", "from_port": 5000+i, "to_port": 5000+i, "cidr": "192.0.2.0/24", "action": "deny"})));
        entries.push(json!({"rule_number":100,"egress":true,"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","action":"allow"}));
        acls.push(json!({"id": format!("acl-{v}"), "vpc_id": vpc, "entries": entries}));
        for s in 0..25 {
            let id = format!("subnet-{v}-{s}");
            let public = s % 2 == 0;
            subnets.push(json!({"id": id, "vpc_id": vpc, "cidr": format!("10.{v}.{s}.0/24"),
                "route_table_id": if public { format!("rtb-pub-{v}") } else { format!("rtb-priv-{v}") },
                "network_acl_id": format!("acl-{v}")}));
            if s == 0 {
                nats.push(json!({"id": format!("nat-{v}"), "subnet_id": id, "state": "available", "public_ip": format!("203.0.113.{}", 10 + v)}));
            }
            for e in 0..4 {
                let mut ep = json!({"id": format!("i-{v}-{s}-{e}"), "subnet_id": id,
                    "private_ip": format!("10.{v}.{s}.{}", 10 + e), "security_group_ids": [format!("sg-{v}")], "state": "running"});
                if public {
                    ep["public_ip"] = json!(format!("54.{v}.{s}.{}", 10 + e));
                }
                eps.push(ep);
            }
        }
    }
    json!({"vpcs": vpcs, "subnets": subnets, "route_tables": rts, "internet_gateways": igws,
        "nat_gateways": nats, "security_groups": sgs, "network_acls": acls, "endpoints": eps})
}

fn req(topo: &Value, src: &str, dst: &str, port: u16) -> ReachabilityRequest {
    serde_json::from_value(json!({"topology": topo, "probe": {"source": src, "destination": dst, "protocol": "tcp", "port": port}})).unwrap()
}

fn bench(c: &mut Criterion) {
    let topo = topology();
    let mut g = c.benchmark_group("reachability_50_subnets");
    let cases = [
        (
            "internet_to_public_ssh",
            req(&topo, "internet", "i-0-4-1", 22),
        ),
        (
            "same_vpc_cross_subnet",
            req(&topo, "i-0-3-0", "i-0-20-2", 8010),
        ),
        (
            "private_to_internet_via_nat",
            req(&topo, "i-1-7-3", "8.8.8.8", 443),
        ),
        ("blocked_sg", req(&topo, "internet", "i-0-2-0", 3389)),
    ];
    for (name, r) in &cases {
        g.bench_function(*name, |b| {
            b.iter(|| black_box(evaluate_reachability(black_box(r), 42).unwrap()))
        });
    }
    g.finish();
}

criterion_group!(benches, bench);
criterion_main!(benches);
