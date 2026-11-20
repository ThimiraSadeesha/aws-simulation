package api

import (
	"net/http"
	"path/filepath"
	"time"

	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/services"
)

func init() {
	RegisterModule("secrets", 62, func(s *Server) {
		svc, err := services.NewSecretsService(s.Deps, filepath.Dir(s.Cfg.DatabasePath))
		if err != nil {
			s.log.Error("secrets manager initialisation failed", "err", err)
			panic("secrets manager: " + err.Error())
		}
		s.Ext.Store("secrets", svc)
		s.routesSecrets(svc)
	})
}

func secretARN(c *Ctx) string {
	n := c.Path("name")
	if n == "" {
		n = "*"
	}
	return services.SecretARN(c.P.AccountID, n)
}

func (s *Server) routesSecrets(svc *services.SecretsService) {
	const p = "/api/v1/secrets"
	s.op("GET "+p, "secretsmanager:ListSecrets", secretARN, func(c *Ctx) error {
		l, err := svc.List(c.Context(), c.P.AccountID)
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "name"))
	})
	s.op("POST "+p, "secretsmanager:CreateSecret", secretARN, func(c *Ctx) error {
		var in services.CreateSecretInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		v, err := svc.Create(c.Context(), c.P.AccountID, in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, v)
	})
	s.op("GET "+p+"/{name}", "secretsmanager:DescribeSecret", secretARN, func(c *Ctx) error {
		v, err := svc.Describe(c.Context(), c.P.AccountID, c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(200, v)
	})
	s.op("GET "+p+"/{name}/value", "secretsmanager:GetSecretValue", secretARN, func(c *Ctx) error {
		v, err := svc.GetValue(c.Context(), c.P.AccountID, c.Path("name"), c.Query("version_id"), c.Query("version_stage"))
		if err != nil {
			return err
		}
		// Reads are normally audited only when denied; secret value access is always audited.
		s.audit.Add(repositories.AuditEntry{TS: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), AccountID: c.P.AccountID,
			Principal: c.P.Display(), Action: "secretsmanager:GetSecretValue", Resource: secretARN(c), Result: "allowed",
			StatusCode: 200, RequestID: requestID(c.R), SourceIP: clientIP(c.R), Detail: "version " + v.VersionID})
		return c.JSON(200, v)
	})
	s.op("PUT "+p+"/{name}", "secretsmanager:PutSecretValue", secretARN, func(c *Ctx) error {
		var in services.UpdateSecretInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		if in.Description != nil {
			if err := c.Authorize("secretsmanager:UpdateSecret", secretARN(c), nil, nil); err != nil {
				return err
			}
		}
		v, err := svc.Update(c.Context(), c.P.AccountID, c.Path("name"), in)
		if err != nil {
			return err
		}
		return c.JSON(200, v)
	})
	s.op("DELETE "+p+"/{name}", "secretsmanager:DeleteSecret", secretARN, func(c *Ctx) error {
		if err := svc.Delete(c.Context(), c.P.AccountID, c.Path("name")); err != nil {
			return err
		}
		return c.NoContent()
	})
}
