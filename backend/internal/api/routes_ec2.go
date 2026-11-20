package api

import (
	"net/http"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/services"
)

func ec2ARN(kind string) ArnFn {
	return func(c *Ctx) string {
		id := c.Path("id")
		if id == "" {
			id = c.Path("name")
		}
		if id == "" {
			id = "*"
		}
		return auth.ARN("ec2", c.P.AccountID, kind+"/"+id)
	}
}

func (s *Server) routesEC2() {
	const p = "/api/v1/ec2"
	s.op("GET "+p+"/instances", "ec2:DescribeInstances", ec2ARN("instance"), func(c *Ctx) error {
		l, err := s.EC2.List(c.Context(), c.P.AccountID, repositories.InstanceFilter{State: c.Query("state")})
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "state", "instance_type", "vpc_id", "subnet_id", "image_id", "autoscaling_group"))
	})
	s.op("POST "+p+"/instances", "ec2:RunInstances", ec2ARN("instance"), func(c *Ctx) error {
		var in services.RunInstancesInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		insts, err := s.EC2.RunInstances(c.Context(), c.P.AccountID, in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, map[string]any{"instances": insts})
	})
	s.op("GET "+p+"/instances/{id}", "ec2:DescribeInstances", ec2ARN("instance"), func(c *Ctx) error {
		i, err := s.EC2.Get(c.Context(), c.P.AccountID, c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, i)
	})
	for action, iamAction := range map[string]string{"start": "ec2:StartInstances", "stop": "ec2:StopInstances", "reboot": "ec2:RebootInstances", "terminate": "ec2:TerminateInstances"} {
		action := action
		s.op("POST "+p+"/instances/{id}/"+action, iamAction, ec2ARN("instance"), func(c *Ctx) error {
			i, err := s.EC2.Action(c.Context(), c.P.AccountID, c.Path("id"), action, "")
			if err != nil {
				return err
			}
			return c.JSON(http.StatusAccepted, i)
		})
	}
	s.op("PUT "+p+"/instances/{id}/tags", "ec2:CreateTags", ec2ARN("instance"), func(c *Ctx) error {
		var in struct {
			Tags map[string]string `json:"tags"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		i, err := s.EC2.SetTags(c.Context(), c.P.AccountID, c.Path("id"), in.Tags)
		if err != nil {
			return err
		}
		return c.JSON(200, i)
	})
	s.op("PUT "+p+"/instances/{id}/workload", "ec2:ModifyInstanceAttribute", ec2ARN("instance"), func(c *Ctx) error {
		var in services.WorkloadInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		i, err := s.EC2.SetWorkload(c.Context(), c.P.AccountID, c.Path("id"), in)
		if err != nil {
			return err
		}
		return c.JSON(200, i)
	})
	s.op("PUT "+p+"/instances/{id}/security-groups", "ec2:ModifyInstanceAttribute", ec2ARN("instance"), func(c *Ctx) error {
		var in struct {
			SecurityGroupIDs []string `json:"security_group_ids"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		i, err := s.EC2.SetSecurityGroups(c.Context(), c.P.AccountID, c.Path("id"), in.SecurityGroupIDs)
		if err != nil {
			return err
		}
		return c.JSON(200, i)
	})
	s.op("GET "+p+"/instances/{id}/console-output", "ec2:GetConsoleOutput", ec2ARN("instance"), func(c *Ctx) error {
		o, err := s.EC2.ConsoleOutput(c.Context(), c.P.AccountID, c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, o)
	})
	s.op("GET "+p+"/instances/{id}/metadata", "ec2:DescribeInstances", ec2ARN("instance"), func(c *Ctx) error {
		o, err := s.EC2.Metadata(c.Context(), c.P.AccountID, c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, o)
	})
	s.op("GET "+p+"/instances/{id}/history", "ec2:DescribeInstances", ec2ARN("instance"), func(c *Ctx) error {
		o, err := s.EC2.History(c.Context(), c.P.AccountID, c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, map[string]any{"items": o})
	})
	s.op("GET "+p+"/instance-types", "", nil, func(c *Ctx) error {
		return c.JSON(200, services.InstanceTypes)
	})
	s.op("GET "+p+"/images", "ec2:DescribeImages", ec2ARN("image"), func(c *Ctx) error {
		return c.JSON(200, services.Images)
	})
	s.op("GET "+p+"/key-pairs", "ec2:DescribeKeyPairs", ec2ARN("key-pair"), func(c *Ctx) error {
		l, err := s.Store.EC2.ListKeyPairs(c.Context(), c.P.AccountID)
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R)))
	})
	s.op("POST "+p+"/key-pairs", "ec2:CreateKeyPair", ec2ARN("key-pair"), func(c *Ctx) error {
		var in struct {
			Name string `json:"name"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		k, err := s.EC2.CreateKeyPair(c.Context(), c.P.AccountID, in.Name)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, k)
	})
	s.op("DELETE "+p+"/key-pairs/{name}", "ec2:DeleteKeyPair", ec2ARN("key-pair"), func(c *Ctx) error {
		if err := s.EC2.DeleteKeyPair(c.Context(), c.P.AccountID, c.Path("name")); err != nil {
			return err
		}
		return c.NoContent()
	})
	_ = apierr.Validation
}
