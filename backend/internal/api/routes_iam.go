package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/policy"
	"cloudsim/backend/internal/services"
)

func iamARN(kind string) ArnFn {
	return func(c *Ctx) string {
		n := c.Path("name")
		if n == "" {
			n = "*"
		}
		return "arn:cloudsim:iam::" + c.P.AccountID + ":" + kind + "/" + n
	}
}

func (s *Server) routesIAM() {
	const p = "/api/v1/iam"
	acct := func(c *Ctx) string { return c.P.AccountID }
	// users
	s.op("GET "+p+"/users", "iam:ListUsers", iamARN("user"), func(c *Ctx) error {
		l, err := s.Store.IAM.ListUsers(c.Context(), acct(c))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "path"))
	})
	s.op("POST "+p+"/users", "iam:CreateUser", iamARN("user"), func(c *Ctx) error {
		var in services.CreateUserInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		u, err := s.IAM.CreateUser(c.Context(), acct(c), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, u)
	})
	s.op("GET "+p+"/users/{name}", "iam:GetUser", iamARN("user"), func(c *Ctx) error {
		d, err := s.IAM.UserDetail(c.Context(), acct(c), c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(200, d)
	})
	s.op("DELETE "+p+"/users/{name}", "iam:DeleteUser", iamARN("user"), func(c *Ctx) error {
		if err := s.IAM.DeleteUser(c.Context(), acct(c), c.Path("name")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("GET "+p+"/users/{name}/access-keys", "iam:ListAccessKeys", iamARN("user"), func(c *Ctx) error {
		u, err := s.IAM.GetUser(c.Context(), acct(c), c.Path("name"))
		if err != nil {
			return err
		}
		keys, err := s.Store.IAM.ListAccessKeys(c.Context(), u.ID)
		if err != nil {
			return err
		}
		return c.JSON(200, map[string]any{"items": keys})
	})
	s.op("POST "+p+"/users/{name}/access-keys", "iam:CreateAccessKey", iamARN("user"), func(c *Ctx) error {
		k, err := s.IAM.CreateAccessKey(c.Context(), acct(c), c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, k)
	})
	s.op("PATCH "+p+"/users/{name}/access-keys/{key}", "iam:UpdateAccessKey", iamARN("user"), func(c *Ctx) error {
		var in struct {
			Status string `json:"status"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := s.IAM.SetAccessKeyStatus(c.Context(), acct(c), c.Path("name"), c.Path("key"), in.Status); err != nil {
			return err
		}
		return c.JSON(200, map[string]string{"access_key_id": c.Path("key"), "status": in.Status})
	})
	s.op("DELETE "+p+"/users/{name}/access-keys/{key}", "iam:DeleteAccessKey", iamARN("user"), func(c *Ctx) error {
		if err := s.IAM.DeleteAccessKey(c.Context(), acct(c), c.Path("name"), c.Path("key")); err != nil {
			return err
		}
		return c.NoContent()
	})
	// groups
	s.op("GET "+p+"/groups", "iam:ListGroups", iamARN("group"), func(c *Ctx) error {
		l, err := s.Store.IAM.ListGroups(c.Context(), acct(c))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R)))
	})
	s.op("POST "+p+"/groups", "iam:CreateGroup", iamARN("group"), func(c *Ctx) error {
		var in struct {
			GroupName string `json:"group_name"`
			Path      string `json:"path"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		g, err := s.IAM.CreateGroup(c.Context(), acct(c), in.GroupName, in.Path)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, g)
	})
	s.op("GET "+p+"/groups/{name}", "iam:GetGroup", iamARN("group"), func(c *Ctx) error {
		d, err := s.IAM.GroupDetail(c.Context(), acct(c), c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(200, d)
	})
	s.op("DELETE "+p+"/groups/{name}", "iam:DeleteGroup", iamARN("group"), func(c *Ctx) error {
		if err := s.IAM.DeleteGroup(c.Context(), acct(c), c.Path("name")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/groups/{name}/members/{user}", "iam:AddUserToGroup", iamARN("group"), func(c *Ctx) error {
		if err := s.IAM.Membership(c.Context(), acct(c), c.Path("name"), c.Path("user"), true); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("DELETE "+p+"/groups/{name}/members/{user}", "iam:RemoveUserFromGroup", iamARN("group"), func(c *Ctx) error {
		if err := s.IAM.Membership(c.Context(), acct(c), c.Path("name"), c.Path("user"), false); err != nil {
			return err
		}
		return c.NoContent()
	})
	// roles
	s.op("GET "+p+"/roles", "iam:ListRoles", iamARN("role"), func(c *Ctx) error {
		l, err := s.Store.IAM.ListRoles(c.Context(), acct(c))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R)))
	})
	s.op("POST "+p+"/roles", "iam:CreateRole", iamARN("role"), func(c *Ctx) error {
		var in services.CreateRoleInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		r, err := s.IAM.CreateRole(c.Context(), acct(c), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, r)
	})
	s.op("GET "+p+"/roles/{name}", "iam:GetRole", iamARN("role"), func(c *Ctx) error {
		d, err := s.IAM.RoleDetail(c.Context(), acct(c), c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(200, d)
	})
	s.op("DELETE "+p+"/roles/{name}", "iam:DeleteRole", iamARN("role"), func(c *Ctx) error {
		if err := s.IAM.DeleteRole(c.Context(), acct(c), c.Path("name")); err != nil {
			return err
		}
		return c.NoContent()
	})
	// managed policies
	s.op("GET "+p+"/policies", "iam:ListPolicies", iamARN("policy"), func(c *Ctx) error {
		l, err := s.Store.IAM.ListPolicies(c.Context(), acct(c))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "is_builtin"))
	})
	s.op("POST "+p+"/policies", "iam:CreatePolicy", iamARN("policy"), func(c *Ctx) error {
		var in services.CreatePolicyInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		pol, err := s.IAM.CreatePolicy(c.Context(), acct(c), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, pol)
	})
	s.op("GET "+p+"/policies/{name}", "iam:GetPolicy", iamARN("policy"), func(c *Ctx) error {
		pol, err := s.IAM.GetPolicy(c.Context(), acct(c), c.Path("name"))
		if err != nil {
			return err
		}
		return c.JSON(200, pol)
	})
	s.op("PUT "+p+"/policies/{name}", "iam:CreatePolicyVersion", iamARN("policy"), func(c *Ctx) error {
		doc, err := decodePolicyBody(c)
		if err != nil {
			return err
		}
		pol, err := s.IAM.UpdatePolicy(c.Context(), acct(c), c.Path("name"), doc)
		if err != nil {
			return err
		}
		return c.JSON(200, pol)
	})
	s.op("DELETE "+p+"/policies/{name}", "iam:DeletePolicy", iamARN("policy"), func(c *Ctx) error {
		if err := s.IAM.DeletePolicy(c.Context(), acct(c), c.Path("name")); err != nil {
			return err
		}
		return c.NoContent()
	})
	for _, kind := range []string{"users", "groups", "roles"} {
		kind := kind
		single := strings.TrimSuffix(kind, "s")
		title := strings.ToUpper(single[:1]) + single[1:]
		s.op("POST "+p+"/"+kind+"/{name}/attached-policies/{policy}", "iam:Attach"+title+"Policy", iamARN(single), func(c *Ctx) error {
			if err := s.IAM.AttachPolicy(c.Context(), acct(c), kind, c.Path("name"), c.Path("policy"), true); err != nil {
				return err
			}
			return c.NoContent()
		})
		s.op("DELETE "+p+"/"+kind+"/{name}/attached-policies/{policy}", "iam:Detach"+title+"Policy", iamARN(single), func(c *Ctx) error {
			if err := s.IAM.AttachPolicy(c.Context(), acct(c), kind, c.Path("name"), c.Path("policy"), false); err != nil {
				return err
			}
			return c.NoContent()
		})
		s.op("PUT "+p+"/"+kind+"/{name}/inline-policies/{policy}", "iam:Put"+title+"Policy", iamARN(single), func(c *Ctx) error {
			doc, err := decodePolicyBody(c)
			if err != nil {
				return err
			}
			if err := s.IAM.PutInline(c.Context(), acct(c), kind, c.Path("name"), c.Path("policy"), doc); err != nil {
				return err
			}
			return c.NoContent()
		})
		s.op("DELETE "+p+"/"+kind+"/{name}/inline-policies/{policy}", "iam:Delete"+title+"Policy", iamARN(single), func(c *Ctx) error {
			if err := s.IAM.DeleteInline(c.Context(), acct(c), kind, c.Path("name"), c.Path("policy")); err != nil {
				return err
			}
			return c.NoContent()
		})
	}
	s.op("POST "+p+"/simulate", "iam:SimulatePrincipalPolicy", nil, func(c *Ctx) error {
		var in services.SimulateInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		r, err := s.IAM.Simulate(c.Context(), acct(c), in)
		if err != nil {
			return err
		}
		return c.JSON(200, r)
	})
	s.op("POST "+p+"/validate-policy", "", nil, func(c *Ctx) error {
		var in struct {
			PolicyDocument json.RawMessage `json:"policy_document"`
			Type           string          `json:"type"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		kind := policy.Identity
		switch in.Type {
		case "resource":
			kind = policy.Resource
		case "trust":
			kind = policy.Trust
		}
		var str string
		doc := string(in.PolicyDocument)
		if json.Unmarshal(in.PolicyDocument, &str) == nil {
			doc = str
		}
		errs := policy.Validate(doc, kind)
		if errs == nil {
			errs = []string{}
		}
		return c.JSON(200, map[string]any{"valid": len(errs) == 0, "errors": errs})
	})
	// STS
	s.op("POST /api/v1/sts/assume-role", "", nil, func(c *Ctx) error {
		var in services.AssumeRoleInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := c.Authorize("sts:AssumeRole", in.RoleARN, nil, nil); err != nil {
			return err
		}
		out, err := s.IAM.AssumeRole(c.Context(), c.P, in)
		if err != nil {
			return err
		}
		return c.JSON(200, out)
	})
	s.op("GET /api/v1/sts/caller-identity", "", nil, func(c *Ctx) error {
		uid := c.P.Name
		return c.JSON(200, map[string]string{"account": c.P.AccountID, "arn": c.P.ARN, "user_id": uid, "type": c.P.Kind})
	})
}

func decodePolicyBody(c *Ctx) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.Decode(&raw); err != nil {
		return nil, err
	}
	// Accept either the bare document or {"policy_document": …}.
	var wrapper struct {
		PolicyDocument json.RawMessage `json:"policy_document"`
	}
	if json.Unmarshal(raw, &wrapper) == nil && len(wrapper.PolicyDocument) > 0 {
		return wrapper.PolicyDocument, nil
	}
	if len(raw) == 0 {
		return nil, apierr.Validation("policy document required")
	}
	return raw, nil
}
