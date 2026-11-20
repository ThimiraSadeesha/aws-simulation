package api

import (
	"net/http"
	"strconv"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/services"
)

func (s *Server) routesVPC() {
	const p = "/api/v1/vpc"
	acct := func(c *Ctx) string { return c.P.AccountID }
	// VPCs
	s.op("GET "+p+"/vpcs", "ec2:DescribeVpcs", ec2ARN("vpc"), func(c *Ctx) error {
		l, err := s.Net.ListVPCs(c.Context(), acct(c))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "is_default", "state", "cidr_block"))
	})
	s.op("POST "+p+"/vpcs", "ec2:CreateVpc", ec2ARN("vpc"), func(c *Ctx) error {
		var in services.CreateVPCInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		v, err := s.Net.CreateVPC(c.Context(), acct(c), in, false)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, v)
	})
	s.op("GET "+p+"/vpcs/{id}", "ec2:DescribeVpcs", ec2ARN("vpc"), func(c *Ctx) error {
		v, err := s.Net.GetVPC(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, v)
	})
	s.op("PATCH "+p+"/vpcs/{id}", "ec2:ModifyVpcAttribute", ec2ARN("vpc"), func(c *Ctx) error {
		var in services.UpdateVPCInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		v, err := s.Net.UpdateVPC(c.Context(), acct(c), c.Path("id"), in)
		if err != nil {
			return err
		}
		return c.JSON(200, v)
	})
	s.op("DELETE "+p+"/vpcs/{id}", "ec2:DeleteVpc", ec2ARN("vpc"), func(c *Ctx) error {
		if err := s.Net.DeleteVPC(c.Context(), acct(c), c.Path("id")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("GET "+p+"/vpcs/{id}/topology", "ec2:DescribeVpcs", ec2ARN("vpc"), func(c *Ctx) error {
		t, err := s.Net.VPCTopology(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, t)
	})
	// Subnets
	s.op("GET "+p+"/subnets", "ec2:DescribeSubnets", ec2ARN("subnet"), func(c *Ctx) error {
		l, err := s.Net.ListSubnets(c.Context(), acct(c), c.Query("vpc_id"))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "availability_zone", "is_public", "map_public_ip_on_launch"))
	})
	s.op("POST "+p+"/subnets", "ec2:CreateSubnet", ec2ARN("subnet"), func(c *Ctx) error {
		var in services.CreateSubnetInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		sn, err := s.Net.CreateSubnet(c.Context(), acct(c), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, sn)
	})
	s.op("GET "+p+"/subnets/{id}", "ec2:DescribeSubnets", ec2ARN("subnet"), func(c *Ctx) error {
		sn, err := s.Net.GetSubnet(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, sn)
	})
	s.op("PATCH "+p+"/subnets/{id}", "ec2:ModifySubnetAttribute", ec2ARN("subnet"), func(c *Ctx) error {
		var in services.UpdateSubnetInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		sn, err := s.Net.UpdateSubnet(c.Context(), acct(c), c.Path("id"), in)
		if err != nil {
			return err
		}
		return c.JSON(200, sn)
	})
	s.op("DELETE "+p+"/subnets/{id}", "ec2:DeleteSubnet", ec2ARN("subnet"), func(c *Ctx) error {
		if err := s.Net.DeleteSubnet(c.Context(), acct(c), c.Path("id")); err != nil {
			return err
		}
		return c.NoContent()
	})
	// Route tables
	s.op("GET "+p+"/route-tables", "ec2:DescribeRouteTables", ec2ARN("route-table"), func(c *Ctx) error {
		l, err := s.Net.ListRouteTables(c.Context(), acct(c), c.Query("vpc_id"))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "is_main"))
	})
	s.op("POST "+p+"/route-tables", "ec2:CreateRouteTable", ec2ARN("route-table"), func(c *Ctx) error {
		var in struct {
			VPCID string `json:"vpc_id"`
			Name  string `json:"name"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		rt, err := s.Net.CreateRouteTable(c.Context(), acct(c), in.VPCID, in.Name)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, rt)
	})
	s.op("GET "+p+"/route-tables/{id}", "ec2:DescribeRouteTables", ec2ARN("route-table"), func(c *Ctx) error {
		rt, err := s.Net.GetRouteTable(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, rt)
	})
	s.op("DELETE "+p+"/route-tables/{id}", "ec2:DeleteRouteTable", ec2ARN("route-table"), func(c *Ctx) error {
		if err := s.Net.DeleteRouteTable(c.Context(), acct(c), c.Path("id")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/route-tables/{id}/routes", "ec2:CreateRoute", ec2ARN("route-table"), func(c *Ctx) error {
		var in struct {
			DestinationCIDR string `json:"destination_cidr"`
			TargetID        string `json:"target_id"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := s.Net.AddRoute(c.Context(), acct(c), c.Path("id"), in.DestinationCIDR, in.TargetID); err != nil {
			return err
		}
		rt, err := s.Net.GetRouteTable(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, rt)
	})
	s.op("DELETE "+p+"/route-tables/{id}/routes", "ec2:DeleteRoute", ec2ARN("route-table"), func(c *Ctx) error {
		if err := s.Net.DeleteRoute(c.Context(), acct(c), c.Path("id"), c.Query("destination_cidr")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/route-tables/{id}/associations", "ec2:AssociateRouteTable", ec2ARN("route-table"), func(c *Ctx) error {
		var in struct {
			SubnetID string `json:"subnet_id"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := s.Net.AssociateRouteTable(c.Context(), acct(c), c.Path("id"), in.SubnetID); err != nil {
			return err
		}
		rt, err := s.Net.GetRouteTable(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, rt)
	})
	// Internet gateways
	s.op("GET "+p+"/internet-gateways", "ec2:DescribeInternetGateways", ec2ARN("internet-gateway"), func(c *Ctx) error {
		l, err := s.Net.ListIGWs(c.Context(), acct(c))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "state", "vpc_id"))
	})
	s.op("POST "+p+"/internet-gateways", "ec2:CreateInternetGateway", ec2ARN("internet-gateway"), func(c *Ctx) error {
		var in struct {
			Name string            `json:"name"`
			Tags map[string]string `json:"tags"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		g, err := s.Net.CreateIGW(c.Context(), acct(c), in.Name, in.Tags)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, g)
	})
	s.op("GET "+p+"/internet-gateways/{id}", "ec2:DescribeInternetGateways", ec2ARN("internet-gateway"), func(c *Ctx) error {
		g, err := s.Net.GetIGW(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, g)
	})
	s.op("DELETE "+p+"/internet-gateways/{id}", "ec2:DeleteInternetGateway", ec2ARN("internet-gateway"), func(c *Ctx) error {
		if err := s.Net.DeleteIGW(c.Context(), acct(c), c.Path("id")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/internet-gateways/{id}/attach", "ec2:AttachInternetGateway", ec2ARN("internet-gateway"), func(c *Ctx) error {
		var in struct {
			VPCID string `json:"vpc_id"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := s.Net.AttachIGW(c.Context(), acct(c), c.Path("id"), in.VPCID); err != nil {
			return err
		}
		g, err := s.Net.GetIGW(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, g)
	})
	s.op("POST "+p+"/internet-gateways/{id}/detach", "ec2:DetachInternetGateway", ec2ARN("internet-gateway"), func(c *Ctx) error {
		if err := s.Net.DetachIGW(c.Context(), acct(c), c.Path("id")); err != nil {
			return err
		}
		g, err := s.Net.GetIGW(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, g)
	})
	// NAT gateways
	s.op("GET "+p+"/nat-gateways", "ec2:DescribeNatGateways", ec2ARN("natgateway"), func(c *Ctx) error {
		l, err := s.Net.ListNATs(c.Context(), acct(c))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "state", "vpc_id", "subnet_id"))
	})
	s.op("POST "+p+"/nat-gateways", "ec2:CreateNatGateway", ec2ARN("natgateway"), func(c *Ctx) error {
		var in struct {
			SubnetID string `json:"subnet_id"`
			Name     string `json:"name"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		n, err := s.Net.CreateNAT(c.Context(), acct(c), in.SubnetID, in.Name)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, n)
	})
	s.op("GET "+p+"/nat-gateways/{id}", "ec2:DescribeNatGateways", ec2ARN("natgateway"), func(c *Ctx) error {
		n, err := s.Net.GetNAT(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, n)
	})
	s.op("DELETE "+p+"/nat-gateways/{id}", "ec2:DeleteNatGateway", ec2ARN("natgateway"), func(c *Ctx) error {
		n, err := s.Net.DeleteNAT(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(http.StatusAccepted, n)
	})
	// Security groups
	s.op("GET "+p+"/security-groups", "ec2:DescribeSecurityGroups", ec2ARN("security-group"), func(c *Ctx) error {
		l, err := s.Net.ListSGs(c.Context(), acct(c), c.Query("vpc_id"))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "group_name", "is_default"))
	})
	s.op("POST "+p+"/security-groups", "ec2:CreateSecurityGroup", ec2ARN("security-group"), func(c *Ctx) error {
		var in services.CreateSGInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		g, err := s.Net.CreateSG(c.Context(), acct(c), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, g)
	})
	s.op("GET "+p+"/security-groups/{id}", "ec2:DescribeSecurityGroups", ec2ARN("security-group"), func(c *Ctx) error {
		g, err := s.Net.GetSG(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, g)
	})
	s.op("DELETE "+p+"/security-groups/{id}", "ec2:DeleteSecurityGroup", ec2ARN("security-group"), func(c *Ctx) error {
		if err := s.Net.DeleteSG(c.Context(), acct(c), c.Path("id")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/security-groups/{id}/rules", "ec2:AuthorizeSecurityGroupIngress", ec2ARN("security-group"), func(c *Ctx) error {
		var in services.SGRuleInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		if in.Direction == "egress" {
			if err := c.Authorize("ec2:AuthorizeSecurityGroupEgress", ec2ARN("security-group")(c), nil, nil); err != nil {
				return err
			}
		}
		g, err := s.Net.AddSGRule(c.Context(), acct(c), c.Path("id"), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, g)
	})
	s.op("DELETE "+p+"/security-groups/{id}/rules/{rule}", "ec2:RevokeSecurityGroupIngress", ec2ARN("security-group"), func(c *Ctx) error {
		if err := s.Net.DeleteSGRule(c.Context(), acct(c), c.Path("id"), c.Path("rule")); err != nil {
			return err
		}
		return c.NoContent()
	})
	// Network ACLs
	s.op("GET "+p+"/network-acls", "ec2:DescribeNetworkAcls", ec2ARN("network-acl"), func(c *Ctx) error {
		l, err := s.Net.ListACLs(c.Context(), acct(c), c.Query("vpc_id"))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "is_default"))
	})
	s.op("POST "+p+"/network-acls", "ec2:CreateNetworkAcl", ec2ARN("network-acl"), func(c *Ctx) error {
		var in struct {
			VPCID string `json:"vpc_id"`
			Name  string `json:"name"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		a, err := s.Net.CreateACL(c.Context(), acct(c), in.VPCID, in.Name)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, a)
	})
	s.op("GET "+p+"/network-acls/{id}", "ec2:DescribeNetworkAcls", ec2ARN("network-acl"), func(c *Ctx) error {
		a, err := s.Net.GetACL(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, a)
	})
	s.op("DELETE "+p+"/network-acls/{id}", "ec2:DeleteNetworkAcl", ec2ARN("network-acl"), func(c *Ctx) error {
		if err := s.Net.DeleteACL(c.Context(), acct(c), c.Path("id")); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/network-acls/{id}/entries", "ec2:CreateNetworkAclEntry", ec2ARN("network-acl"), func(c *Ctx) error {
		var in repositories.ACLEntry
		if err := c.Decode(&in); err != nil {
			return err
		}
		a, err := s.Net.PutACLEntry(c.Context(), acct(c), c.Path("id"), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, a)
	})
	s.op("DELETE "+p+"/network-acls/{id}/entries", "ec2:DeleteNetworkAclEntry", ec2ARN("network-acl"), func(c *Ctx) error {
		n, err := strconv.Atoi(c.Query("rule_number"))
		if err != nil {
			return apierr.Validation("rule_number query parameter is required")
		}
		if err := s.Net.DeleteACLEntry(c.Context(), acct(c), c.Path("id"), n, c.Query("egress") == "true"); err != nil {
			return err
		}
		return c.NoContent()
	})
	s.op("POST "+p+"/network-acls/{id}/associations", "ec2:ReplaceNetworkAclAssociation", ec2ARN("network-acl"), func(c *Ctx) error {
		var in struct {
			SubnetID string `json:"subnet_id"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := s.Net.AssociateACL(c.Context(), acct(c), c.Path("id"), in.SubnetID); err != nil {
			return err
		}
		a, err := s.Net.GetACL(c.Context(), acct(c), c.Path("id"))
		if err != nil {
			return err
		}
		return c.JSON(200, a)
	})
	// ENIs
	s.op("GET "+p+"/network-interfaces", "ec2:DescribeNetworkInterfaces", ec2ARN("network-interface"), func(c *Ctx) error {
		l, err := s.Net.ListENIs(c.Context(), acct(c))
		if err != nil {
			return err
		}
		return c.JSON(200, paginate(l, parseList(c.R), "status", "subnet_id", "vpc_id"))
	})
	s.op("POST "+p+"/network-interfaces", "ec2:CreateNetworkInterface", ec2ARN("network-interface"), func(c *Ctx) error {
		var in services.CreateENIInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		e, err := s.Net.CreateENI(c.Context(), acct(c), in)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, e)
	})
	s.op("DELETE "+p+"/network-interfaces/{id}", "ec2:DeleteNetworkInterface", ec2ARN("network-interface"), func(c *Ctx) error {
		if err := s.Net.DeleteENI(c.Context(), acct(c), c.Path("id")); err != nil {
			return err
		}
		return c.NoContent()
	})
	// DNS & reachability
	s.op("GET "+p+"/dns/resolve", "route53resolver:Resolve", nil, func(c *Ctx) error {
		r, err := s.Net.ResolveDNS(c.Context(), acct(c), c.Query("name"), c.Query("vpc_id"))
		if err != nil {
			return err
		}
		return c.JSON(200, r)
	})
	s.op("POST "+p+"/reachability", "ec2:StartNetworkInsightsAnalysis", nil, func(c *Ctx) error {
		var in services.ReachabilityInput
		if err := c.Decode(&in); err != nil {
			return err
		}
		r, err := s.Net.Reachability(c.Context(), acct(c), in)
		if err != nil {
			return err
		}
		return c.JSON(200, r)
	})
}
