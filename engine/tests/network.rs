mod common;

use common::{app, assert_error};
use serde_json::{json, Value};

use cloudsim_engine::network::{evaluate_reachability, ReachabilityRequest};

/// vpc-1 10.0.0.0/16
///   subnet-pub  10.0.1.0/24  rtb-pub  (0.0.0.0/0 -> igw-1)   acl-open
///   subnet-priv 10.0.2.0/24  rtb-priv (0.0.0.0/0 -> nat-1)   acl-open
///   subnet-locked 10.0.3.0/24 rtb-pub                        acl-deny22 (rule 90 deny tcp 22)
///   subnet-noeph 10.0.4.0/24 rtb-pub                         acl-noeph (inbound 22 only, outbound 22 only)
/// nat-1 lives in subnet-pub.
fn topology(nat_state: &str) -> Value {
    json!({
      "vpcs":[{"id":"vpc-1","cidr":"10.0.0.0/16"},{"id":"vpc-2","cidr":"10.1.0.0/16"}],
      "subnets":[
        {"id":"subnet-pub","vpc_id":"vpc-1","cidr":"10.0.1.0/24","route_table_id":"rtb-pub","network_acl_id":"acl-open"},
        {"id":"subnet-priv","vpc_id":"vpc-1","cidr":"10.0.2.0/24","route_table_id":"rtb-priv","network_acl_id":"acl-open"},
        {"id":"subnet-locked","vpc_id":"vpc-1","cidr":"10.0.3.0/24","route_table_id":"rtb-pub","network_acl_id":"acl-deny22"},
        {"id":"subnet-noeph","vpc_id":"vpc-1","cidr":"10.0.4.0/24","route_table_id":"rtb-pub","network_acl_id":"acl-noeph"},
        {"id":"subnet-other","vpc_id":"vpc-2","cidr":"10.1.1.0/24","route_table_id":"rtb-other","network_acl_id":null}
      ],
      "route_tables":[
        {"id":"rtb-pub","vpc_id":"vpc-1","routes":[{"destination":"10.0.0.0/16","target":"local"},{"destination":"0.0.0.0/0","target":"igw-1"}]},
        {"id":"rtb-priv","vpc_id":"vpc-1","routes":[{"destination":"10.0.0.0/16","target":"local"},{"destination":"0.0.0.0/0","target":"nat-1"}]},
        {"id":"rtb-other","vpc_id":"vpc-2","routes":[{"destination":"10.1.0.0/16","target":"local"},{"destination":"0.0.0.0/0","target":"igw-2"}]}
      ],
      "internet_gateways":[{"id":"igw-1","vpc_id":"vpc-1"},{"id":"igw-2","vpc_id":"vpc-2"}],
      "nat_gateways":[{"id":"nat-1","subnet_id":"subnet-pub","state":nat_state,"public_ip":"203.0.113.10"}],
      "security_groups":[
        {"id":"sg-web","vpc_id":"vpc-1",
         "ingress":[{"protocol":"tcp","from_port":22,"to_port":22,"cidr":"0.0.0.0/0","source_sg":null},
                    {"protocol":"tcp","from_port":443,"to_port":443,"cidr":"0.0.0.0/0","source_sg":null}],
         "egress":[{"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","source_sg":null}]},
        {"id":"sg-app","vpc_id":"vpc-1",
         "ingress":[{"protocol":"tcp","from_port":8080,"to_port":8080,"cidr":null,"source_sg":"sg-web"}],
         "egress":[{"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","source_sg":null}]},
        {"id":"sg-other","vpc_id":"vpc-2",
         "ingress":[{"protocol":"tcp","from_port":443,"to_port":443,"cidr":"0.0.0.0/0"}],
         "egress":[{"protocol":"-1","cidr":"0.0.0.0/0"}]}
      ],
      "network_acls":[
        {"id":"acl-open","vpc_id":"vpc-1","entries":[
          {"rule_number":100,"egress":false,"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","action":"allow"},
          {"rule_number":100,"egress":true,"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","action":"allow"}]},
        {"id":"acl-deny22","vpc_id":"vpc-1","entries":[
          {"rule_number":90,"egress":false,"protocol":"tcp","from_port":22,"to_port":22,"cidr":"0.0.0.0/0","action":"deny"},
          {"rule_number":100,"egress":false,"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","action":"allow"},
          {"rule_number":100,"egress":true,"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","action":"allow"}]},
        {"id":"acl-noeph","vpc_id":"vpc-1","entries":[
          {"rule_number":100,"egress":false,"protocol":"tcp","from_port":22,"to_port":22,"cidr":"0.0.0.0/0","action":"allow"},
          {"rule_number":100,"egress":true,"protocol":"tcp","from_port":22,"to_port":22,"cidr":"0.0.0.0/0","action":"allow"}]}
      ],
      "endpoints":[
        {"id":"i-web","subnet_id":"subnet-pub","private_ip":"10.0.1.10","public_ip":"54.0.0.1","security_group_ids":["sg-web"],"state":"running"},
        {"id":"i-web2","subnet_id":"subnet-pub","private_ip":"10.0.1.11","public_ip":"54.0.0.2","security_group_ids":["sg-web"],"state":"running"},
        {"id":"i-app","subnet_id":"subnet-priv","private_ip":"10.0.2.10","public_ip":null,"security_group_ids":["sg-app"],"state":"running"},
        {"id":"i-locked","subnet_id":"subnet-locked","private_ip":"10.0.3.10","public_ip":"54.0.0.3","security_group_ids":["sg-web"],"state":"running"},
        {"id":"i-noeph","subnet_id":"subnet-noeph","private_ip":"10.0.4.10","public_ip":"54.0.0.4","security_group_ids":["sg-web"],"state":"running"},
        {"id":"i-stopped","subnet_id":"subnet-pub","private_ip":"10.0.1.20","public_ip":"54.0.0.5","security_group_ids":["sg-web"],"state":"stopped"},
        {"id":"i-other","subnet_id":"subnet-other","private_ip":"10.1.1.10","public_ip":"54.1.0.1","security_group_ids":["sg-other"],"state":"running"}
      ]
    })
}

fn probe(topo: Value, src: &str, dst: &str, proto: &str, port: Option<u16>) -> Value {
    let req: ReachabilityRequest = serde_json::from_value(json!({
        "topology": topo,
        "probe": {"source": src, "destination": dst, "protocol": proto, "port": port}
    }))
    .unwrap();
    serde_json::to_value(evaluate_reachability(&req, 42).unwrap()).unwrap()
}

#[test]
fn public_subnet_ssh_allowed() {
    let r = probe(topology("available"), "internet", "i-web", "tcp", Some(22));
    assert_eq!(r["reachable"], true, "{r}");
    assert!(r["blocked_by"].is_null());
    let types: Vec<&str> = r["hops"]
        .as_array()
        .unwrap()
        .iter()
        .map(|h| h["type"].as_str().unwrap())
        .collect();
    assert!(types.contains(&"internet_gateway"));
    assert!(types.contains(&"security_group"));
    assert!(types.contains(&"network_acl"));
    let l = r["simulated_latency_ms"].as_f64().unwrap();
    assert!((15.0..=16.5).contains(&l), "{l}");
    // Also addressable by public IP.
    assert_eq!(
        probe(
            topology("available"),
            "internet",
            "54.0.0.1",
            "tcp",
            Some(22)
        )["reachable"],
        true
    );
}

#[test]
fn sg_missing_port_blocked() {
    let r = probe(topology("available"), "internet", "i-web", "tcp", Some(80));
    assert_eq!(r["reachable"], false);
    assert_eq!(r["blocked_by"]["type"], "security_group");
    assert_eq!(r["blocked_by"]["component"], "sg-web");
    assert!(r["explanation"].as_str().unwrap().contains("sg-web"));
}

#[test]
fn nacl_deny_blocked_with_blocked_by() {
    let r = probe(
        topology("available"),
        "internet",
        "i-locked",
        "tcp",
        Some(22),
    );
    assert_eq!(r["reachable"], false);
    assert_eq!(r["blocked_by"]["component"], "acl-deny22");
    assert_eq!(r["blocked_by"]["type"], "network_acl");
    assert_eq!(r["blocked_by"]["detail"], "inbound rule 90 deny");
    // 443 passes rule 100 on the same NACL.
    assert_eq!(
        probe(
            topology("available"),
            "internet",
            "i-locked",
            "tcp",
            Some(443)
        )["reachable"],
        true
    );
}

#[test]
fn nacl_implicit_deny_and_return_traffic() {
    // acl-noeph allows inbound 22 but not outbound ephemeral ports -> return traffic blocked.
    let r = probe(
        topology("available"),
        "internet",
        "i-noeph",
        "tcp",
        Some(22),
    );
    assert_eq!(r["reachable"], false);
    assert_eq!(r["blocked_by"]["component"], "acl-noeph");
    assert_eq!(r["blocked_by"]["detail"], "outbound rule * deny");
    assert!(r["explanation"].as_str().unwrap().contains("implicit deny"));
    // 443 is not even allowed inbound.
    let r = probe(
        topology("available"),
        "internet",
        "i-noeph",
        "tcp",
        Some(443),
    );
    assert_eq!(r["blocked_by"]["detail"], "inbound rule * deny");
}

#[test]
fn private_instance_unreachable_from_internet() {
    let r = probe(
        topology("available"),
        "internet",
        "i-app",
        "tcp",
        Some(8080),
    );
    assert_eq!(r["reachable"], false);
    assert_eq!(r["blocked_by"]["component"], "i-app");
    assert!(r["explanation"]
        .as_str()
        .unwrap()
        .contains("not reachable from the internet"));
    // Even by private IP.
    assert_eq!(
        probe(
            topology("available"),
            "internet",
            "10.0.2.10",
            "tcp",
            Some(8080)
        )["reachable"],
        false
    );
}

#[test]
fn private_instance_reaches_internet_via_available_nat() {
    let r = probe(topology("available"), "i-app", "internet", "tcp", Some(443));
    assert_eq!(r["reachable"], true, "{r}");
    let nat_hop = r["hops"]
        .as_array()
        .unwrap()
        .iter()
        .find(|h| h["type"] == "nat_gateway")
        .unwrap();
    assert_eq!(nat_hop["result"], "allow");
    assert_eq!(nat_hop["component"], "nat-1");
    let l = r["simulated_latency_ms"].as_f64().unwrap();
    assert!((18.0..=19.8).contains(&l), "{l}");
    // Arbitrary internet IP works too.
    assert_eq!(
        probe(topology("available"), "i-app", "8.8.8.8", "udp", Some(53))["reachable"],
        true
    );
}

#[test]
fn nat_pending_blocks() {
    let r = probe(topology("pending"), "i-app", "internet", "tcp", Some(443));
    assert_eq!(r["reachable"], false);
    assert_eq!(r["blocked_by"]["component"], "nat-1");
    assert_eq!(r["blocked_by"]["type"], "nat_gateway");
    assert!(r["explanation"].as_str().unwrap().contains("pending"));
}

#[test]
fn same_vpc_local_routing() {
    // Cross-subnet within the VPC, allowed by source_sg reference.
    let r = probe(topology("available"), "i-web", "i-app", "tcp", Some(8080));
    assert_eq!(r["reachable"], true, "{r}");
    let rt = r["hops"]
        .as_array()
        .unwrap()
        .iter()
        .find(|h| h["type"] == "route_table")
        .unwrap();
    assert_eq!(rt["detail"], "10.0.0.0/16 -> local");
    let l = r["simulated_latency_ms"].as_f64().unwrap();
    assert!((0.5..=0.55).contains(&l), "{l}");
    // Same subnet: no NACL hops, 0.2 ms.
    let r = probe(topology("available"), "i-web", "i-web2", "tcp", Some(22));
    assert_eq!(r["reachable"], true);
    assert!(r["hops"]
        .as_array()
        .unwrap()
        .iter()
        .all(|h| h["type"] != "network_acl"));
    let l = r["simulated_latency_ms"].as_f64().unwrap();
    assert!((0.2..=0.22).contains(&l), "{l}");
    // App SG does not allow 9090 from web.
    let r = probe(topology("available"), "i-web", "i-app", "tcp", Some(9090));
    assert_eq!(r["blocked_by"]["component"], "sg-app");
    // Private app can't reach web on 8080 (web SG lacks it).
    assert_eq!(
        probe(
            topology("available"),
            "10.0.2.10",
            "10.0.1.10",
            "tcp",
            Some(8080)
        )["reachable"],
        false
    );
}

#[test]
fn stopped_endpoint_and_public_ip_requirements() {
    let r = probe(
        topology("available"),
        "internet",
        "i-stopped",
        "tcp",
        Some(22),
    );
    assert_eq!(r["reachable"], false);
    assert_eq!(r["blocked_by"]["type"], "endpoint");
    // Cross-VPC over public IPs via both IGWs.
    let r = probe(topology("available"), "i-web", "i-other", "tcp", Some(443));
    assert_eq!(r["reachable"], true, "{r}");
    // Cross-VPC to a private-only endpoint is not simulated (no peering).
    let r = probe(topology("available"), "i-other", "i-app", "tcp", Some(8080));
    assert_eq!(r["reachable"], false);
}

#[test]
fn deterministic_latency() {
    let a = probe(topology("available"), "internet", "i-web", "tcp", Some(22));
    let b = probe(topology("available"), "internet", "i-web", "tcp", Some(22));
    assert_eq!(a, b);
}

#[tokio::test]
async fn reachability_and_cidr_over_http() {
    let a = app();
    let r = a
        .post(
            "/v1/network/reachability",
            json!({"topology": topology("available"), "probe": {"source":"internet","destination":"i-web","protocol":"tcp","port":22}}),
        )
        .await;
    assert_eq!(r.status, 200);
    assert_eq!(r.body["reachable"], true);
    let r = a
        .post(
            "/v1/network/reachability",
            json!({"topology": topology("available"), "probe": {"source":"internet","destination":"i-nope","protocol":"tcp","port":22}}),
        )
        .await;
    assert_error(&r, 422, "invalid_request");

    let r = a
        .post(
            "/v1/network/cidr",
            json!({"op":"validate","cidr":"10.0.0.0/16"}),
        )
        .await;
    assert_eq!(
        r.body,
        json!({"valid":true,"network":"10.0.0.0","prefix":16,"first_host":"10.0.0.1","last_host":"10.0.255.254","size":65536})
    );
    let r = a
        .post(
            "/v1/network/cidr",
            json!({"op":"validate","cidr":"10.0.0.0/33"}),
        )
        .await;
    assert_eq!(r.status, 200);
    assert_eq!(r.body["valid"], false);
    let r = a
        .post(
            "/v1/network/cidr",
            json!({"op":"contains","cidr":"10.0.0.0/16","other":"10.0.1.0/24"}),
        )
        .await;
    assert_eq!(r.body["contains"], true);
    let r = a
        .post(
            "/v1/network/cidr",
            json!({"op":"overlaps","cidr":"10.0.0.0/16","other":"10.1.0.0/16"}),
        )
        .await;
    assert_eq!(r.body["overlaps"], false);
    let r = a
        .post(
            "/v1/network/cidr",
            json!({"op":"subnets","cidr":"10.0.0.0/16","new_prefix":24}),
        )
        .await;
    let subs = r.body["subnets"].as_array().unwrap();
    assert_eq!(subs.len(), 256);
    assert_eq!(subs[0], "10.0.0.0/24");
    assert_eq!(subs[1], "10.0.1.0/24");
    let r = a
        .post(
            "/v1/network/cidr",
            json!({"op":"subnets","cidr":"10.0.0.0/8","new_prefix":24}),
        )
        .await;
    assert_eq!(r.body["subnets"].as_array().unwrap().len(), 256);
    assert_eq!(r.body["total_subnets"], 65536);
    assert_error(
        &a.post(
            "/v1/network/cidr",
            json!({"op":"subnets","cidr":"10.0.0.0/16","new_prefix":8}),
        )
        .await,
        422,
        "invalid_request",
    );
    assert_error(
        &a.post(
            "/v1/network/cidr",
            json!({"op":"contains","cidr":"10.0.0.0/16"}),
        )
        .await,
        422,
        "invalid_request",
    );
}
