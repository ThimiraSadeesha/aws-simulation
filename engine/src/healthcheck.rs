//! `cloudsim-engine --healthcheck`: a dependency-free probe for container HEALTHCHECKs
//! (the runtime image has no curl/wget). GETs `http://127.0.0.1:<port>/healthz`.

use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream};
use std::time::Duration;

/// Port to probe: the port of `ENGINE_ADDR` (default 7070).
pub fn probe_port() -> u16 {
    std::env::var("ENGINE_ADDR")
        .ok()
        .and_then(|a| a.trim().parse::<SocketAddr>().ok())
        .map(|a| a.port())
        .unwrap_or(7070)
}

/// Returns `true` when `/healthz` answers `200`.
pub fn check(port: u16, timeout: Duration) -> bool {
    let addr = SocketAddr::from(([127, 0, 0, 1], port));
    let Ok(mut stream) = TcpStream::connect_timeout(&addr, timeout) else {
        return false;
    };
    let _ = stream.set_read_timeout(Some(timeout));
    let _ = stream.set_write_timeout(Some(timeout));
    let req =
        format!("GET /healthz HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\nConnection: close\r\n\r\n");
    if stream.write_all(req.as_bytes()).is_err() {
        return false;
    }
    let mut buf = [0u8; 64];
    let mut got = Vec::new();
    while got.len() < 12 {
        match stream.read(&mut buf) {
            Ok(0) | Err(_) => break,
            Ok(n) => got.extend_from_slice(&buf[..n]),
        }
    }
    got.starts_with(b"HTTP/1.1 200") || got.starts_with(b"HTTP/1.0 200")
}

/// Entry point for `--healthcheck`: exit code 0 when healthy, 1 otherwise.
pub fn run() -> std::process::ExitCode {
    if check(probe_port(), Duration::from_secs(2)) {
        std::process::ExitCode::SUCCESS
    } else {
        std::process::ExitCode::FAILURE
    }
}
