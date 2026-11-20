package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/labs"
	"cloudsim/backend/internal/repositories"
)

// Training labs: definitions are loaded from cfg.LabsDir at startup (invalid files abort startup),
// progress is stored per account, and every step is validated against live simulator state by
// querying the API in-process as the account root.

type labsModule struct {
	s    *Server
	list []*labs.Lab
	byID map[string]*labs.Lab
	repo repositories.LabRepo
}

func init() {
	RegisterModule("labs", 90, func(s *Server) {
		m := &labsModule{s: s, byID: map[string]*labs.Lab{}, repo: repositories.NewLabRepo(s.Store.DB)}
		if _, err := os.Stat(s.Cfg.LabsDir); err != nil {
			s.log.Warn("training labs directory not found; no labs available", "dir", s.Cfg.LabsDir, "err", err)
		} else {
			l, err := labs.LoadDir(s.Cfg.LabsDir)
			if err != nil {
				// Fail loudly: a broken lab definition must never ship silently.
				panic(fmt.Sprintf("invalid training lab definitions in %s: %v", s.Cfg.LabsDir, err))
			}
			m.list = l
			for _, x := range l {
				m.byID[x.ID] = x
			}
			s.log.Info("training labs loaded", "count", len(l))
		}
		s.Ext.Store("labs", m)
		arn := func(c *Ctx) string {
			id := c.Path("id")
			if id == "" {
				id = "*"
			}
			return auth.ARN("labs", c.P.AccountID, "lab/"+id)
		}
		const p = "/api/v1/labs"
		s.op("GET "+p, "labs:ListLabs", arn, m.handleList)
		s.op("GET "+p+"/progress", "labs:ListLabs", arn, m.handleProgress)
		s.op("GET "+p+"/{id}", "labs:GetLab", arn, m.handleGet)
		s.op("POST "+p+"/{id}/start", "labs:StartLab", arn, m.handleStart)
		s.op("POST "+p+"/{id}/steps/{index}/validate", "labs:ValidateStep", arn, m.handleValidate)
		s.op("POST "+p+"/{id}/reset", "labs:ResetLab", arn, m.handleReset)
		s.DashboardContrib = append(s.DashboardContrib, func(ctx context.Context, account string, out map[string]any) {
			out["active_labs"] = m.active(ctx, account)
		})
	})
}

// rootCaller performs lab validation/reset calls as the account root, in-process.
type rootCaller struct {
	s *Server
	p *auth.Principal
}

func (r *rootCaller) Call(ctx context.Context, method, path string, body any) (int, []byte, error) {
	resp := r.s.Internal(ctx, r.p, method, "/api/v1"+path, body, nil)
	return resp.Status, resp.Body, nil
}

func (m *labsModule) lab(c *Ctx) (*labs.Lab, error) {
	l, ok := m.byID[c.Path("id")]
	if !ok {
		return nil, apierr.NotFound("lab %s not found", c.Path("id"))
	}
	return l, nil
}

func (m *labsModule) progress(ctx context.Context, account, id string) (*repositories.LabProgress, error) {
	p, err := m.repo.Get(ctx, account, id)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, nil
	}
	return p, err
}

func progressJSON(l *labs.Lab, p *repositories.LabProgress) map[string]any {
	steps := make([]map[string]any, len(l.Steps))
	for i := range l.Steps {
		steps[i] = map[string]any{"index": i, "status": "pending", "message": "", "checked_at": nil, "attempts": 0}
	}
	out := map[string]any{"status": "not_started", "started_at": nil, "completed_at": nil, "completed_steps": 0, "total_steps": len(l.Steps), "steps": steps}
	if p == nil {
		return out
	}
	done := 0
	for _, s := range p.Steps {
		if s.Index < len(steps) {
			steps[s.Index] = map[string]any{"index": s.Index, "status": s.Status, "message": s.Message, "checked_at": s.CheckedAt, "attempts": s.Attempts}
			if s.Status == "passed" {
				done++
			}
		}
	}
	out["status"], out["started_at"], out["completed_steps"] = p.Status, p.StartedAt, done
	if p.CompletedAt != nil {
		out["completed_at"] = *p.CompletedAt
	}
	return out
}

func labSummary(l *labs.Lab, p *repositories.LabProgress) map[string]any {
	pj := progressJSON(l, p)
	return map[string]any{"id": l.ID, "title": l.Title, "level": l.Level, "service": l.Service, "duration_min": l.DurationMin,
		"description": l.Description, "resource_prefix": l.ResourcePrefix, "status": pj["status"],
		"completed_steps": pj["completed_steps"], "total_steps": len(l.Steps)}
}

func labDetail(l *labs.Lab, p *repositories.LabProgress) map[string]any {
	out := labSummary(l, p)
	steps := make([]map[string]any, len(l.Steps))
	for i, s := range l.Steps {
		steps[i] = map[string]any{"index": i, "title": s.Title, "instructions_md": s.InstructionsMD, "hint": s.Hint, "cli": s.CLI, "console": s.Console,
			"validation": map[string]string{"type": s.Validation.Type, "description": s.Validation.Description}}
	}
	out["steps"], out["objectives"], out["notes"] = steps, l.Objectives, l.Notes
	out["workspace"] = map[string]string{"tag_key": "cloudsim:lab", "tag_value": l.ID, "resource_prefix": l.ResourcePrefix}
	out["progress"] = progressJSON(l, p)
	return out
}

func (m *labsModule) handleList(c *Ctx) error {
	ps, err := m.repo.List(c.Context(), c.P.AccountID)
	if err != nil {
		return err
	}
	byLab := map[string]*repositories.LabProgress{}
	for i := range ps {
		byLab[ps[i].LabID] = &ps[i]
	}
	items := make([]map[string]any, 0, len(m.list))
	for _, l := range m.list {
		items = append(items, labSummary(l, byLab[l.ID]))
	}
	return c.JSON(200, map[string]any{"items": items, "total": len(items)})
}

func (m *labsModule) active(ctx context.Context, account string) []map[string]any {
	ps, err := m.repo.List(ctx, account)
	out := []map[string]any{}
	if err != nil {
		return out
	}
	for i := range ps {
		l, ok := m.byID[ps[i].LabID]
		if !ok || ps[i].Status != "in_progress" {
			continue
		}
		pj := progressJSON(l, &ps[i])
		out = append(out, map[string]any{"lab_id": l.ID, "title": l.Title, "completed_steps": pj["completed_steps"], "total_steps": len(l.Steps)})
	}
	return out
}

func (m *labsModule) handleProgress(c *Ctx) error {
	ps, err := m.repo.List(c.Context(), c.P.AccountID)
	if err != nil {
		return err
	}
	items := []map[string]any{}
	completed := 0
	for i := range ps {
		l, ok := m.byID[ps[i].LabID]
		if !ok {
			continue
		}
		pj := progressJSON(l, &ps[i])
		if ps[i].Status == "completed" {
			completed++
		}
		items = append(items, map[string]any{"lab_id": l.ID, "title": l.Title, "status": pj["status"], "completed_steps": pj["completed_steps"],
			"total_steps": len(l.Steps), "started_at": pj["started_at"], "completed_at": pj["completed_at"]})
	}
	return c.JSON(200, map[string]any{"items": items, "total_labs": len(m.list), "started": len(items), "completed": completed, "active": m.active(c.Context(), c.P.AccountID)})
}

func (m *labsModule) handleGet(c *Ctx) error {
	l, err := m.lab(c)
	if err != nil {
		return err
	}
	p, err := m.progress(c.Context(), c.P.AccountID, l.ID)
	if err != nil {
		return err
	}
	return c.JSON(200, labDetail(l, p))
}

func (m *labsModule) handleStart(c *Ctx) error {
	l, err := m.lab(c)
	if err != nil {
		return err
	}
	p, err := m.repo.Start(c.Context(), c.P.AccountID, l.ID, len(l.Steps))
	if err != nil {
		return err
	}
	out := progressJSON(l, p)
	out["lab_id"] = l.ID
	out["workspace"] = map[string]string{"tag_key": "cloudsim:lab", "tag_value": l.ID, "resource_prefix": l.ResourcePrefix}
	return c.JSON(200, out)
}

func (m *labsModule) root(ctx context.Context, account string) (*rootCaller, error) {
	p, err := m.s.PrincipalForAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	return &rootCaller{s: m.s, p: p}, nil
}

func (m *labsModule) handleValidate(c *Ctx) error {
	l, err := m.lab(c)
	if err != nil {
		return err
	}
	idx, err := strconv.Atoi(c.Path("index"))
	if err != nil || idx < 0 || idx >= len(l.Steps) {
		return apierr.Validation("step index must be 0..%d", len(l.Steps)-1)
	}
	rc, err := m.root(c.Context(), c.P.AccountID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.Context(), 20*time.Second)
	defer cancel()
	res := labs.Evaluate(ctx, rc, l, idx, c.P.AccountID)
	status := "failed"
	if res.Passed {
		status = "passed"
	}
	det, _ := json.Marshal(res.Details)
	p, err := m.repo.RecordStep(c.Context(), c.P.AccountID, l.ID, len(l.Steps), repositories.LabStepResult{Index: idx, Status: status, Message: res.Message, Details: string(det)})
	if err != nil {
		return err
	}
	pj := progressJSON(l, p)
	m.s.Hub.Publish(c.P.AccountID, map[string]any{"type": "lab_progress", "lab_id": l.ID, "step": idx, "passed": res.Passed, "status": pj["status"]})
	return c.JSON(200, map[string]any{"passed": res.Passed, "message": res.Message, "details": res.Details, "progress": pj})
}

func (m *labsModule) handleReset(c *Ctx) error {
	l, err := m.lab(c)
	if err != nil {
		return err
	}
	rc, err := m.root(c.Context(), c.P.AccountID)
	if err != nil {
		return err
	}
	rep := labs.Reset(c.Context(), rc, l, 20*time.Second)
	if err := m.repo.Delete(c.Context(), c.P.AccountID, l.ID); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"lab_id": l.ID, "deleted": rep.Deleted, "remaining": rep.Remaining, "errors": rep.Errors, "progress": progressJSON(l, nil)})
}
