package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/orchestration"
	"cloudsim/backend/internal/services"
)

func init() {
	RegisterModule("sqs", 60, func(s *Server) {
		svc := services.NewSQSService(s.Deps)
		s.Ext.Store("sqs", svc)
		s.Resyncers = append(s.Resyncers, svc.Resync)
		s.Workers = append(s.Workers, func(ctx context.Context) {
			orchestration.Every(ctx, time.Minute, func(ctx context.Context) {
				if n, err := svc.CleanupExpired(ctx); err != nil {
					s.log.Error("sqs retention cleanup failed", "err", err)
				} else if n > 0 {
					s.log.Info("sqs retention cleanup", "deleted", n)
				}
			})
		})
		s.routesSQS(svc)
	})
}

func sqsARN(c *Ctx) string {
	n := c.Path("name")
	if n == "" {
		n = "*"
	}
	return auth.ARN("sqs", c.P.AccountID, n)
}

func optInt(c *Ctx, name string) (*int, error) {
	v := c.Query(name)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, apierr.Validation("%s must be an integer", name)
	}
	return &n, nil
}

func (s *Server) routesSQS(svc *services.SQSService) {
	const p = "/api/v1/sqs/queues"
	s.op("GET "+p, "sqs:ListQueues", sqsARN, func(c *Ctx) error {
		l, err := svc.List(c.Context(), c.P.AccountID)
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "name", "dead_letter_queue"))
	})
	s.op("POST "+p, "sqs:CreateQueue", func(c *Ctx) string { return auth.ARN("sqs", c.P.AccountID, "*") }, func(c *Ctx) error {
		var in services.CreateQueueInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		q, err := svc.Create(c.Context(), c.P.AccountID, in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, q)
	})
	s.op("GET "+p+"/{name}", "sqs:GetQueueAttributes", sqsARN, func(c *Ctx) error {
		q, err := svc.Get(c.Context(), c.P.AccountID, c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(200, q)
	})
	s.op("PATCH "+p+"/{name}", "sqs:SetQueueAttributes", sqsARN, func(c *Ctx) error {
		var in services.QueueAttributes
		if err := c.Decode(&in); err != nil {
			return err
		}
		q, err := svc.Update(c.Context(), c.P.AccountID, c.Path("name"), in)
		if err != nil {
			return err
		}
		return c.JSON(200, q)
	})
	s.op("DELETE "+p+"/{name}", "sqs:DeleteQueue", sqsARN, func(c *Ctx) error {
		if err := svc.Delete(c.Context(), c.P.AccountID, c.Path("name")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/{name}/messages", "sqs:SendMessage", sqsARN, func(c *Ctx) error {
		var in services.SendMessageInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		m, err := svc.Send(c.Context(), c.P.AccountID, c.Path("name"), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, m)
	})
	s.op("GET "+p+"/{name}/messages", "sqs:ReceiveMessage", sqsARN, func(c *Ctx) error {
		max := 1
		if m, err := optInt(c, "max"); err != nil {
			return err
		} else if m != nil {
			max = *m
		}
		vt, err := optInt(c, "visibility_timeout")
		if err != nil {
			return err
		}
		msgs, err := svc.Receive(c.Context(), c.P.AccountID, c.Path("name"), max, vt)
		if err != nil {
			return err
		}
		return c.JSON(200, map[string]any{"messages": msgs})
	})
	s.op("DELETE "+p+"/{name}/messages/{receipt}", "sqs:DeleteMessage", sqsARN, func(c *Ctx) error {
		if err := svc.DeleteMessage(c.Context(), c.P.AccountID, c.Path("name"), c.Path("receipt")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/{name}/purge", "sqs:PurgeQueue", sqsARN, func(c *Ctx) error {
		n, err := svc.Purge(c.Context(), c.P.AccountID, c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(200, map[string]any{"purged": n})
	})
}
