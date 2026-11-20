package api

import (
	"encoding/json"
	"net/http"

	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/services"
)

func init() {
	RegisterModule("dynamodb", 63, func(s *Server) {
		svc := services.NewDynamoDBService(s.Deps)
		s.Ext.Store("dynamodb", svc)
		s.Resyncers = append(s.Resyncers, svc.Resync)
		s.routesDynamoDB(svc)
	})
}

func ddbARN(c *Ctx) string {
	n := c.Path("name")
	if n == "" {
		n = "*"
	}
	return auth.ARN("dynamodb", c.P.AccountID, "table/"+n)
}

func (s *Server) routesDynamoDB(svc *services.DynamoDBService) {
	const p = "/api/v1/dynamodb/tables"
	s.op("GET "+p, "dynamodb:ListTables", ddbARN, func(c *Ctx) error {
		l, err := svc.List(c.Context(), c.P.AccountID)
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "table_name", "status"))
	})
	s.op("POST "+p, "dynamodb:CreateTable", ddbARN, func(c *Ctx) error {
		var in services.CreateTableInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		t, err := svc.Create(c.Context(), c.P.AccountID, in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, t)
	})
	s.op("GET "+p+"/{name}", "dynamodb:DescribeTable", ddbARN, func(c *Ctx) error {
		t, err := svc.Get(c.Context(), c.P.AccountID, c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(200, t)
	})
	s.op("DELETE "+p+"/{name}", "dynamodb:DeleteTable", ddbARN, func(c *Ctx) error {
		if err := svc.Delete(c.Context(), c.P.AccountID, c.Path("name")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("PUT "+p+"/{name}/items", "dynamodb:PutItem", ddbARN, func(c *Ctx) error {
		var raw json.RawMessage
		if err := c.Decode(&raw); err != nil {
			return err
		}
		it, err := svc.PutItem(c.Context(), c.P.AccountID, c.Path("name"), raw)
		if err != nil {
			return err
		}
		return c.JSON(200, map[string]any{"item": it})
	})
	s.op("GET "+p+"/{name}/items", "dynamodb:GetItem", ddbARN, func(c *Ctx) error {
		it, err := svc.GetItem(c.Context(), c.P.AccountID, c.Path("name"), c.Query("pk"), c.Query("sk"))
		if err != nil {
			return err
		}
		return c.JSON(200, map[string]any{"item": it})
	})
	s.op("DELETE "+p+"/{name}/items", "dynamodb:DeleteItem", ddbARN, func(c *Ctx) error {
		if err := svc.DeleteItem(c.Context(), c.P.AccountID, c.Path("name"), c.Query("pk"), c.Query("sk")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/{name}/query", "dynamodb:Query", ddbARN, func(c *Ctx) error {
		var in services.QueryInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		r, err := svc.Query(c.Context(), c.P.AccountID, c.Path("name"), in)
		if err != nil {
			return err
		}
		return c.JSON(200, r)
	})
	s.op("GET "+p+"/{name}/scan", "dynamodb:Scan", ddbARN, func(c *Ctx) error {
		limit, err := optInt(c, "limit")
		if err != nil {
			return err
		}
		l := 0
		if limit != nil {
			l = *limit
		}
		r, err := svc.Scan(c.Context(), c.P.AccountID, c.Path("name"), l, c.Query("exclusive_start_key"))
		if err != nil {
			return err
		}
		return c.JSON(200, r)
	})
}
