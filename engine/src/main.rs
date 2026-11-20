use std::process::ExitCode;

use cloudsim_engine::{healthcheck, router, Config, Engine};
use tracing_subscriber::EnvFilter;

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    if args.iter().any(|a| a == "--healthcheck") {
        return healthcheck::run();
    }
    if args.iter().any(|a| a == "--version" || a == "-V") {
        println!("cloudsim-engine {}", env!("CARGO_PKG_VERSION"));
        return ExitCode::SUCCESS;
    }

    tracing_subscriber::fmt()
        .json()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .with_current_span(false)
        .init();

    let cfg = match Config::from_env() {
        Ok(c) => c,
        Err(e) => {
            tracing::error!(error = %e, "invalid configuration; refusing to start");
            return ExitCode::from(2);
        }
    };

    let runtime = match tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
    {
        Ok(r) => r,
        Err(e) => {
            tracing::error!(error = %e, "failed to start tokio runtime");
            return ExitCode::FAILURE;
        }
    };
    match runtime.block_on(serve(cfg)) {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            tracing::error!(error = %e, "engine stopped with error");
            ExitCode::FAILURE
        }
    }
}

async fn serve(cfg: Config) -> std::io::Result<()> {
    let engine = Engine::new(cfg.clone());
    let listener = tokio::net::TcpListener::bind(cfg.addr).await?;
    tracing::info!(
        addr = %listener.local_addr()?,
        time_scale = cfg.time_scale,
        seed = cfg.seed,
        tick_ms = cfg.tick_ms,
        event_buffer = cfg.event_buffer,
        version = env!("CARGO_PKG_VERSION"),
        "cloudsim-engine listening"
    );
    let ticker = engine.spawn_tick_loop();
    let shutdown_engine = engine.clone();
    axum::serve(listener, router(engine.clone()))
        .with_graceful_shutdown(async move {
            shutdown_signal().await;
            tracing::info!("shutdown signal received; draining connections");
            shutdown_engine.begin_shutdown();
        })
        .await?;
    ticker.abort();
    tracing::info!("cloudsim-engine stopped");
    Ok(())
}

async fn shutdown_signal() {
    let ctrl_c = async {
        let _ = tokio::signal::ctrl_c().await;
    };
    #[cfg(unix)]
    let term = async {
        match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
            Ok(mut s) => {
                s.recv().await;
            }
            Err(_) => std::future::pending::<()>().await,
        }
    };
    #[cfg(not(unix))]
    let term = std::future::pending::<()>();
    tokio::select! {
        _ = ctrl_c => {},
        _ = term => {},
    }
}
