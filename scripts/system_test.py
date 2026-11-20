#!/usr/bin/env python3
"""CloudSim system test: drives the real Docker Compose stack end to end.

Verifies: full startup + health checks, frontend->backend proxying, backend->engine
communication, EC2 lifecycle, S3 object round-trip, IAM enforcement through access keys,
VPC + reachability, training-lab validation, resource cleanup, database persistence and
recovery after restarting the backend and the engine containers.

Usage: python3 scripts/system_test.py [--no-build] [--keep]
Requires: docker compose, a .env file (make .env). Stdlib only.
"""
import argparse
import http.cookiejar
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CONSOLE = f"http://127.0.0.1:{os.environ.get('CONSOLE_PORT', '3000')}"
BACKEND = f"http://127.0.0.1:{os.environ.get('BACKEND_PORT', '8080')}"
PASSED, FAILED = [], []


def compose(*args, check=True):
    return subprocess.run(["docker", "compose", *args], cwd=ROOT, check=check, text=True, capture_output=True)


class Client:
    def __init__(self, base):
        self.base = base
        self.csrf = None
        self.auth = None
        self.session_token = None
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def req(self, method, path, body=None, raw=None, headers=None):
        data, hdrs = None, dict(headers or {})
        if raw is not None:
            data = raw
        elif body is not None:
            data = json.dumps(body).encode()
            hdrs["Content-Type"] = "application/json"
        if self.csrf and method not in ("GET", "HEAD"):
            hdrs["X-CSRF-Token"] = self.csrf
        if self.auth:
            hdrs["Authorization"] = self.auth
        r = urllib.request.Request(self.base + path, data=data, method=method, headers=hdrs)
        try:
            with self.opener.open(r, timeout=30) as resp:
                return resp.status, resp.read()
        except urllib.error.HTTPError as e:
            return e.code, e.read()

    def json(self, method, path, body=None, expect=None):
        status, data = self.req(method, path, body)
        if expect is not None and status != expect:
            raise AssertionError(f"{method} {path}: expected {expect}, got {status}: {data[:400]!r}")
        return json.loads(data) if data else {}


def check(name, fn):
    try:
        fn()
        PASSED.append(name)
        print(f"  PASS  {name}")
    except Exception as e:  # noqa: BLE001 - report every failure
        FAILED.append((name, str(e)))
        print(f"  FAIL  {name}: {e}")


def wait_for(cond, timeout=60, interval=0.5, what="condition"):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        try:
            if cond():
                return
        except Exception as e:  # noqa: BLE001
            last = e
        time.sleep(interval)
    raise AssertionError(f"timed out waiting for {what} ({last})")


def wait_healthy(timeout=240):
    def ok():
        s, b = Client(CONSOLE).req("GET", "/healthz")
        return s == 200 and json.loads(b).get("engine") == "ok"
    wait_for(ok, timeout, 2, "stack health via console proxy")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--no-build", action="store_true")
    ap.add_argument("--keep", action="store_true", help="leave the stack running")
    args = ap.parse_args()
    if not os.path.exists(os.path.join(ROOT, ".env")):
        sys.exit("missing .env (run: make .env)")

    print("== starting stack")
    t0 = time.time()
    compose("up", "-d", *([] if args.no_build else ["--build"]))
    wait_healthy()
    print(f"   stack healthy in {time.time() - t0:.1f}s")

    user = "sys" + uuid.uuid4().hex[:8]
    c = Client(CONSOLE)  # all browser-style traffic goes through the Next.js proxy
    state = {}

    def t_auth():
        c.json("POST", "/api/v1/auth/register", {"username": user, "password": "system-test-pw"}, 201)
        out = c.json("POST", "/api/v1/auth/login", {"username": user, "password": "system-test-pw"}, 200)
        c.csrf = out["csrf_token"]
        assert c.json("GET", "/api/v1/auth/me", expect=200)["user"]["username"] == user

    def t_engine_ports_not_published():
        out = compose("port", "engine", "7070", check=False)
        assert out.returncode != 0 or not out.stdout.strip(), f"engine port is published: {out.stdout}"

    def t_ec2():
        out = c.json("POST", "/api/v1/ec2/instances", {"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.micro", "name": "sys-web",
                     "user_data": "#!/bin/bash\nyum install -y httpd\nsystemctl start httpd\n"}, 201)
        iid = out["instances"][0]["instance_id"]
        state["iid"] = iid
        wait_for(lambda: c.json("GET", f"/api/v1/ec2/instances/{iid}")["state"] == "running", 60, 0.5, "instance running")
        c.json("POST", f"/api/v1/ec2/instances/{iid}/stop", expect=202)
        wait_for(lambda: c.json("GET", f"/api/v1/ec2/instances/{iid}")["state"] == "stopped", 60, 0.5, "instance stopped")
        c.json("POST", f"/api/v1/ec2/instances/{iid}/start", expect=202)
        wait_for(lambda: c.json("GET", f"/api/v1/ec2/instances/{iid}")["state"] == "running", 60, 0.5, "instance restarted")

    def t_s3():
        b = "sys-" + uuid.uuid4().hex[:10]
        state["bucket"] = b
        c.json("POST", "/api/v1/s3/buckets", {"name": b}, 201)
        payload = os.urandom(200_000)
        s, _ = c.req("PUT", f"/api/v1/s3/buckets/{b}/objects/dir/blob.bin", raw=payload, headers={"Content-Type": "application/octet-stream", "x-amz-meta-owner": "system-test"})
        assert s == 200, s
        s, got = c.req("GET", f"/api/v1/s3/buckets/{b}/objects/dir/blob.bin")
        assert s == 200 and got == payload, "object bytes differ"
        listing = c.json("GET", f"/api/v1/s3/buckets/{b}/objects?prefix=dir/", expect=200)
        assert any(o["key"] == "dir/blob.bin" for o in listing["items"]), listing

    def t_iam():
        c.json("POST", "/api/v1/iam/users", {"user_name": "sysreader"}, 201)
        c.req("POST", "/api/v1/iam/users/sysreader/attached-policies/CloudSimEC2ReadOnlyAccess")
        k = c.json("POST", "/api/v1/iam/users/sysreader/access-keys", expect=201)
        ro = Client(BACKEND)  # CLI-style access through the published backend port
        ro.auth = f"CloudSim {k['access_key_id']}:{k['secret_access_key']}"
        assert ro.req("GET", "/api/v1/ec2/instances")[0] == 200
        s, body = ro.req("POST", "/api/v1/vpc/vpcs", {"cidr_block": "10.77.0.0/16"})
        assert s == 403 and b"AccessDenied" in body, (s, body)

    def t_vpc():
        v = c.json("POST", "/api/v1/vpc/vpcs", {"cidr_block": "10.60.0.0/16", "name": "sys-vpc"}, 201)
        state["vpc"] = v["vpc_id"]
        sn = c.json("POST", "/api/v1/vpc/subnets", {"vpc_id": v["vpc_id"], "cidr_block": "10.60.1.0/24"}, 201)
        state["subnet"] = sn["subnet_id"]
        r = c.json("POST", "/api/v1/vpc/reachability", {"source": "internet", "destination": state["iid"], "protocol": "tcp", "port": 22}, 200)
        assert r["reachable"] is False and r["blocked_by"]["type"] == "security_group", r
        topo = c.json("GET", f"/api/v1/vpc/vpcs/{v['vpc_id']}/topology", expect=200)
        assert len(topo["subnets"]) == 1

    def t_lab():
        labs = c.json("GET", "/api/v1/labs", expect=200)["items"]
        assert len(labs) >= 10, f"expected >=10 labs, got {len(labs)}"
        lab = next(l for l in labs if l["id"] == "ec2-first-instance")
        c.json("POST", f"/api/v1/labs/{lab['id']}/start", expect=200)
        detail = c.json("GET", f"/api/v1/labs/{lab['id']}", expect=200)
        results = [c.json("POST", f"/api/v1/labs/{lab['id']}/steps/{s['index']}/validate", expect=200) for s in detail["steps"]]
        assert any(r["passed"] for r in results), results  # a running instance exists, so at least one step validates

    def t_persistence_backend_restart():
        compose("restart", "backend")
        wait_healthy()
        c2 = Client(CONSOLE)
        out = c2.json("POST", "/api/v1/auth/login", {"username": user, "password": "system-test-pw"}, 200)
        c2.csrf = out["csrf_token"]
        inst = c2.json("GET", f"/api/v1/ec2/instances/{state['iid']}", expect=200)
        assert inst["state"] == "running", inst["state"]
        s, _ = c2.req("GET", f"/api/v1/s3/buckets/{state['bucket']}/objects/dir/blob.bin")
        assert s == 200
        c.csrf = c2.csrf
        c.opener = c2.opener

    def t_engine_restart_recovery():
        compose("restart", "engine")
        wait_healthy()
        iid = state["iid"]
        # the backend must re-register resources so lifecycle actions keep working
        wait_for(lambda: c.req("POST", f"/api/v1/ec2/instances/{iid}/stop")[0] == 202, 60, 1, "stop accepted after engine restart")
        wait_for(lambda: c.json("GET", f"/api/v1/ec2/instances/{iid}")["state"] == "stopped", 60, 0.5, "stopped after engine restart")

    def t_cleanup():
        iid = state["iid"]
        c.json("POST", f"/api/v1/ec2/instances/{iid}/terminate", expect=202)
        wait_for(lambda: c.json("GET", f"/api/v1/ec2/instances/{iid}")["state"] == "terminated", 60, 0.5, "terminated")
        assert c.req("DELETE", f"/api/v1/s3/buckets/{state['bucket']}?force=true")[0] == 204
        assert c.req("DELETE", f"/api/v1/vpc/subnets/{state['subnet']}")[0] == 204
        assert c.req("DELETE", f"/api/v1/vpc/vpcs/{state['vpc']}")[0] == 204

    def t_frontend_pages():
        for path in ["/login", "/"]:
            s, body = Client(CONSOLE).req("GET", path)
            assert s in (200, 307, 308) and len(body) > 0, (path, s)

    print("== running checks")
    for name, fn in [
        ("register/login through console proxy", t_auth),
        ("engine port not published", t_engine_ports_not_published),
        ("frontend serves pages", t_frontend_pages),
        ("EC2 lifecycle via engine", t_ec2),
        ("S3 object round-trip", t_s3),
        ("IAM enforcement with access keys", t_iam),
        ("VPC + reachability analysis", t_vpc),
        ("training lab validation", t_lab),
        ("persistence across backend restart", t_persistence_backend_restart),
        ("recovery after engine restart", t_engine_restart_recovery),
        ("resource cleanup", t_cleanup),
    ]:
        check(name, fn)

    if not args.keep:
        compose("down")
    print(f"\n{len(PASSED)} passed, {len(FAILED)} failed")
    for n, e in FAILED:
        print(f" - {n}: {e}")
    sys.exit(1 if FAILED else 0)


if __name__ == "__main__":
    main()
