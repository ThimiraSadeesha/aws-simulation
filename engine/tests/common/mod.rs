#![allow(dead_code)]

use axum::body::Body;
use axum::http::{HeaderMap, Method, Request, StatusCode};
use axum::Router;
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tower::ServiceExt;

use cloudsim_engine::{router, Config, Engine};

pub const TOKEN: &str = "test-token";

pub struct App {
    pub engine: Engine,
    pub router: Router,
}

pub fn app_with(f: impl FnOnce(&mut Config)) -> App {
    let mut cfg = Config::for_tests(TOKEN);
    f(&mut cfg);
    let engine = Engine::new(cfg);
    App {
        router: router(engine.clone()),
        engine,
    }
}

pub fn app() -> App {
    app_with(|_| {})
}

pub struct Resp {
    pub status: StatusCode,
    pub headers: HeaderMap,
    pub body: Value,
}

impl App {
    pub async fn raw(
        &self,
        method: Method,
        uri: &str,
        body: Option<Value>,
        token: Option<&str>,
    ) -> Resp {
        let mut b = Request::builder().method(method).uri(uri);
        if let Some(t) = token {
            b = b.header("X-Engine-Token", t);
        }
        let req = match body {
            Some(v) => b
                .header("Content-Type", "application/json")
                .body(Body::from(serde_json::to_vec(&v).unwrap()))
                .unwrap(),
            None => b.body(Body::empty()).unwrap(),
        };
        let res = self.router.clone().oneshot(req).await.unwrap();
        let status = res.status();
        let headers = res.headers().clone();
        let bytes = res.into_body().collect().await.unwrap().to_bytes();
        let body = if bytes.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&bytes)
                .unwrap_or_else(|_| Value::String(String::from_utf8_lossy(&bytes).into()))
        };
        Resp {
            status,
            headers,
            body,
        }
    }

    pub async fn call(&self, method: Method, uri: &str, body: Option<Value>) -> Resp {
        self.raw(method, uri, body, Some(TOKEN)).await
    }

    pub async fn get(&self, uri: &str) -> Resp {
        self.call(Method::GET, uri, None).await
    }

    pub async fn post(&self, uri: &str, body: Value) -> Resp {
        self.call(Method::POST, uri, Some(body)).await
    }

    pub async fn put(&self, uri: &str, body: Value) -> Resp {
        self.call(Method::PUT, uri, Some(body)).await
    }

    pub async fn delete(&self, uri: &str) -> Resp {
        self.call(Method::DELETE, uri, None).await
    }

    pub async fn register(&self, id: &str, kind: &str, deps: &[&str]) -> Resp {
        self.put(
            &format!("/v1/resources/{id}"),
            json!({"kind": kind, "owner": "123456789012", "state": null, "depends_on": deps, "attributes": {}}),
        )
        .await
    }

    pub async fn act(&self, id: &str, action: &str) -> Resp {
        self.post(
            &format!("/v1/resources/{id}/actions"),
            json!({"action": action, "reason": "User initiated"}),
        )
        .await
    }

    pub async fn state(&self, id: &str) -> String {
        self.get(&format!("/v1/resources/{id}")).await.body["state"]
            .as_str()
            .unwrap_or("<none>")
            .to_string()
    }

    pub async fn events(&self, after: u64) -> Vec<Value> {
        self.get(&format!("/v1/events?after={after}&limit=1000"))
            .await
            .body["events"]
            .as_array()
            .cloned()
            .unwrap_or_default()
    }
}

pub fn assert_error(r: &Resp, status: u16, code: &str) {
    assert_eq!(r.status.as_u16(), status, "body: {}", r.body);
    assert_eq!(r.body["error"]["code"], code, "body: {}", r.body);
    assert!(r.body["error"]["message"].is_string());
}
