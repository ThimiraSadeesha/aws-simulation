package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/orchestration"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/services"
	"cloudsim/backend/internal/simulator"
)

func (s *Server) routesCore() {
	s.public("GET /healthz", s.handleHealth)
	s.public("POST /api/v1/auth/register", s.handleRegister)
	s.public("POST /api/v1/auth/login", s.handleLogin)
	s.op("POST /api/v1/auth/logout", "", nil, s.handleLogout)
	s.op("GET /api/v1/auth/me", "", nil, s.handleMe)
	s.op("GET /api/v1/dashboard/summary", "", nil, s.handleDashboard)
	s.op("GET /api/v1/events", "cloudwatch:DescribeEvents", nil, s.handleEvents)
	s.op("GET /api/v1/audit", "cloudtrail:LookupEvents", nil, s.handleAudit)
	s.op("GET /api/v1/ws/events", "", nil, s.handleEventsWS)

	// Engine event wiring.
	s.KindHandlers["instance"] = func(ctx context.Context, ev simulator.Event) {
		s.EC2.OnInstanceState(ctx, ev.ResourceID, ev.From, ev.To, ev.Reason)
	}
	s.KindHandlers["nat_gateway"] = func(ctx context.Context, ev simulator.Event) {
		s.Net.OnNATState(ctx, ev.ResourceID, ev.To, ev.Reason)
	}
	s.Resyncers = append(s.Resyncers, s.resyncCore)
}

func (s *Server) handleHealth(c *Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
	defer cancel()
	out := map[string]any{"status": "ok", "database": "ok", "engine": "ok", "version": s.Cfg.Version}
	if err := s.Store.DB.PingContext(ctx); err != nil {
		out["database"], out["status"] = "unavailable", "degraded"
	}
	if _, err := s.Engine.Health(ctx); err != nil {
		out["engine"], out["status"] = "unavailable", "degraded"
	}
	status := http.StatusOK
	if out["database"] != "ok" {
		status = http.StatusServiceUnavailable
	}
	return c.JSON(status, out)
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

func (s *Server) handleRegister(c *Ctx) error {
	var in credentials
	if err := c.Decode(&in); err != nil {
		return err
	}
	if !s.loginLimiter.allow("register|" + clientIP(c.R)) {
		return apierr.New(429, "RateLimited", "too many registration attempts; try again later")
	}
	u, err := s.Auth.Register(c.Context(), in.Username, in.Password, in.Email, s.Cfg.AllowRegistration)
	if err != nil {
		return err
	}
	s.log.Info("user registered", "username", u.Username, "role", u.Role)
	return c.JSON(http.StatusCreated, map[string]any{"user": u})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.Cfg.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

func (s *Server) handleLogin(c *Ctx) error {
	var in credentials
	if err := c.Decode(&in); err != nil {
		return err
	}
	if !s.loginLimiter.allow("login|" + clientIP(c.R) + "|" + in.Username) {
		return apierr.New(429, "RateLimited", "too many login attempts; wait a minute and try again")
	}
	u, token, csrf, err := s.Auth.Login(c.Context(), in.Username, in.Password, clientIP(c.R), c.R.UserAgent())
	if err != nil {
		if ai, ok := c.R.Context().Value(keyAudit).(*auditInfo); ok {
			ai.action, ai.resource, ai.denied, ai.detail = "signin:ConsoleLogin", "user/"+in.Username, true, "failed login"
		}
		return err
	}
	s.setSessionCookie(c.W, token, int(s.Cfg.SessionTTL.Seconds()))
	s.audit.Add(repositories.AuditEntry{AccountID: u.AccountID, TS: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Principal: "root",
		Action: "signin:ConsoleLogin", Resource: "arn:cloudsim:iam::" + u.AccountID + ":root", Result: "allowed", StatusCode: 200, RequestID: requestID(c.R), SourceIP: clientIP(c.R)})
	return c.JSON(http.StatusOK, map[string]any{"user": u, "csrf_token": csrf})
}

func (s *Server) handleLogout(c *Ctx) error {
	_ = s.Auth.Logout(c.Context(), c.P)
	s.setSessionCookie(c.W, "", -1)
	return c.NoContent()
}

func (s *Server) handleMe(c *Ctx) error {
	out := map[string]any{"user": c.P.PlatformUser, "principal": map[string]string{"arn": c.P.ARN, "type": c.P.Kind, "name": c.P.Name}}
	if c.P.ViaSession {
		out["csrf_token"] = c.P.CSRFToken
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) handleEvents(c *Ctx) error {
	p := parseList(c.R)
	evs, total, err := s.Store.Events.List(c.Context(), repositories.EventFilter{AccountID: c.P.AccountID, ResourceID: c.Query("resource_id"), Type: c.Query("type"),
		Limit: p.PageSize, Offset: (p.Page - 1) * p.PageSize})
	if err != nil {
		return err
	}
	return c.JSON(200, Page[repositories.Event]{Items: evs, Total: total, Page: p.Page, PageSize: p.PageSize})
}

func (s *Server) handleAudit(c *Ctx) error {
	p := parseList(c.R)
	es, total, err := s.Store.Audit.List(c.Context(), repositories.AuditFilter{AccountID: c.P.AccountID, Action: c.Query("action"), Principal: c.Query("principal"),
		Limit: p.PageSize, Offset: (p.Page - 1) * p.PageSize})
	if err != nil {
		return err
	}
	return c.JSON(200, Page[repositories.AuditEntry]{Items: es, Total: total, Page: p.Page, PageSize: p.PageSize})
}

func (s *Server) handleDashboard(c *Ctx) error {
	ctx, acct := c.Context(), c.P.AccountID
	insts, err := s.Store.EC2.ListInstances(ctx, repositories.InstanceFilter{AccountID: acct})
	if err != nil {
		return err
	}
	counts := map[string]int{"running": 0, "stopped": 0, "pending": 0, "total": 0}
	for _, i := range insts {
		if i.State == "terminated" {
			continue
		}
		counts["total"]++
		switch i.State {
		case "running", "stopped", "pending":
			counts[i.State]++
		}
	}
	vpcs, _ := s.Store.Network.ListVPCs(ctx, acct)
	subs, _ := s.Store.Network.ListSubnets(ctx, acct, "")
	sgs, _ := s.Store.Network.ListSGs(ctx, acct, "")
	evs, _, _ := s.Store.Events.List(ctx, repositories.EventFilter{AccountID: acct, Limit: 10})
	audit, _, _ := s.Store.Audit.List(ctx, repositories.AuditFilter{AccountID: acct, Limit: 10})
	health := map[string]string{"backend": "ok", "database": "ok", "engine": "ok"}
	hctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err := s.Engine.Health(hctx); err != nil {
		health["engine"] = "unavailable"
	}
	out := map[string]any{
		"instances": counts, "vpcs": len(vpcs), "subnets": len(subs), "security_groups": len(sgs),
		"resources_total": counts["total"] + len(vpcs) + len(subs) + len(sgs),
		"buckets":         0, "objects": 0, "storage_bytes": 0, "load_balancers": 0, "autoscaling_groups": 0,
		"alarms":          map[string]int{"ok": 0, "alarm": 0, "insufficient_data": 0},
		"avg_cpu_percent": 0.0, "network_in_bytes_5m": 0, "network_out_bytes_5m": 0, "cpu_series": []any{},
		"recent_events": evs, "recent_activity": audit, "health": health, "active_labs": []any{},
	}
	for _, f := range s.DashboardContrib {
		f(ctx, acct, out)
	}
	return c.JSON(200, out)
}

// ---- engine synchronisation

func (s *Server) runEngineSync(ctx context.Context) {
	p := &orchestration.EventPump{Engine: s.Engine, KV: s.Store.KV, Log: s.log, Handlers: s.KindHandlers, Resync: s.resyncAll}
	p.Run(ctx)
}

func (s *Server) resyncAll(ctx context.Context) {
	for _, r := range s.Resyncers {
		if err := r(ctx); err != nil {
			s.log.Error("engine resync failed", "err", err)
		}
	}
	s.log.Info("engine state resynchronized from database")
}

func ptr(s string) *string { return &s }

// resyncCore re-registers VPC resources, NAT gateways and instances in dependency order,
// restoring each with its persisted state (transitional states resume their timers).
func (s *Server) resyncCore(ctx context.Context) error {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT vpc_id, account_id, cidr_block FROM vpcs`)
	if err != nil {
		return err
	}
	type vpc struct{ id, acct, cidr string }
	var vs []vpc
	for rows.Next() {
		var v vpc
		if err := rows.Scan(&v.id, &v.acct, &v.cidr); err != nil {
			rows.Close()
			return err
		}
		vs = append(vs, v)
	}
	rows.Close()
	for _, v := range vs {
		if _, err := s.Engine.Register(ctx, v.id, simulator.RegisterRequest{Kind: "vpc", Owner: v.acct, State: ptr("available"), Attributes: map[string]any{"cidr": v.cidr}}); err != nil {
			return err
		}
	}
	type child struct{ id, acct, parent, kind string }
	var children []child
	for _, q := range []struct{ sql, kind string }{
		{`SELECT subnet_id, account_id, vpc_id FROM subnets`, "subnet"},
		{`SELECT route_table_id, account_id, vpc_id FROM route_tables`, "route_table"},
		{`SELECT network_acl_id, account_id, vpc_id FROM network_acls`, "network_acl"},
		{`SELECT group_id, account_id, vpc_id FROM security_groups`, "security_group"},
		{`SELECT internet_gateway_id, account_id, COALESCE(vpc_id,'') FROM internet_gateways`, "internet_gateway"},
	} {
		r, err := s.Store.DB.QueryContext(ctx, q.sql)
		if err != nil {
			return err
		}
		for r.Next() {
			var c child
			if err := r.Scan(&c.id, &c.acct, &c.parent); err != nil {
				r.Close()
				return err
			}
			c.kind = q.kind
			children = append(children, c)
		}
		r.Close()
	}
	for _, c := range children {
		deps := []string{}
		if c.parent != "" {
			deps = append(deps, c.parent)
		}
		if _, err := s.Engine.Register(ctx, c.id, simulator.RegisterRequest{Kind: c.kind, Owner: c.acct, State: ptr("available"), DependsOn: deps}); err != nil {
			return err
		}
	}
	nats, err := s.Store.Network.ListAllNATs(ctx)
	if err != nil {
		return err
	}
	for _, n := range nats {
		if _, err := s.Engine.Register(ctx, n.NATGatewayID, simulator.RegisterRequest{Kind: "nat_gateway", Owner: n.AccountID, State: ptr(n.State), DependsOn: []string{n.SubnetID}}); err != nil {
			return err
		}
	}
	insts, err := s.Store.EC2.ListInstances(ctx, repositories.InstanceFilter{})
	if err != nil {
		return err
	}
	for _, i := range insts {
		if i.State == "terminated" {
			continue
		}
		res, err := s.Engine.Register(ctx, i.InstanceID, simulator.RegisterRequest{Kind: "instance", Owner: i.AccountID, State: ptr(i.State), DependsOn: []string{i.SubnetID},
			Attributes: map[string]any{"instance_type": i.InstanceType, "vcpu": i.VCPU, "memory_mib": i.MemoryMiB, "workload": i.WorkloadProfile}})
		if err != nil {
			return err
		}
		if res.State != i.State {
			_ = s.Store.EC2.UpdateState(ctx, i.InstanceID, res.State, "")
		}
		if i.WorkloadProfile != "steady" {
			_ = s.Engine.SetWorkload(ctx, i.InstanceID, simulator.Workload{Profile: i.WorkloadProfile, CPUTarget: i.CPUTarget})
		}
	}
	return nil
}

// runReconciler periodically compares engine state with the database and corrects drift
// (e.g. events lost while the backend was down).
func (s *Server) runReconciler(ctx context.Context) {
	orchestration.Every(ctx, 30*time.Second, func(ctx context.Context) {
		insts, err := s.Store.EC2.ListInstances(ctx, repositories.InstanceFilter{})
		if err != nil {
			return
		}
		for _, i := range insts {
			if i.State == "terminated" {
				continue
			}
			r, err := s.Engine.Get(ctx, i.InstanceID)
			if err != nil {
				if simulator.IsCode(err, "not_found") {
					s.resyncAll(ctx)
					return
				}
				return
			}
			if r.State != i.State {
				s.log.Info("reconciler correcting instance state", "id", i.InstanceID, "db", i.State, "engine", r.State)
				s.EC2.OnInstanceState(ctx, i.InstanceID, i.State, r.State, "reconciled with simulation engine")
			}
		}
	})
}

func atoiDefault(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return d
}

func engineErr(err error) error { return services.EngineErr(err) }
