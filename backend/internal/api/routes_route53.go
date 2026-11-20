package api

import (
	"context"
	"net/http"

	"cloudsim/backend/internal/services"
)

func init() {
	RegisterModule("route53", 64, func(s *Server) {
		svc := services.NewRoute53Service(s.Deps, s.Net)
		s.Ext.Store("route53", svc)
		s.Resyncers = append(s.Resyncers, svc.Resync)
		services.ExternalResolvers = append(services.ExternalResolvers, func(ctx context.Context, account, name, vpcID string) ([]map[string]any, string) {
			return svc.Resolve(ctx, account, name, vpcID)
		})
		s.routesRoute53(svc)
	})
}

func zoneARN(c *Ctx) string {
	id := c.Path("id")
	if id == "" {
		id = "*"
	}
	return services.ZoneARN(id)
}

func (s *Server) routesRoute53(svc *services.Route53Service) {
	const p = "/api/v1/route53/zones"
	s.op("GET "+p, "route53:ListHostedZones", zoneARN, func(c *Ctx) error {
		l, err := svc.ListZones(c.Context(), c.P.AccountID)
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "name", "private", "vpc_id"))
	})
	s.op("POST "+p, "route53:CreateHostedZone", zoneARN, func(c *Ctx) error {
		var in services.CreateZoneInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		z, err := svc.CreateZone(c.Context(), c.P.AccountID, in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, z)
	})
	s.op("GET "+p+"/{id}", "route53:GetHostedZone", zoneARN, func(c *Ctx) error {
		z, err := svc.GetZone(c.Context(), c.P.AccountID, c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, z)
	})
	s.op("DELETE "+p+"/{id}", "route53:DeleteHostedZone", zoneARN, func(c *Ctx) error {
		if err := svc.DeleteZone(c.Context(), c.P.AccountID, c.Path("id")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("GET "+p+"/{id}/records", "route53:ListResourceRecordSets", zoneARN, func(c *Ctx) error {
		l, err := svc.ListRecords(c.Context(), c.P.AccountID, c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "name", "type"))
	})
	s.op("POST "+p+"/{id}/records", "route53:ChangeResourceRecordSets", zoneARN, func(c *Ctx) error {
		var in services.RecordInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		r, err := svc.UpsertRecord(c.Context(), c.P.AccountID, c.Path("id"), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, r)
	})
	s.op("DELETE "+p+"/{id}/records", "route53:ChangeResourceRecordSets", zoneARN, func(c *Ctx) error {
		if err := svc.DeleteRecord(c.Context(), c.P.AccountID, c.Path("id"), c.Query("name"), c.Query("type")); err != nil {
			return err
		}
		return c.NoContent()
	})
}
