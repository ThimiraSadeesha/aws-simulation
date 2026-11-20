//! Stateless VPC reachability evaluation (`POST /v1/network/reachability`).
//!
//! Models, in order: endpoint state, source SG egress, source NACL outbound, routing
//! (local / IGW / NAT), destination NACL inbound, destination SG ingress, and stateless
//! NACL return traffic on ephemeral ports 1024-65535. Traffic that stays inside one
//! subnet does not cross a NACL (as in AWS). VPC peering / transit gateways are not
//! simulated; cross-VPC traffic is evaluated as internet traffic via public IPs.

use std::collections::HashMap;

use rand::Rng;
use serde::{Deserialize, Serialize};

use super::cidr::{ip_to_string, parse_ipv4, Cidr};
use crate::util::{round_to, SeedMix};

/// Representative address used for the literal source/destination `"internet"`.
pub const INTERNET_IP: &str = "198.51.100.1";
pub const EPHEMERAL_FROM: i64 = 1024;
pub const EPHEMERAL_TO: i64 = 65535;

// ---- request ----------------------------------------------------------------

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct Topology {
    pub vpcs: Vec<Vpc>,
    pub subnets: Vec<Subnet>,
    pub route_tables: Vec<RouteTable>,
    pub internet_gateways: Vec<InternetGateway>,
    pub nat_gateways: Vec<NatGateway>,
    pub security_groups: Vec<SecurityGroup>,
    pub network_acls: Vec<NetworkAcl>,
    pub endpoints: Vec<Endpoint>,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct Vpc {
    pub id: String,
    pub cidr: String,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct Subnet {
    pub id: String,
    pub vpc_id: String,
    pub cidr: String,
    pub route_table_id: Option<String>,
    pub network_acl_id: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct RouteTable {
    pub id: String,
    pub vpc_id: String,
    pub routes: Vec<Route>,
    /// Optional: the VPC's main route table, used by subnets without `route_table_id`.
    pub main: bool,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct Route {
    pub destination: String,
    pub target: String,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct InternetGateway {
    pub id: String,
    pub vpc_id: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct NatGateway {
    pub id: String,
    pub subnet_id: String,
    pub state: String,
    pub public_ip: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct SecurityGroup {
    pub id: String,
    pub vpc_id: Option<String>,
    pub ingress: Vec<SgRule>,
    pub egress: Vec<SgRule>,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct SgRule {
    pub protocol: Option<String>,
    pub from_port: Option<i64>,
    pub to_port: Option<i64>,
    pub cidr: Option<String>,
    pub source_sg: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct NetworkAcl {
    pub id: String,
    pub vpc_id: Option<String>,
    pub entries: Vec<NaclEntry>,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct NaclEntry {
    pub rule_number: i64,
    pub egress: bool,
    pub protocol: Option<String>,
    pub from_port: Option<i64>,
    pub to_port: Option<i64>,
    pub cidr: Option<String>,
    pub action: String,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct Endpoint {
    pub id: String,
    pub subnet_id: String,
    pub private_ip: String,
    pub public_ip: Option<String>,
    pub security_group_ids: Vec<String>,
    /// Missing state is treated as `running`.
    pub state: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct Probe {
    pub source: String,
    pub destination: String,
    pub protocol: Option<String>,
    pub port: Option<i64>,
}

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct ReachabilityRequest {
    pub topology: Topology,
    pub probe: Probe,
}

// ---- response ---------------------------------------------------------------

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Hop {
    pub component: String,
    #[serde(rename = "type")]
    pub component_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub direction: Option<String>,
    pub result: String,
    pub detail: String,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct BlockedBy {
    pub component: String,
    #[serde(rename = "type")]
    pub component_type: String,
    pub detail: String,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct ReachabilityResult {
    pub reachable: bool,
    pub hops: Vec<Hop>,
    pub blocked_by: Option<BlockedBy>,
    pub explanation: String,
    pub simulated_latency_ms: f64,
}

// ---- protocol / port matching -------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Proto {
    All,
    Tcp,
    Udp,
    Icmp,
    Num(u8),
}

impl Proto {
    pub fn parse(s: Option<&str>) -> Result<Proto, String> {
        let s = s.map(|x| x.trim().to_ascii_lowercase());
        Ok(match s.as_deref() {
            None | Some("") | Some("-1") | Some("all") => Proto::All,
            Some("tcp") | Some("6") => Proto::Tcp,
            Some("udp") | Some("17") => Proto::Udp,
            Some("icmp") | Some("1") => Proto::Icmp,
            Some(other) => Proto::Num(
                other
                    .parse::<u8>()
                    .map_err(|_| format!("unknown protocol {other:?}"))?,
            ),
        })
    }

    fn has_ports(self) -> bool {
        matches!(self, Proto::Tcp | Proto::Udp)
    }

    fn name(self) -> String {
        match self {
            Proto::All => "all".into(),
            Proto::Tcp => "tcp".into(),
            Proto::Udp => "udp".into(),
            Proto::Icmp => "icmp".into(),
            Proto::Num(n) => format!("proto-{n}"),
        }
    }
}

#[derive(Debug, Clone, Copy)]
enum PortSpec {
    Port(Option<u16>),
    /// Return traffic: the rule must cover the whole ephemeral range 1024-65535.
    Ephemeral,
}

fn rule_proto(p: Option<&str>) -> Proto {
    // Unparsable rule protocols never match anything.
    Proto::parse(p).unwrap_or(Proto::Num(255))
}

fn proto_matches(rule: Proto, probe: Proto) -> bool {
    rule == Proto::All || rule == probe
}

fn port_matches(
    rule: Proto,
    from: Option<i64>,
    to: Option<i64>,
    probe: Proto,
    spec: PortSpec,
) -> bool {
    if rule == Proto::All || !probe.has_ports() {
        return true;
    }
    let lo = from.unwrap_or(0);
    let hi = to.unwrap_or(65535);
    if lo < 0 || hi < 0 {
        return true;
    }
    match spec {
        PortSpec::Port(Some(p)) => lo <= i64::from(p) && i64::from(p) <= hi,
        PortSpec::Port(None) => false,
        PortSpec::Ephemeral => lo <= EPHEMERAL_FROM && hi >= EPHEMERAL_TO,
    }
}

fn rule_desc(rule: Proto, from: Option<i64>, to: Option<i64>) -> String {
    match rule {
        Proto::Tcp | Proto::Udp => {
            let lo = from.unwrap_or(0);
            let hi = to.unwrap_or(65535);
            if lo == hi {
                format!("{} {lo}", rule.name())
            } else {
                format!("{} {lo}-{hi}", rule.name())
            }
        }
        other => other.name(),
    }
}

fn cidr_matches(c: Option<&str>, ip: u32) -> bool {
    match c {
        None => false,
        Some(c) => Cidr::parse(c).is_ok_and(|c| c.contains_ip(ip)),
    }
}

// ---- index --------------------------------------------------------------------

struct Rt {
    id: String,
    routes: Vec<(Cidr, String)>,
}

impl Rt {
    fn lpm(&self, ip: u32) -> Option<&(Cidr, String)> {
        self.routes
            .iter()
            .filter(|(c, _)| c.contains_ip(ip))
            .max_by_key(|(c, _)| c.prefix)
    }
}

struct Index<'a> {
    vpcs: HashMap<&'a str, Cidr>,
    subnets: HashMap<&'a str, &'a Subnet>,
    rts: HashMap<&'a str, &'a RouteTable>,
    main_rt: HashMap<&'a str, &'a RouteTable>,
    igws: HashMap<&'a str, &'a InternetGateway>,
    nats: HashMap<&'a str, &'a NatGateway>,
    sgs: HashMap<&'a str, &'a SecurityGroup>,
    nacls: HashMap<&'a str, &'a NetworkAcl>,
    eps: HashMap<&'a str, &'a Endpoint>,
    ep_by_ip: HashMap<u32, &'a Endpoint>,
}

enum Node<'a> {
    Internet(u32),
    Ep(&'a Endpoint),
}

impl<'a> Index<'a> {
    fn build(t: &'a Topology) -> Result<Self, String> {
        let mut vpcs = HashMap::new();
        for v in &t.vpcs {
            let c = Cidr::parse(&v.cidr).map_err(|e| format!("vpc {}: {e}", v.id))?;
            vpcs.insert(v.id.as_str(), c);
        }
        let mut subnets = HashMap::new();
        for s in &t.subnets {
            Cidr::parse(&s.cidr).map_err(|e| format!("subnet {}: {e}", s.id))?;
            if !vpcs.contains_key(s.vpc_id.as_str()) {
                return Err(format!(
                    "subnet {} references unknown vpc {}",
                    s.id, s.vpc_id
                ));
            }
            subnets.insert(s.id.as_str(), s);
        }
        let rts: HashMap<_, _> = t.route_tables.iter().map(|r| (r.id.as_str(), r)).collect();
        let main_rt = t
            .route_tables
            .iter()
            .filter(|r| r.main)
            .map(|r| (r.vpc_id.as_str(), r))
            .collect();
        let igws = t
            .internet_gateways
            .iter()
            .map(|g| (g.id.as_str(), g))
            .collect();
        let nats: HashMap<_, _> = t.nat_gateways.iter().map(|n| (n.id.as_str(), n)).collect();
        let sgs: HashMap<_, _> = t
            .security_groups
            .iter()
            .map(|g| (g.id.as_str(), g))
            .collect();
        let nacls: HashMap<_, _> = t.network_acls.iter().map(|a| (a.id.as_str(), a)).collect();
        for s in &t.subnets {
            if let Some(r) = s.route_table_id.as_deref() {
                if !rts.contains_key(r) {
                    return Err(format!(
                        "subnet {} references unknown route table {r}",
                        s.id
                    ));
                }
            }
            if let Some(a) = s.network_acl_id.as_deref() {
                if !nacls.contains_key(a) {
                    return Err(format!(
                        "subnet {} references unknown network ACL {a}",
                        s.id
                    ));
                }
            }
        }
        for n in &t.nat_gateways {
            if !subnets.contains_key(n.subnet_id.as_str()) {
                return Err(format!(
                    "NAT gateway {} references unknown subnet {}",
                    n.id, n.subnet_id
                ));
            }
        }
        let mut eps = HashMap::new();
        let mut ep_by_ip = HashMap::new();
        for e in &t.endpoints {
            if !subnets.contains_key(e.subnet_id.as_str()) {
                return Err(format!(
                    "endpoint {} references unknown subnet {}",
                    e.id, e.subnet_id
                ));
            }
            for g in &e.security_group_ids {
                if !sgs.contains_key(g.as_str()) {
                    return Err(format!(
                        "endpoint {} references unknown security group {g}",
                        e.id
                    ));
                }
            }
            let ip =
                parse_ipv4(&e.private_ip).map_err(|err| format!("endpoint {}: {err}", e.id))?;
            ep_by_ip.insert(ip, e);
            if let Some(p) = e.public_ip.as_deref() {
                let pip = parse_ipv4(p).map_err(|err| format!("endpoint {}: {err}", e.id))?;
                ep_by_ip.insert(pip, e);
            }
            eps.insert(e.id.as_str(), e);
        }
        Ok(Index {
            vpcs,
            subnets,
            rts,
            main_rt,
            igws,
            nats,
            sgs,
            nacls,
            eps,
            ep_by_ip,
        })
    }

    fn resolve(&self, s: &str) -> Result<Node<'a>, String> {
        let t = s.trim();
        if t.eq_ignore_ascii_case("internet") {
            return Ok(Node::Internet(parse_ipv4(INTERNET_IP)?));
        }
        if let Some(ep) = self.eps.get(t) {
            return Ok(Node::Ep(ep));
        }
        if let Ok(ip) = parse_ipv4(t) {
            if let Some(ep) = self.ep_by_ip.get(&ip) {
                return Ok(Node::Ep(ep));
            }
            if self.vpcs.values().any(|c| c.contains_ip(ip)) {
                return Err(format!("no endpoint with IP {t} in the topology"));
            }
            return Ok(Node::Internet(ip));
        }
        Err(format!(
            "unknown endpoint {t:?}: expected an endpoint id, \"internet\" or an IPv4 address"
        ))
    }

    fn subnet_of(&self, ep: &Endpoint) -> &'a Subnet {
        // Validated in build().
        self.subnets[ep.subnet_id.as_str()]
    }

    fn route_table_for(&self, s: &Subnet) -> Rt {
        let vpc_cidr = self.vpcs.get(s.vpc_id.as_str()).copied();
        let table = s
            .route_table_id
            .as_deref()
            .and_then(|id| self.rts.get(id).copied())
            .or_else(|| self.main_rt.get(s.vpc_id.as_str()).copied());
        let (id, mut routes) = match table {
            Some(t) => (
                t.id.clone(),
                t.routes
                    .iter()
                    .filter_map(|r| {
                        Cidr::parse(&r.destination)
                            .ok()
                            .map(|c| (c, r.target.clone()))
                    })
                    .collect::<Vec<_>>(),
            ),
            None => (format!("{}-main", s.vpc_id), Vec::new()),
        };
        if !routes.iter().any(|(_, t)| t == "local") {
            if let Some(c) = vpc_cidr {
                routes.push((c, "local".to_string()));
            }
        }
        Rt { id, routes }
    }

    fn is_igw(&self, target: &str) -> bool {
        self.igws.contains_key(target) || target.starts_with("igw-")
    }

    fn is_nat(&self, target: &str) -> bool {
        self.nats.contains_key(target) || target.starts_with("nat-")
    }
}

// ---- evaluation -----------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum PathKind {
    SameSubnet,
    SameVpc,
    ViaIgw,
    ViaNat,
}

impl PathKind {
    fn base_latency_ms(self) -> f64 {
        match self {
            PathKind::SameSubnet => 0.2,
            PathKind::SameVpc => 0.5,
            PathKind::ViaIgw => 15.0,
            PathKind::ViaNat => 18.0,
        }
    }

    fn describe(self) -> &'static str {
        match self {
            PathKind::SameSubnet => "within the same subnet",
            PathKind::SameVpc => "within the VPC via the local route",
            PathKind::ViaIgw => "via an internet gateway",
            PathKind::ViaNat => "via a NAT gateway",
        }
    }
}

struct Block {
    blocked: BlockedBy,
    explanation: String,
}

type Step = Result<(), Block>;

struct Eval<'a, 'i> {
    idx: &'i Index<'a>,
    hops: Vec<Hop>,
    proto: Proto,
    port: Option<u16>,
}

fn ip_of(s: &str) -> u32 {
    parse_ipv4(s).unwrap_or(0)
}

impl Eval<'_, '_> {
    fn hop(
        &mut self,
        component: &str,
        ty: &str,
        direction: Option<&str>,
        allow: bool,
        detail: String,
    ) {
        self.hops.push(Hop {
            component: component.to_string(),
            component_type: ty.to_string(),
            direction: direction.map(str::to_string),
            result: if allow { "allow" } else { "deny" }.to_string(),
            detail,
        });
    }

    fn deny(
        &mut self,
        component: &str,
        ty: &str,
        direction: Option<&str>,
        detail: String,
        explanation: String,
    ) -> Block {
        self.hop(component, ty, direction, false, detail.clone());
        Block {
            blocked: BlockedBy {
                component: component.to_string(),
                component_type: ty.to_string(),
                detail,
            },
            explanation,
        }
    }

    fn probe_desc(&self) -> String {
        match (self.proto, self.port) {
            (Proto::Tcp | Proto::Udp, Some(p)) => format!("{}/{p}", self.proto.name()),
            (Proto::All, _) => "all traffic".to_string(),
            (p, _) => p.name(),
        }
    }

    fn up(&mut self, ep: &Endpoint, role: &str) -> Step {
        let st = ep.state.as_deref().unwrap_or("running");
        if matches!(st, "running" | "available" | "in-use" | "active") {
            Ok(())
        } else {
            Err(self.deny(
                &ep.id,
                "endpoint",
                None,
                format!("{role} is {st}"),
                format!("The {role} {} is {st}; it must be running.", ep.id),
            ))
        }
    }

    fn sg(&mut self, ep: &Endpoint, ingress: bool, peer_ip: u32, peer: Option<&Endpoint>) -> Step {
        let dir = if ingress { "ingress" } else { "egress" };
        for sg_id in &ep.security_group_ids {
            let Some(sg) = self.idx.sgs.get(sg_id.as_str()).copied() else {
                continue;
            };
            let rules = if ingress { &sg.ingress } else { &sg.egress };
            for r in rules {
                let rp = rule_proto(r.protocol.as_deref());
                if !proto_matches(rp, self.proto)
                    || !port_matches(
                        rp,
                        r.from_port,
                        r.to_port,
                        self.proto,
                        PortSpec::Port(self.port),
                    )
                {
                    continue;
                }
                let by_cidr = cidr_matches(r.cidr.as_deref(), peer_ip);
                let by_sg = r.source_sg.as_deref().is_some_and(|g| {
                    peer.is_some_and(|p| p.security_group_ids.iter().any(|x| x == g))
                });
                if by_cidr || by_sg {
                    let src = if by_cidr {
                        r.cidr.clone().unwrap_or_default()
                    } else {
                        format!("sg {}", r.source_sg.as_deref().unwrap_or_default())
                    };
                    let detail = format!("rule {src} {}", rule_desc(rp, r.from_port, r.to_port));
                    self.hop(&sg.id, "security_group", Some(dir), true, detail);
                    return Ok(());
                }
            }
        }
        let comp = ep
            .security_group_ids
            .first()
            .cloned()
            .unwrap_or_else(|| ep.id.clone());
        let peer_s = ip_to_string(peer_ip);
        let fromto = if ingress { "from" } else { "to" };
        let pd = self.probe_desc();
        let groups = if ep.security_group_ids.is_empty() {
            "(none attached)".to_string()
        } else {
            ep.security_group_ids.join(", ")
        };
        Err(self.deny(
            &comp,
            "security_group",
            Some(dir),
            format!("no {dir} rule allows {pd} {fromto} {peer_s}"),
            format!(
                "Traffic blocked by security group {groups} {dir}: no rule allows {pd} {fromto} {peer_s}."
            ),
        ))
    }

    fn nacl(&mut self, subnet: &Subnet, egress: bool, peer_ip: u32, spec: PortSpec) -> Step {
        let dir = if egress { "outbound" } else { "inbound" };
        let is_return = matches!(spec, PortSpec::Ephemeral);
        let direction = if is_return {
            format!("{dir} (return traffic)")
        } else {
            dir.to_string()
        };
        let Some(acl) = subnet
            .network_acl_id
            .as_deref()
            .and_then(|id| self.idx.nacls.get(id).copied())
        else {
            self.hop(
                &subnet.id,
                "network_acl",
                Some(&direction),
                true,
                "no network ACL associated (default ACL allows all)".to_string(),
            );
            return Ok(());
        };
        let mut entries: Vec<&NaclEntry> =
            acl.entries.iter().filter(|e| e.egress == egress).collect();
        entries.sort_by_key(|e| e.rule_number);
        let what = if is_return {
            format!(
                "Return traffic ({} ephemeral ports {EPHEMERAL_FROM}-{EPHEMERAL_TO})",
                self.proto.name()
            )
        } else {
            "Traffic".to_string()
        };
        for e in entries {
            let rp = rule_proto(e.protocol.as_deref());
            if !proto_matches(rp, self.proto) {
                continue;
            }
            let cidr_ok = match e.cidr.as_deref() {
                None => true,
                Some(c) => cidr_matches(Some(c), peer_ip),
            };
            if !cidr_ok || !port_matches(rp, e.from_port, e.to_port, self.proto, spec) {
                continue;
            }
            let allow = e.action.eq_ignore_ascii_case("allow");
            let detail = format!(
                "{dir} rule {} {}",
                e.rule_number,
                if allow { "allow" } else { "deny" }
            );
            if allow {
                self.hop(&acl.id, "network_acl", Some(&direction), true, detail);
                return Ok(());
            }
            return Err(self.deny(
                &acl.id,
                "network_acl",
                Some(&direction),
                detail,
                format!(
                    "{what} blocked by network ACL {} {dir} (rule {} deny).",
                    acl.id, e.rule_number
                ),
            ));
        }
        Err(self.deny(
            &acl.id,
            "network_acl",
            Some(&direction),
            format!("{dir} rule * deny"),
            format!(
                "{what} blocked by network ACL {} {dir} (implicit deny).",
                acl.id
            ),
        ))
    }

    fn route_local(&mut self, subnet: &Subnet, dst_ip: u32) -> Step {
        let rt = self.idx.route_table_for(subnet);
        match rt.lpm(dst_ip) {
            Some((c, t)) if t == "local" => {
                self.hop(&rt.id, "route_table", None, true, format!("{c} -> local"));
                Ok(())
            }
            Some((c, t)) => {
                let (c, t) = (*c, t.clone());
                Err(self.deny(
                    &rt.id,
                    "route_table",
                    None,
                    format!("{c} -> {t}"),
                    format!(
                        "Route table {} sends {} to {t}; routing intra-VPC traffic through {t} is not simulated.",
                        rt.id,
                        ip_to_string(dst_ip)
                    ),
                ))
            }
            None => Err(self.deny(
                &rt.id,
                "route_table",
                None,
                format!("no route to {}", ip_to_string(dst_ip)),
                format!(
                    "Route table {} has no route to {}.",
                    rt.id,
                    ip_to_string(dst_ip)
                ),
            )),
        }
    }

    fn igw_attached(&self, igw: &str, vpc: &str) -> bool {
        self.idx
            .igws
            .get(igw)
            .is_some_and(|g| g.vpc_id.as_deref() == Some(vpc))
    }

    /// Route from `subnet` towards an internet address. Returns the public source
    /// address the traffic leaves with and the path kind.
    fn egress_internet(
        &mut self,
        src: &Endpoint,
        subnet: &Subnet,
        dst_ip: u32,
    ) -> Result<(u32, PathKind), Block> {
        let rt = self.idx.route_table_for(subnet);
        let dst_s = ip_to_string(dst_ip);
        let Some((c, t)) = rt.lpm(dst_ip).cloned() else {
            return Err(self.deny(
                &rt.id,
                "route_table",
                None,
                format!("no route to {dst_s}"),
                format!("Route table {} has no route to {dst_s}.", rt.id),
            ));
        };
        if self.idx.is_igw(&t) {
            if !self.igw_attached(&t, &subnet.vpc_id) {
                return Err(self.deny(
                    &t,
                    "internet_gateway",
                    None,
                    format!("{t} is not attached to {}", subnet.vpc_id),
                    format!(
                        "Route table {} targets internet gateway {t}, which is not attached to {}.",
                        rt.id, subnet.vpc_id
                    ),
                ));
            }
            self.hop(&rt.id, "route_table", None, true, format!("{c} -> {t}"));
            let Some(pub_ip) = src.public_ip.as_deref().map(ip_of) else {
                return Err(self.deny(
                    &src.id,
                    "endpoint",
                    None,
                    "source has no public IP".to_string(),
                    format!(
                        "Source {} has no public IP address; traffic through internet gateway {t} requires one (private subnets should route 0.0.0.0/0 to a NAT gateway).",
                        src.id
                    ),
                ));
            };
            self.hop(
                &t,
                "internet_gateway",
                Some("outbound"),
                true,
                format!("egress to internet as {}", ip_to_string(pub_ip)),
            );
            return Ok((pub_ip, PathKind::ViaIgw));
        }
        if self.idx.is_nat(&t) {
            let Some(nat) = self.idx.nats.get(t.as_str()).copied() else {
                return Err(self.deny(
                    &t,
                    "nat_gateway",
                    None,
                    format!("{t} not found"),
                    format!(
                        "Route table {} targets NAT gateway {t}, which does not exist.",
                        rt.id
                    ),
                ));
            };
            self.hop(&rt.id, "route_table", None, true, format!("{c} -> {t}"));
            if nat.state != "available" {
                return Err(self.deny(
                    &nat.id,
                    "nat_gateway",
                    Some("outbound"),
                    format!("NAT gateway is {}", nat.state),
                    format!(
                        "NAT gateway {} is {}; it must be available to forward traffic.",
                        nat.id, nat.state
                    ),
                ));
            }
            let nat_subnet = self.idx.subnets[nat.subnet_id.as_str()];
            let nat_rt = self.idx.route_table_for(nat_subnet);
            let via = nat_rt.lpm(dst_ip).cloned();
            let igw_ok = via.as_ref().is_some_and(|(_, t2)| {
                self.idx.is_igw(t2) && self.igw_attached(t2, &nat_subnet.vpc_id)
            });
            if !igw_ok {
                return Err(self.deny(
                    &nat.id,
                    "nat_gateway",
                    Some("outbound"),
                    format!("NAT subnet {} has no route to an internet gateway", nat_subnet.id),
                    format!(
                        "NAT gateway {} sits in subnet {} whose route table {} has no route to an attached internet gateway; the NAT must live in a public subnet.",
                        nat.id, nat_subnet.id, nat_rt.id
                    ),
                ));
            }
            let Some(nat_ip) = nat.public_ip.as_deref().map(ip_of) else {
                return Err(self.deny(
                    &nat.id,
                    "nat_gateway",
                    Some("outbound"),
                    "NAT gateway has no public IP".to_string(),
                    format!("NAT gateway {} has no public (Elastic) IP.", nat.id),
                ));
            };
            self.hop(
                &nat.id,
                "nat_gateway",
                Some("outbound"),
                true,
                format!("source NAT to {}", ip_to_string(nat_ip)),
            );
            let (c2, igw) = via.unwrap_or((c, t.clone()));
            self.hop(
                &nat_rt.id,
                "route_table",
                None,
                true,
                format!("{c2} -> {igw}"),
            );
            self.hop(
                &igw,
                "internet_gateway",
                Some("outbound"),
                true,
                format!("egress to internet as {}", ip_to_string(nat_ip)),
            );
            return Ok((nat_ip, PathKind::ViaNat));
        }
        let explanation = if t == "local" {
            format!("{dst_s} resolves to the local route but no endpoint in the topology has that address.")
        } else {
            format!(
                "Route table {} sends {dst_s} to {t}; only local, internet gateway and NAT gateway targets are simulated.",
                rt.id
            )
        };
        Err(self.deny(
            &rt.id,
            "route_table",
            None,
            format!("{c} -> {t}"),
            explanation,
        ))
    }

    /// Inbound from the internet to `dst` (requires a public IP and an IGW route).
    fn ingress_internet(&mut self, dst: &Endpoint, subnet: &Subnet, src_ip: u32) -> Step {
        if dst.public_ip.is_none() {
            return Err(self.deny(
                &dst.id,
                "endpoint",
                Some("inbound"),
                "destination has no public IP".to_string(),
                format!(
                    "Destination {} has no public IP address, so it is not reachable from the internet.",
                    dst.id
                ),
            ));
        }
        let rt = self.idx.route_table_for(subnet);
        match rt.lpm(src_ip).cloned() {
            Some((c, t)) if self.idx.is_igw(&t) && self.igw_attached(&t, &subnet.vpc_id) => {
                self.hop(&rt.id, "route_table", None, true, format!("{c} -> {t}"));
                self.hop(
                    &t,
                    "internet_gateway",
                    Some("inbound"),
                    true,
                    format!("inbound from internet to {}", dst.public_ip.as_deref().unwrap_or_default()),
                );
                Ok(())
            }
            Some((c, t)) if self.idx.is_nat(&t) => Err(self.deny(
                &rt.id,
                "route_table",
                None,
                format!("{c} -> {t} (NAT is outbound-only)"),
                format!(
                    "Subnet {} routes internet traffic through NAT gateway {t}; NAT only permits connections initiated from inside, so {} is not reachable from the internet.",
                    subnet.id, dst.id
                ),
            )),
            _ => Err(self.deny(
                &rt.id,
                "route_table",
                None,
                "no route to an internet gateway".to_string(),
                format!(
                    "Subnet {} (route table {}) has no route to an attached internet gateway; it is a private subnet, so {} is not reachable from the internet.",
                    subnet.id, rt.id, dst.id
                ),
            )),
        }
    }

    fn same_vpc(&mut self, s: &Endpoint, d: &Endpoint) -> Step {
        let ss = self.idx.subnet_of(s);
        let ds = self.idx.subnet_of(d);
        let (s_ip, d_ip) = (ip_of(&s.private_ip), ip_of(&d.private_ip));
        let cross_subnet = ss.id != ds.id;
        let port = PortSpec::Port(self.port);
        self.up(s, "source")?;
        self.up(d, "destination")?;
        self.sg(s, false, d_ip, Some(d))?;
        if cross_subnet {
            self.nacl(ss, true, d_ip, port)?;
        }
        self.route_local(ss, d_ip)?;
        if cross_subnet {
            self.nacl(ds, false, s_ip, port)?;
        }
        self.sg(d, true, s_ip, Some(s))?;
        if cross_subnet {
            self.nacl(ds, true, s_ip, PortSpec::Ephemeral)?;
            self.nacl(ss, false, d_ip, PortSpec::Ephemeral)?;
        }
        Ok(())
    }

    fn probe_to_internet(&mut self, s: &Endpoint, ip: u32, path: &mut PathKind) -> Step {
        let ss = self.idx.subnet_of(s);
        self.up(s, "source")?;
        self.sg(s, false, ip, None)?;
        self.nacl(ss, true, ip, PortSpec::Port(self.port))?;
        let (_, p) = self.egress_internet(s, ss, ip)?;
        *path = p;
        self.nacl(ss, false, ip, PortSpec::Ephemeral)?;
        Ok(())
    }

    fn probe_from_internet(&mut self, ip: u32, d: &Endpoint) -> Step {
        let ds = self.idx.subnet_of(d);
        self.up(d, "destination")?;
        self.ingress_internet(d, ds, ip)?;
        self.nacl(ds, false, ip, PortSpec::Port(self.port))?;
        self.sg(d, true, ip, None)?;
        self.nacl(ds, true, ip, PortSpec::Ephemeral)?;
        Ok(())
    }

    fn cross_vpc(&mut self, s: &Endpoint, d: &Endpoint, path: &mut PathKind) -> Step {
        let ss = self.idx.subnet_of(s);
        let ds = self.idx.subnet_of(d);
        self.up(s, "source")?;
        self.up(d, "destination")?;
        let Some(d_pub) = d.public_ip.as_deref().map(ip_of) else {
            return Err(self.deny(
                &d.id,
                "endpoint",
                None,
                "destination is in another VPC and has no public IP".to_string(),
                format!(
                    "{} and {} are in different VPCs and {} has no public IP; VPC peering and transit gateways are not simulated.",
                    s.id, d.id, d.id
                ),
            ));
        };
        self.sg(s, false, d_pub, None)?;
        self.nacl(ss, true, d_pub, PortSpec::Port(self.port))?;
        let (egress_ip, p) = self.egress_internet(s, ss, d_pub)?;
        *path = p;
        self.ingress_internet(d, ds, egress_ip)?;
        self.nacl(ds, false, egress_ip, PortSpec::Port(self.port))?;
        self.sg(d, true, egress_ip, None)?;
        self.nacl(ds, true, egress_ip, PortSpec::Ephemeral)?;
        self.nacl(ss, false, d_pub, PortSpec::Ephemeral)?;
        Ok(())
    }
}

/// Evaluate a reachability probe. `Err` means the request itself is invalid (→ 422).
pub fn evaluate(req: &ReachabilityRequest, seed: u64) -> Result<ReachabilityResult, String> {
    let idx = Index::build(&req.topology)?;
    let proto = Proto::parse(req.probe.protocol.as_deref())?;
    let port = match req.probe.port {
        None => None,
        Some(p) if (0..=65535).contains(&p) => Some(p as u16),
        Some(p) => return Err(format!("port must be 0-65535, got {p}")),
    };
    if proto.has_ports() && port.is_none() {
        return Err(format!(
            "probe.port is required for protocol {}",
            proto.name()
        ));
    }
    let src = idx.resolve(&req.probe.source)?;
    let dst = idx.resolve(&req.probe.destination)?;

    let mut ev = Eval {
        idx: &idx,
        hops: Vec::new(),
        proto,
        port,
    };
    let mut path = PathKind::ViaIgw;
    let step = match (&src, &dst) {
        (Node::Internet(_), Node::Internet(_)) => {
            return Err("source and destination cannot both be outside the topology".to_string())
        }
        (Node::Ep(s), Node::Ep(d)) => {
            let ss = idx.subnet_of(s);
            let ds = idx.subnet_of(d);
            if ss.vpc_id == ds.vpc_id {
                path = if ss.id == ds.id {
                    PathKind::SameSubnet
                } else {
                    PathKind::SameVpc
                };
                ev.same_vpc(s, d)
            } else {
                ev.cross_vpc(s, d, &mut path)
            }
        }
        (Node::Ep(s), Node::Internet(ip)) => ev.probe_to_internet(s, *ip, &mut path),
        (Node::Internet(ip), Node::Ep(d)) => ev.probe_from_internet(*ip, d),
    };

    let mut rng = SeedMix::new(seed)
        .str("latency")
        .str(&req.probe.source)
        .str(&req.probe.destination)
        .str(&proto.name())
        .u64(u64::from(port.unwrap_or(0)))
        .rng();
    let jitter: f64 = rng.gen_range(0.0..=0.10);
    let latency = round_to(path.base_latency_ms() * (1.0 + jitter), 3);

    let probe_desc = ev.probe_desc();
    let hops = std::mem::take(&mut ev.hops);
    Ok(match step {
        Ok(()) => ReachabilityResult {
            reachable: true,
            hops,
            blocked_by: None,
            explanation: format!(
                "{} can reach {} on {probe_desc} {}.",
                req.probe.source.trim(),
                req.probe.destination.trim(),
                path.describe()
            ),
            simulated_latency_ms: latency,
        },
        Err(b) => ReachabilityResult {
            reachable: false,
            hops,
            blocked_by: Some(b.blocked),
            explanation: b.explanation,
            simulated_latency_ms: latency,
        },
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn base() -> serde_json::Value {
        serde_json::json!({
          "vpcs":[{"id":"vpc-1","cidr":"10.0.0.0/16"}],
          "subnets":[{"id":"subnet-1","vpc_id":"vpc-1","cidr":"10.0.1.0/24","route_table_id":"rtb-1","network_acl_id":"acl-1"}],
          "route_tables":[{"id":"rtb-1","vpc_id":"vpc-1","routes":[{"destination":"10.0.0.0/16","target":"local"},{"destination":"0.0.0.0/0","target":"igw-1"}]}],
          "internet_gateways":[{"id":"igw-1","vpc_id":"vpc-1"}],
          "nat_gateways":[],
          "security_groups":[{"id":"sg-1","vpc_id":"vpc-1",
             "ingress":[{"protocol":"tcp","from_port":22,"to_port":22,"cidr":"0.0.0.0/0","source_sg":null}],
             "egress":[{"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","source_sg":null}]}],
          "network_acls":[{"id":"acl-1","vpc_id":"vpc-1","entries":[
             {"rule_number":100,"egress":false,"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","action":"allow"},
             {"rule_number":100,"egress":true,"protocol":"-1","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0","action":"allow"}]}],
          "endpoints":[{"id":"i-1","subnet_id":"subnet-1","private_ip":"10.0.1.10","public_ip":"54.0.0.1","security_group_ids":["sg-1"],"state":"running"}]
        })
    }

    fn run(
        topo: serde_json::Value,
        src: &str,
        dst: &str,
        proto: &str,
        port: Option<i64>,
    ) -> ReachabilityResult {
        let req = ReachabilityRequest {
            topology: serde_json::from_value(topo).unwrap(),
            probe: Probe {
                source: src.into(),
                destination: dst.into(),
                protocol: Some(proto.into()),
                port,
            },
        };
        evaluate(&req, 42).unwrap()
    }

    #[test]
    fn ssh_from_internet_allowed() {
        let r = run(base(), "internet", "i-1", "tcp", Some(22));
        assert!(r.reachable, "{r:?}");
        assert!(r.blocked_by.is_none());
        assert!(r.simulated_latency_ms >= 15.0 && r.simulated_latency_ms <= 16.5);
    }

    #[test]
    fn port_not_in_sg_is_blocked() {
        let r = run(base(), "internet", "i-1", "tcp", Some(80));
        assert!(!r.reachable);
        assert_eq!(r.blocked_by.unwrap().component_type, "security_group");
    }

    #[test]
    fn protocol_parsing() {
        assert_eq!(Proto::parse(Some("-1")).unwrap(), Proto::All);
        assert_eq!(Proto::parse(Some("TCP")).unwrap(), Proto::Tcp);
        assert_eq!(Proto::parse(Some("17")).unwrap(), Proto::Udp);
        assert!(Proto::parse(Some("bogus")).is_err());
    }

    #[test]
    fn ephemeral_rule_must_cover_range() {
        assert!(port_matches(
            Proto::Tcp,
            Some(1024),
            Some(65535),
            Proto::Tcp,
            PortSpec::Ephemeral
        ));
        assert!(!port_matches(
            Proto::Tcp,
            Some(22),
            Some(22),
            Proto::Tcp,
            PortSpec::Ephemeral
        ));
        assert!(port_matches(
            Proto::All,
            None,
            None,
            Proto::Tcp,
            PortSpec::Ephemeral
        ));
        assert!(port_matches(
            Proto::Icmp,
            None,
            None,
            Proto::Icmp,
            PortSpec::Port(None)
        ));
    }

    #[test]
    fn invalid_requests() {
        let req = ReachabilityRequest {
            topology: serde_json::from_value(base()).unwrap(),
            probe: Probe {
                source: "internet".into(),
                destination: "i-404".into(),
                protocol: Some("tcp".into()),
                port: Some(22),
            },
        };
        assert!(evaluate(&req, 1).is_err());
        let mut req2 = req.clone();
        req2.probe.destination = "i-1".into();
        req2.probe.port = None;
        assert!(evaluate(&req2, 1).is_err());
    }
}
