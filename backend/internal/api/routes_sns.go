package api

import (
	"net/http"
	"strconv"

	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/services"
)

func init() {
	RegisterModule("sns", 61, func(s *Server) {
		v, _ := s.Ext.Load("sqs")
		sqs, _ := v.(*services.SQSService)
		if sqs == nil {
			sqs = services.NewSQSService(s.Deps)
		}
		svc := services.NewSNSService(s.Deps, sqs)
		s.Ext.Store("sns", svc)
		s.Resyncers = append(s.Resyncers, svc.Resync)
		s.routesSNS(svc)
	})
}

func snsARN(c *Ctx) string {
	n := c.Path("name")
	if n == "" {
		n = "*"
	}
	return auth.ARN("sns", c.P.AccountID, n)
}

func (s *Server) routesSNS(svc *services.SNSService) {
	const p = "/api/v1/sns/topics"
	s.op("GET "+p, "sns:ListTopics", snsARN, func(c *Ctx) error {
		l, err := svc.List(c.Context(), c.P.AccountID)
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "name"))
	})
	s.op("POST "+p, "sns:CreateTopic", snsARN, func(c *Ctx) error {
		var in services.CreateTopicInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		t, err := svc.Create(c.Context(), c.P.AccountID, in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, t)
	})
	s.op("GET "+p+"/{name}", "sns:GetTopicAttributes", snsARN, func(c *Ctx) error {
		t, err := svc.Get(c.Context(), c.P.AccountID, c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(200, t)
	})
	s.op("DELETE "+p+"/{name}", "sns:DeleteTopic", snsARN, func(c *Ctx) error {
		if err := svc.Delete(c.Context(), c.P.AccountID, c.Path("name")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("GET "+p+"/{name}/subscriptions", "sns:ListSubscriptionsByTopic", snsARN, func(c *Ctx) error {
		l, err := svc.ListSubscriptions(c.Context(), c.P.AccountID, c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "protocol", "endpoint"))
	})
	s.op("POST "+p+"/{name}/subscriptions", "sns:Subscribe", snsARN, func(c *Ctx) error {
		var in services.SubscribeInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		sub, err := svc.Subscribe(c.Context(), c.P.AccountID, c.Path("name"), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, sub)
	})
	s.op("DELETE "+p+"/{name}/subscriptions/{id}", "sns:Unsubscribe", snsARN, func(c *Ctx) error {
		if err := svc.Unsubscribe(c.Context(), c.P.AccountID, c.Path("name"), c.Path("id")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/{name}/publish", "sns:Publish", snsARN, func(c *Ctx) error {
		var in services.PublishInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		r, err := svc.Publish(c.Context(), c.P.AccountID, c.Path("name"), in)
		if err != nil {
			return err
		}
		return c.JSON(200, r)
	})
	s.op("GET "+p+"/{name}/deliveries", "sns:GetTopicAttributes", snsARN, func(c *Ctx) error {
		limit, _ := strconv.Atoi(c.Query("limit"))
		l, err := svc.Deliveries(c.Context(), c.P.AccountID, c.Path("name"), limit)
		if err != nil {
			return err
		}
		return c.JSON(200, map[string]any{"items": l})
	})
}
