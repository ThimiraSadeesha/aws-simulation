package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/db"
)

// RestoreRequested is set when an admin restores a backup; main() performs the swap on restart.
var RestoreRequested = make(chan string, 1)

var backupNameRe = regexp.MustCompile(`^cloudsim-\d{8}T\d{6}Z\.db$`)

func (s *Server) adminOnly(h Op) Op {
	return func(c *Ctx) error {
		if !c.P.IsAdmin() {
			return apierr.Denied("platform administrator role required")
		}
		if ai, ok := c.R.Context().Value(keyAudit).(*auditInfo); ok && ai.action == "" {
			ai.action, ai.resource = "admin:"+c.R.Method+" "+c.R.URL.Path, "platform"
		}
		return h(c)
	}
}

func (s *Server) routesAdmin() {
	const p = "/api/v1/admin"
	s.op("GET "+p+"/users", "", nil, s.adminOnly(func(c *Ctx) error {
		l, err := s.Store.Users.List(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "role"))
	}))
	s.op("PATCH "+p+"/users/{id}", "", nil, s.adminOnly(func(c *Ctx) error {
		var in struct {
			Role string `json:"role"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if in.Role != "admin" && in.Role != "user" {
			return apierr.Validation("role must be admin or user")
		}
		id, err := strconv.ParseInt(c.Path("id"), 10, 64)
		if err != nil {
			return apierr.Validation("invalid user id")
		}
		if id == c.P.PlatformUser.ID && in.Role != "admin" {
			return apierr.Validation("you cannot remove your own admin role")
		}
		if err := s.Store.Users.SetRole(c.Context(), id, in.Role); err != nil {
			return apierr.NotFound("user not found")
		}
		u, _ := s.Store.Users.GetByID(c.Context(), id)
		return c.JSON(200, u)
	}))
	s.op("GET "+p+"/engine", "", nil, s.adminOnly(func(c *Ctx) error {
		stats, err := s.Engine.Stats(c.Context())
		if err != nil {
			return apierr.New(503, "EngineUnavailable", "engine unavailable: %v", err)
		}
		var quotas map[string]any
		_ = s.Engine.Raw(c.Context(), "GET", "/v1/quotas", nil, &quotas)
		return c.JSON(200, map[string]any{"stats": stats, "quotas": quotas, "websocket_subscribers": s.Hub.Subscribers()})
	}))
	s.op("GET "+p+"/integrity", "", nil, s.adminOnly(func(c *Ctx) error {
		r, err := s.Store.DB.Integrity(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(200, r)
	}))
	s.op("POST "+p+"/backups", "", nil, s.adminOnly(func(c *Ctx) error {
		if err := os.MkdirAll(s.Cfg.BackupDir, 0o750); err != nil {
			return err
		}
		name := "cloudsim-" + time.Now().UTC().Format("20060102T150405Z") + ".db"
		dst := filepath.Join(s.Cfg.BackupDir, name)
		if _, err := os.Stat(dst); err == nil {
			return apierr.Conflict("a backup was already created this second; retry")
		}
		if err := s.Store.DB.Backup(c.Context(), dst); err != nil {
			return err
		}
		st, _ := os.Stat(dst)
		return c.JSON(http.StatusCreated, map[string]any{"file": name, "size": st.Size(), "created_at": db.Now()})
	}))
	s.op("GET "+p+"/backups", "", nil, s.adminOnly(func(c *Ctx) error {
		entries, _ := os.ReadDir(s.Cfg.BackupDir)
		out := []map[string]any{}
		for _, e := range entries {
			if !backupNameRe.MatchString(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, map[string]any{"file": e.Name(), "size": info.Size(), "created_at": info.ModTime().UTC().Format(time.RFC3339)})
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["file"].(string) > out[j]["file"].(string) })
		return c.JSON(200, map[string]any{"items": out})
	}))
	s.op("POST "+p+"/restore", "", nil, s.adminOnly(func(c *Ctx) error {
		var in struct {
			File string `json:"file"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if !backupNameRe.MatchString(in.File) {
			return apierr.Validation("invalid backup file name")
		}
		src := filepath.Join(s.Cfg.BackupDir, in.File)
		b, err := db.Open(src)
		if err != nil {
			return apierr.NotFound("backup %s not found", in.File)
		}
		rep, err := b.Integrity(c.Context())
		b.Close()
		if err != nil || rep.IntegrityCheck != "ok" || len(rep.ForeignKeyViolations) > 0 {
			return apierr.Validation("backup failed integrity check: %v", rep)
		}
		select {
		case RestoreRequested <- src:
		default:
			return apierr.Conflict("a restore is already in progress")
		}
		return c.JSON(http.StatusAccepted, map[string]any{"status": "restoring", "file": in.File,
			"message": "the backend will restore the backup and restart; reconnect in a few seconds"})
	}))
	s.op("GET "+p+"/faults", "", nil, s.adminOnly(func(c *Ctx) error {
		var out any
		if err := s.Engine.Raw(c.Context(), "GET", "/v1/faults", nil, &out); err != nil {
			return apierr.New(503, "EngineUnavailable", "%v", err)
		}
		return c.JSON(200, out)
	}))
	s.op("POST "+p+"/faults", "", nil, s.adminOnly(func(c *Ctx) error {
		var in map[string]any
		if err := c.Decode(&in); err != nil {
			return err
		}
		var out any
		if err := s.Engine.Raw(c.Context(), "POST", "/v1/faults", in, &out); err != nil {
			return fmt.Errorf("%w", engineErr(err))
		}
		return c.JSON(http.StatusCreated, out)
	}))
	s.op("DELETE "+p+"/faults/{id}", "", nil, s.adminOnly(func(c *Ctx) error {
		if err := s.Engine.Raw(c.Context(), "DELETE", "/v1/faults/"+c.Path("id"), nil, nil); err != nil {
			return engineErr(err)
		}
		return c.NoContent()
	}))
}
