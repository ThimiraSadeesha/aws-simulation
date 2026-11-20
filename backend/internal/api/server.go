package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/config"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/services"
	"cloudsim/backend/internal/simulator"
)

// Server wires configuration, repositories, services and HTTP routes.
type Server struct {
	Cfg    *config.Config
	Store  *repositories.Store
	Auth   *auth.Service
	Engine *simulator.Client
	Deps   *services.Deps
	Net    *services.NetworkService
	EC2    *services.EC2Service
	IAM    *services.IAMService
	Hub    *Hub

	// Engine event handlers by resource kind (instance, nat_gateway, load_balancer, …).
	KindHandlers map[string]func(ctx context.Context, ev simulator.Event)
	// Resyncers re-register live resources with the engine (restart recovery).
	Resyncers []func(ctx context.Context) error
	// Workers are long-running background loops started by Start.
	Workers []func(ctx context.Context)
	// DashboardContrib lets modules add fields to /dashboard/summary.
	DashboardContrib []func(ctx context.Context, account string, out map[string]any)
	// Ext is a typed-at-use-site bag where modules store their services for cross-module access.
	Ext sync.Map

	mux          *http.ServeMux
	log          *slog.Logger
	limiter      *limiterSet
	loginLimiter *limiterSet
	audit        *auditWriter
	handler      http.Handler
}

// Module is a self-contained service registration (routes, workers, hooks).
type Module func(s *Server)

var (
	modMu   sync.Mutex
	modules []namedModule
)

type namedModule struct {
	name  string
	order int
	fn    Module
}

// RegisterModule is called from init() in each service's handler file. Lower order runs first.
func RegisterModule(name string, order int, fn Module) {
	modMu.Lock()
	defer modMu.Unlock()
	modules = append(modules, namedModule{name, order, fn})
}

func New(cfg *config.Config, store *repositories.Store, eng *simulator.Client, log *slog.Logger) *Server {
	s := &Server{
		Cfg: cfg, Store: store, Engine: eng, log: log,
		mux:          http.NewServeMux(),
		limiter:      newLimiterSet(cfg.RateLimitRPS, cfg.RateLimitBurst),
		loginLimiter: newLimiterSet(float64(cfg.LoginPerMinute)/60.0, max(cfg.LoginPerMinute, 1)),
		KindHandlers: map[string]func(context.Context, simulator.Event){},
	}
	s.Auth = auth.NewService(store, cfg.SessionTTL)
	s.Deps = services.NewDeps(store, eng, log)
	s.Hub = NewHub(log)
	s.Deps.Pub = s.Hub
	s.Net = services.NewNetworkService(s.Deps)
	s.EC2 = services.NewEC2Service(s.Deps, s.Net)
	s.IAM = services.NewIAMService(s.Deps, s.Auth)
	s.audit = newAuditWriter(store.Audit, log)

	s.routesCore()
	s.routesEC2()
	s.routesVPC()
	s.routesIAM()
	s.routesAdmin()
	modMu.Lock()
	mods := append([]namedModule(nil), modules...)
	modMu.Unlock()
	sortModules(mods)
	for _, m := range mods {
		m.fn(s)
	}
	s.mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "NotFound", "message": "no such API route: " + r.Method + " " + r.URL.Path, "request_id": requestID(r)}})
	}))
	s.handler = s.base(s.cors(s.mux))
	return s
}

func sortModules(m []namedModule) {
	for i := 1; i < len(m); i++ {
		for j := i; j > 0 && (m[j].order < m[j-1].order || (m[j].order == m[j-1].order && m[j].name < m[j-1].name)); j-- {
			m[j], m[j-1] = m[j-1], m[j]
		}
	}
}

func (s *Server) Handler() http.Handler { return s.handler }
func (s *Server) Logger() *slog.Logger  { return s.log }

// cors only allows explicitly configured origins (the default deployment is same-origin via the frontend proxy).
func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.Cfg.AllowedOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && allowed[o] {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, Authorization, X-CloudSim-Session-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Start launches background workers; it returns when ctx is cancelled and workers have stopped.
func (s *Server) Start(ctx context.Context) {
	var wg sync.WaitGroup
	run := func(f func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f(ctx)
		}()
	}
	run(s.audit.Run)
	run(s.runEngineSync)
	run(s.runReconciler)
	run(s.runHousekeeping)
	for _, w := range s.Workers {
		run(w)
	}
	wg.Wait()
}

// Init performs one-time startup tasks (seed data).
func (s *Server) Init(ctx context.Context) error {
	return s.IAM.SeedBuiltins(ctx)
}

func (s *Server) runHousekeeping(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.Store.Sessions.DeleteExpired(ctx); err == nil && n > 0 {
				s.log.Info("expired sessions removed", "count", n)
			}
			_, _ = s.Store.IAM.DeleteExpiredKeys(ctx)
			_ = s.Store.Events.Prune(ctx, 5000)
			s.limiter.gc(10 * time.Minute)
		}
	}
}

// FlushAudit waits for queued audit entries (tests).
func (s *Server) FlushAudit() { s.audit.Flush() }
