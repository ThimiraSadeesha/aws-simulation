#!/usr/bin/env python3
"""HTTP load test against a running CloudSim backend (stdlib only).

Creates a throwaway user, then runs a mixed workload with N concurrent workers:
reads (describe instances, list VPCs, dashboard) and writes (launch + terminate instances,
create security groups). Prints throughput and latency percentiles per operation.

Usage: python3 scripts/loadtest.py [--base http://127.0.0.1:8080] [--workers 32] [--seconds 20]
"""
import argparse
import http.cookiejar
import json
import statistics
import threading
import time
import urllib.error
import urllib.request
import uuid
from collections import defaultdict


def make_client(base):
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    csrf = {}

    def call(method, path, body=None):
        data = json.dumps(body).encode() if body is not None else None
        h = {"Content-Type": "application/json"} if data else {}
        if csrf.get("t") and method != "GET":
            h["X-CSRF-Token"] = csrf["t"]
        req = urllib.request.Request(base + path, data=data, method=method, headers=h)
        try:
            with opener.open(req, timeout=30) as r:
                return r.status, r.read()
        except urllib.error.HTTPError as e:
            return e.code, e.read()

    return call, csrf


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:8080")
    ap.add_argument("--workers", type=int, default=32)
    ap.add_argument("--seconds", type=int, default=20)
    a = ap.parse_args()

    call, csrf = make_client(a.base)
    user = "load" + uuid.uuid4().hex[:8]
    call("POST", "/api/v1/auth/register", {"username": user, "password": "load-test-pw"})
    s, b = call("POST", "/api/v1/auth/login", {"username": user, "password": "load-test-pw"})
    if s != 200:
        raise SystemExit(f"login failed: {s} {b[:200]!r}")
    csrf["t"] = json.loads(b)["csrf_token"]
    # warm up: default VPC + a few instances
    for _ in range(3):
        call("POST", "/api/v1/ec2/instances", {"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.nano"})

    lat = defaultdict(list)
    errors = defaultdict(int)
    lock = threading.Lock()
    stop = time.time() + a.seconds

    ops = [
        ("describe-instances", lambda: call("GET", "/api/v1/ec2/instances?page_size=50")),
        ("list-vpcs", lambda: call("GET", "/api/v1/vpc/vpcs")),
        ("dashboard", lambda: call("GET", "/api/v1/dashboard/summary")),
        ("describe-instances", lambda: call("GET", "/api/v1/ec2/instances?state=running")),
    ]

    def worker(i):
        n = 0
        while time.time() < stop:
            if n % 10 == 9:  # 10% writes
                name, fn = "run+terminate-instance", None
                t = time.perf_counter()
                s, b = call("POST", "/api/v1/ec2/instances", {"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.nano"})
                if s == 201:
                    iid = json.loads(b)["instances"][0]["instance_id"]
                    s, _ = call("POST", f"/api/v1/ec2/instances/{iid}/terminate")
                    ok = s == 202
                else:
                    ok = s == 429  # quota is an expected outcome under load
                dt = time.perf_counter() - t
            else:
                name, fn = ops[n % len(ops)]
                t = time.perf_counter()
                s, _ = fn()
                dt = time.perf_counter() - t
                ok = s == 200
            with lock:
                lat[name].append(dt * 1000)
                if not ok:
                    errors[name] += 1
            n += 1

    threads = [threading.Thread(target=worker, args=(i,)) for i in range(a.workers)]
    t0 = time.time()
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    elapsed = time.time() - t0
    total = sum(len(v) for v in lat.values())
    print(f"workers={a.workers} duration={elapsed:.1f}s requests={total} throughput={total / elapsed:.0f} ops/s")
    print(f"{'operation':26} {'count':>7} {'p50ms':>8} {'p95ms':>8} {'p99ms':>8} {'errors':>7}")
    for name, v in sorted(lat.items()):
        v.sort()
        q = lambda p: v[min(len(v) - 1, int(p * len(v)))]  # noqa: E731
        print(f"{name:26} {len(v):7d} {statistics.median(v):8.1f} {q(0.95):8.1f} {q(0.99):8.1f} {errors[name]:7d}")


if __name__ == "__main__":
    main()
