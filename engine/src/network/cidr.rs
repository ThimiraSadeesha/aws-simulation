//! Hand-rolled IPv4 CIDR math.

use std::fmt;
use std::net::Ipv4Addr;

use serde::{Deserialize, Serialize};

pub const MAX_SUBNETS_LISTED: usize = 256;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct Cidr {
    /// Network address (host bits zero).
    pub addr: u32,
    pub prefix: u8,
}

pub fn parse_ipv4(s: &str) -> Result<u32, String> {
    s.trim()
        .parse::<Ipv4Addr>()
        .map(u32::from)
        .map_err(|_| format!("invalid IPv4 address {s:?}"))
}

pub fn ip_to_string(ip: u32) -> String {
    Ipv4Addr::from(ip).to_string()
}

pub fn mask(prefix: u8) -> u32 {
    if prefix == 0 {
        0
    } else {
        u32::MAX << (32 - u32::from(prefix))
    }
}

impl Cidr {
    pub fn new(ip: u32, prefix: u8) -> Cidr {
        Cidr {
            addr: ip & mask(prefix),
            prefix,
        }
    }

    /// Parse `a.b.c.d/p`. Host bits are masked off. A bare address is treated as `/32`.
    pub fn parse(s: &str) -> Result<Cidr, String> {
        Self::parse_inner(s).map(|(c, _)| c)
    }

    /// Like `parse` but rejects host bits being set (e.g. `10.0.0.5/16`).
    pub fn parse_strict(s: &str) -> Result<Cidr, String> {
        let s = s.trim();
        if !s.contains('/') {
            return Err(format!("invalid CIDR {s:?}: missing /prefix"));
        }
        let (c, raw) = Self::parse_inner(s)?;
        if raw != c.addr {
            return Err(format!(
                "invalid CIDR {s:?}: host bits are set (did you mean {c}?)"
            ));
        }
        Ok(c)
    }

    fn parse_inner(s: &str) -> Result<(Cidr, u32), String> {
        let s = s.trim();
        let (ip_s, prefix) = match s.split_once('/') {
            Some((ip, p)) => {
                let p: u8 = p
                    .parse()
                    .map_err(|_| format!("invalid CIDR {s:?}: bad prefix length"))?;
                (ip, p)
            }
            None => (s, 32),
        };
        if prefix > 32 {
            return Err(format!("invalid CIDR {s:?}: prefix must be 0-32"));
        }
        let ip = parse_ipv4(ip_s).map_err(|_| format!("invalid CIDR {s:?}: bad address"))?;
        Ok((Cidr::new(ip, prefix), ip))
    }

    pub fn mask(&self) -> u32 {
        mask(self.prefix)
    }

    pub fn broadcast(&self) -> u32 {
        self.addr | !self.mask()
    }

    pub fn size(&self) -> u64 {
        1u64 << (32 - u32::from(self.prefix))
    }

    pub fn contains_ip(&self, ip: u32) -> bool {
        ip & self.mask() == self.addr
    }

    /// `other` is entirely inside `self`.
    pub fn contains(&self, other: &Cidr) -> bool {
        other.prefix >= self.prefix && self.contains_ip(other.addr)
    }

    pub fn overlaps(&self, other: &Cidr) -> bool {
        self.contains(other) || other.contains(self)
    }

    pub fn first_host(&self) -> u32 {
        if self.prefix >= 31 {
            self.addr
        } else {
            self.addr + 1
        }
    }

    pub fn last_host(&self) -> u32 {
        if self.prefix >= 31 {
            self.broadcast()
        } else {
            self.broadcast() - 1
        }
    }

    /// Split into `/new_prefix` subnets, listing at most `max`. Returns (subnets, total count).
    pub fn subnets(&self, new_prefix: u8, max: usize) -> Result<(Vec<Cidr>, u64), String> {
        if new_prefix > 32 || new_prefix < self.prefix {
            return Err(format!(
                "new_prefix must be between {} and 32, got {new_prefix}",
                self.prefix
            ));
        }
        let total = 1u64 << u32::from(new_prefix - self.prefix);
        let step = 1u64 << (32 - u32::from(new_prefix));
        let n = total.min(max as u64);
        let out = (0..n)
            .map(|i| Cidr {
                addr: (u64::from(self.addr) + i * step) as u32,
                prefix: new_prefix,
            })
            .collect();
        Ok((out, total))
    }
}

impl fmt::Display for Cidr {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}/{}", Ipv4Addr::from(self.addr), self.prefix)
    }
}

// ---- /v1/network/cidr -------------------------------------------------------

#[derive(Debug, Clone, Deserialize)]
pub struct CidrRequest {
    pub op: String,
    pub cidr: String,
    #[serde(default)]
    pub other: Option<String>,
    #[serde(default)]
    pub new_prefix: Option<u8>,
}

#[derive(Debug, Clone, Serialize, Default, PartialEq)]
pub struct CidrResponse {
    pub valid: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub network: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub prefix: Option<u8>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub first_host: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_host: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub size: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub contains: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub overlaps: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub subnets: Option<Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub total_subnets: Option<u64>,
}

fn describe(c: &Cidr) -> CidrResponse {
    CidrResponse {
        valid: true,
        network: Some(ip_to_string(c.addr)),
        prefix: Some(c.prefix),
        first_host: Some(ip_to_string(c.first_host())),
        last_host: Some(ip_to_string(c.last_host())),
        size: Some(c.size()),
        ..Default::default()
    }
}

/// Execute a CIDR operation. `validate` reports problems in-band (`valid:false`);
/// the other operations return `Err` (→ 422) for unparsable input.
pub fn cidr_op(req: &CidrRequest) -> Result<CidrResponse, String> {
    match req.op.as_str() {
        "validate" => Ok(match Cidr::parse_strict(&req.cidr) {
            Ok(c) => describe(&c),
            Err(e) => CidrResponse {
                valid: false,
                error: Some(e),
                ..Default::default()
            },
        }),
        "contains" | "overlaps" => {
            let a = Cidr::parse(&req.cidr)?;
            let other = req
                .other
                .as_deref()
                .ok_or_else(|| format!("'other' is required for op '{}'", req.op))?;
            let b = Cidr::parse(other)?;
            let mut r = describe(&a);
            if req.op == "contains" {
                r.contains = Some(a.contains(&b));
            } else {
                r.overlaps = Some(a.overlaps(&b));
            }
            Ok(r)
        }
        "subnets" => {
            let a = Cidr::parse(&req.cidr)?;
            let np = req
                .new_prefix
                .ok_or_else(|| "'new_prefix' is required for op 'subnets'".to_string())?;
            let (subs, total) = a.subnets(np, MAX_SUBNETS_LISTED)?;
            let mut r = describe(&a);
            r.subnets = Some(subs.iter().map(Cidr::to_string).collect());
            r.total_subnets = Some(total);
            Ok(r)
        }
        other => Err(format!(
            "unknown op {other:?}; expected validate|contains|overlaps|subnets"
        )),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_and_describe() {
        let c = Cidr::parse("10.0.0.0/16").unwrap();
        assert_eq!(c.size(), 65536);
        assert_eq!(ip_to_string(c.first_host()), "10.0.0.1");
        assert_eq!(ip_to_string(c.last_host()), "10.0.255.254");
        assert_eq!(c.to_string(), "10.0.0.0/16");
        assert_eq!(
            Cidr::parse("10.0.0.5/16").unwrap().to_string(),
            "10.0.0.0/16"
        );
        assert!(Cidr::parse_strict("10.0.0.5/16").is_err());
        assert!(Cidr::parse("10.0.0.0/33").is_err());
        assert!(Cidr::parse("10.0.0/16").is_err());
        assert!(Cidr::parse("abc").is_err());
        assert_eq!(Cidr::parse("1.2.3.4").unwrap().prefix, 32);
        let z = Cidr::parse("0.0.0.0/0").unwrap();
        assert!(z.contains_ip(parse_ipv4("8.8.8.8").unwrap()));
        assert_eq!(z.size(), 1u64 << 32);
        let h = Cidr::parse("10.0.0.7/32").unwrap();
        assert_eq!(h.first_host(), h.last_host());
    }

    #[test]
    fn contains_overlaps() {
        let a = Cidr::parse("10.0.0.0/16").unwrap();
        let b = Cidr::parse("10.0.1.0/24").unwrap();
        let c = Cidr::parse("10.1.0.0/16").unwrap();
        assert!(a.contains(&b));
        assert!(!b.contains(&a));
        assert!(a.overlaps(&b) && b.overlaps(&a));
        assert!(!a.overlaps(&c));
    }

    #[test]
    fn subnetting() {
        let a = Cidr::parse("10.0.0.0/16").unwrap();
        let (s, total) = a.subnets(24, 256).unwrap();
        assert_eq!(total, 256);
        assert_eq!(s[0].to_string(), "10.0.0.0/24");
        assert_eq!(s[255].to_string(), "10.0.255.0/24");
        let (s, total) = a.subnets(28, 256).unwrap();
        assert_eq!(total, 4096);
        assert_eq!(s.len(), 256);
        assert!(a.subnets(8, 10).is_err());
    }

    #[test]
    fn ops() {
        let r = cidr_op(&CidrRequest {
            op: "validate".into(),
            cidr: "10.0.0.0/16".into(),
            other: None,
            new_prefix: None,
        })
        .unwrap();
        assert!(r.valid);
        assert_eq!(r.network.as_deref(), Some("10.0.0.0"));
        let r = cidr_op(&CidrRequest {
            op: "validate".into(),
            cidr: "300.0.0.0/8".into(),
            other: None,
            new_prefix: None,
        })
        .unwrap();
        assert!(!r.valid);
        let r = cidr_op(&CidrRequest {
            op: "overlaps".into(),
            cidr: "10.0.0.0/16".into(),
            other: Some("10.0.128.0/17".into()),
            new_prefix: None,
        })
        .unwrap();
        assert_eq!(r.overlaps, Some(true));
        assert!(cidr_op(&CidrRequest {
            op: "nope".into(),
            cidr: "10.0.0.0/16".into(),
            other: None,
            new_prefix: None,
        })
        .is_err());
    }
}
