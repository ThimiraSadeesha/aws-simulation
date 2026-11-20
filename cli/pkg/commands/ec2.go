package commands

import (
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
)

var filtersFlag = Flag{Name: "filters", Kind: List, Help: "Name=<filter>,Values=<v1>,<v2> (e.g. Name=tag:Name,Values=web*)"}

// listCmd builds a describe-* command over a list endpoint with id + filter support.
func listCmd(name, summary, path, idFlag, idField string, cols []string, extra ...Flag) *Command {
	flags := []Flag{filtersFlag}
	if idFlag != "" {
		flags = append(flags, Flag{Name: idFlag, Kind: List, Help: "only these ids"})
	}
	flags = append(flags, extra...)
	return &Command{Name: name, Summary: summary, Flags: flags, Columns: cols, Run: func(x *Exec) (any, error) {
		fs, err := ParseFilters(x.Flags["filters"])
		if err != nil {
			return nil, err
		}
		q := url.Values{}
		for _, f := range extra {
			if v := x.Str(f.Name); v != "" && f.Kind != Bool {
				q.Set(strings.ReplaceAll(f.Name, "-", "_"), v)
			}
		}
		items, err := x.ListAll(path, q)
		if err != nil {
			return nil, err
		}
		if idFlag != "" {
			items = filterIDs(items, idField, x.List(idFlag))
		}
		items = applyFilters(items, fs)
		if items == nil {
			items = []any{}
		}
		return map[string]any{"items": items}, nil
	}}
}

// nameAndTags combines --name, --tags and --tag-specifications.
func nameAndTags(x *Exec) (string, map[string]string, error) {
	tags, err := ParseTags(append(append([]string{}, x.Flags["tag-specifications"]...), x.Flags["tags"]...))
	if err != nil {
		return "", nil, err
	}
	name := x.Str("name")
	if name == "" {
		name = tags["Name"]
	}
	if name != "" {
		tags["Name"] = name
	}
	return name, tags, nil
}

var tagFlags = []Flag{
	{Name: "name", Help: "Name tag (CloudSim shortcut)"},
	{Name: "tag-specifications", Kind: List, Help: "ResourceType=<t>,Tags=[{Key=k,Value=v}]"},
	{Name: "tags", Kind: List, Help: "Key=<k>,Value=<v> ..."},
}

func withTags(flags ...Flag) []Flag { return append(flags, tagFlags...) }

func simple(name, summary, method string, path func(x *Exec) string, body func(x *Exec) (any, error), flags ...Flag) *Command {
	return &Command{Name: name, Summary: summary, Flags: flags, Run: func(x *Exec) (any, error) {
		var b any
		if body != nil {
			var err error
			if b, err = body(x); err != nil {
				return nil, err
			}
		}
		return x.Call(method, path(x), b)
	}}
}

func req(name, help string) Flag { return Flag{Name: name, Required: true, Help: help} }
func opt(name, help string) Flag { return Flag{Name: name, Help: help} }

// parsePorts parses "22", "80-90", "-1"/"all".
func parsePorts(p string) (int, int, error) {
	switch p {
	case "", "-1", "all":
		return 0, 65535, nil
	}
	a, b, found := strings.Cut(p, "-")
	from, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, usagef("invalid --port %q", p)
	}
	to := from
	if found {
		if to, err = strconv.Atoi(b); err != nil {
			return 0, 0, usagef("invalid --port %q", p)
		}
	}
	return from, to, nil
}

func sgRule(direction string) func(x *Exec) (any, error) {
	return func(x *Exec) (any, error) {
		proto := x.Str("protocol")
		if proto == "" {
			proto = "tcp"
		}
		if proto == "all" {
			proto = "-1"
		}
		from, to, err := parsePorts(x.Str("port"))
		if err != nil {
			return nil, err
		}
		if proto == "-1" {
			from, to = 0, 65535
		}
		if x.Str("cidr") == "" && x.Str("source-group") == "" {
			return nil, usagef("one of --cidr or --source-group is required")
		}
		body := map[string]any{"direction": direction, "protocol": proto, "from_port": from, "to_port": to,
			"cidr": x.Str("cidr"), "source_security_group_id": x.Str("source-group"), "description": x.Str("description")}
		return x.Call("POST", "/vpc/security-groups/"+esc(x.Str("group-id"))+"/rules", body)
	}
}

func instanceAction(name, action string) *Command {
	return &Command{Name: name, Summary: strings.ToUpper(action[:1]) + action[1:] + " one or more instances",
		Flags: []Flag{{Name: "instance-ids", Kind: List, Required: true, Help: "instance ids"}}, Columns: []string{"instance_id", "state", "instance_type", "name"},
		Run: func(x *Exec) (any, error) {
			var out []any
			for _, id := range x.List("instance-ids") {
				r, err := x.Call("POST", "/ec2/instances/"+esc(id)+"/"+action, nil)
				if err != nil {
					return nil, err
				}
				out = append(out, r)
			}
			return map[string]any{"instances": out}, nil
		}}
}

func init() {
	instCols := []string{"instance_id", "name", "state", "instance_type", "private_ip", "public_ip", "subnet_id"}
	register("ec2", "simulated compute and networking (instances, AMIs, key pairs, VPCs, subnets, security groups, gateways)",
		listCmd("describe-instances", "Describe simulated instances", "/ec2/instances", "instance-ids", "instance_id", instCols),
		&Command{Name: "run-instances", Summary: "Launch simulated instances",
			Flags: withTags(req("image-id", "AMI id (see describe-images)"), opt("instance-type", "instance type (default t3.micro)"),
				Flag{Name: "count", Kind: Int, Help: "number of instances (default 1)"}, opt("subnet-id", "subnet (default VPC when omitted)"),
				Flag{Name: "security-group-ids", Kind: List, Help: "security group ids"}, opt("key-name", "key pair name"),
				opt("user-data", "user-data script or file://path (parsed and echoed, never executed)"),
				Flag{Name: "associate-public-ip-address", Kind: Bool, Help: "assign a public IP"}, opt("workload-profile", "idle|steady|spiky|stress|custom")),
			Columns:  instCols,
			Examples: []string{"cloudsim ec2 run-instances --image-id ami-0c1a2b3c4d5e6f701 --instance-type t3.micro --name web"},
			Run: func(x *Exec) (any, error) {
				name, tags, err := nameAndTags(x)
				if err != nil {
					return nil, err
				}
				ud, err := x.Value("user-data")
				if err != nil {
					return nil, err
				}
				if dec, err := base64.StdEncoding.DecodeString(ud); err == nil && len(ud) > 8 && !strings.Contains(ud, " ") && strings.HasPrefix(string(dec), "#!") {
					ud = string(dec)
				}
				it := x.Str("instance-type")
				if it == "" {
					it = "t3.micro"
				}
				body := map[string]any{"image_id": x.Str("image-id"), "instance_type": it, "count": x.Int("count", 1), "name": name,
					"key_name": x.Str("key-name"), "subnet_id": x.Str("subnet-id"), "security_group_ids": x.List("security-group-ids"),
					"user_data": ud, "tags": tags, "workload_profile": x.Str("workload-profile")}
				if b, ok := x.Bool("associate-public-ip-address"); ok {
					body["associate_public_ip"] = b
				}
				return x.Call("POST", "/ec2/instances", body)
			}},
		instanceAction("start-instances", "start"), instanceAction("stop-instances", "stop"),
		instanceAction("reboot-instances", "reboot"), instanceAction("terminate-instances", "terminate"),
		listCmd("describe-images", "List available simulated machine images", "/ec2/images", "image-ids", "image_id", []string{"image_id", "name", "os", "architecture"}),
		listCmd("describe-instance-types", "List instance types", "/ec2/instance-types", "instance-types", "name", []string{"name", "vcpu", "memory_mib", "family", "hourly_price_sim"}),
		simple("create-key-pair", "Create a key pair (the private key is shown once)", "POST", func(x *Exec) string { return "/ec2/key-pairs" },
			func(x *Exec) (any, error) { return map[string]any{"name": x.Str("key-name")}, nil }, req("key-name", "key pair name")),
		listCmd("describe-key-pairs", "List key pairs", "/ec2/key-pairs", "key-names", "name", []string{"key_pair_id", "name", "fingerprint", "created_at"}),
		&Command{Name: "delete-key-pair", Summary: "Delete a key pair", Flags: []Flag{req("key-name", "key pair name")}, Run: func(x *Exec) (any, error) {
			_, err := x.Call("DELETE", "/ec2/key-pairs/"+esc(x.Str("key-name")), nil)
			return nil, err
		}},
		&Command{Name: "get-console-output", Summary: "Show the simulated boot log of an instance", Flags: []Flag{req("instance-id", "instance id")},
			Run: func(x *Exec) (any, error) {
				r, err := x.Call("GET", "/ec2/instances/"+esc(x.Str("instance-id"))+"/console-output", nil)
				if err != nil {
					return nil, err
				}
				if x.Globals.Output != "json" && x.Globals.Query == "" {
					m, _ := r.(map[string]any)
					return scalarString(m["output"]), nil
				}
				return r, nil
			}},
		&Command{Name: "create-tags", Summary: "Add or overwrite tags on instances", Flags: []Flag{{Name: "resources", Kind: List, Required: true, Help: "instance ids"}, {Name: "tags", Kind: List, Required: true, Help: "Key=k,Value=v"}},
			Run: func(x *Exec) (any, error) {
				add, err := ParseTags(x.Flags["tags"])
				if err != nil {
					return nil, err
				}
				for _, id := range x.List("resources") {
					if !strings.HasPrefix(id, "i-") {
						return nil, usagef("create-tags currently supports instances only (got %s); other resources take tags at creation", id)
					}
					cur, err := x.Call("GET", "/ec2/instances/"+esc(id), nil)
					if err != nil {
						return nil, err
					}
					tags := map[string]string{}
					if m, ok := cur.(map[string]any); ok {
						if t, ok := m["tags"].(map[string]any); ok {
							for k, v := range t {
								tags[k] = scalarString(v)
							}
						}
					}
					for k, v := range add {
						tags[k] = v
					}
					if _, err := x.Call("PUT", "/ec2/instances/"+esc(id)+"/tags", map[string]any{"tags": tags}); err != nil {
						return nil, err
					}
				}
				return nil, nil
			}},
		simple("modify-instance-workload", "Set the simulated workload profile of an instance (CloudSim extension)", "PUT",
			func(x *Exec) string { return "/ec2/instances/" + esc(x.Str("instance-id")) + "/workload" },
			func(x *Exec) (any, error) {
				b := map[string]any{"profile": x.Str("profile")}
				if x.Has("cpu-target") {
					b["cpu_target"] = x.Float("cpu-target", 0)
				}
				return b, nil
			}, req("instance-id", "instance id"), req("profile", "idle|steady|spiky|stress|custom"), Flag{Name: "cpu-target", Kind: Float, Help: "CPU % for custom"}),
		simple("modify-instance-security-groups", "Replace the security groups of an instance", "PUT",
			func(x *Exec) string { return "/ec2/instances/" + esc(x.Str("instance-id")) + "/security-groups" },
			func(x *Exec) (any, error) { return map[string]any{"security_group_ids": x.List("groups")}, nil },
			req("instance-id", "instance id"), Flag{Name: "groups", Kind: List, Required: true, Help: "security group ids"}),
		// networking
		listCmd("describe-vpcs", "Describe VPCs", "/vpc/vpcs", "vpc-ids", "vpc_id", []string{"vpc_id", "name", "cidr_block", "is_default", "state"}),
		&Command{Name: "create-vpc", Summary: "Create a VPC", Flags: withTags(req("cidr-block", "IPv4 CIDR, /16 to /28")), Columns: []string{"vpc_id", "name", "cidr_block", "state"},
			Run: func(x *Exec) (any, error) {
				name, tags, err := nameAndTags(x)
				if err != nil {
					return nil, err
				}
				return x.Call("POST", "/vpc/vpcs", map[string]any{"cidr_block": x.Str("cidr-block"), "name": name, "tags": tags})
			}},
		deleteCmd("delete-vpc", "Delete a VPC", "vpc-id", "/vpc/vpcs/"),
		&Command{Name: "modify-vpc-attribute", Summary: "Modify VPC DNS attributes", Flags: []Flag{req("vpc-id", "VPC id"), {Name: "enable-dns-support", Kind: Bool}, {Name: "enable-dns-hostnames", Kind: Bool}},
			Run: func(x *Exec) (any, error) {
				b := map[string]any{}
				if v, ok := x.Bool("enable-dns-support"); ok {
					b["enable_dns_support"] = v
				}
				if v, ok := x.Bool("enable-dns-hostnames"); ok {
					b["enable_dns_hostnames"] = v
				}
				return x.Call("PATCH", "/vpc/vpcs/"+esc(x.Str("vpc-id")), b)
			}},
		listCmd("describe-subnets", "Describe subnets", "/vpc/subnets", "subnet-ids", "subnet_id", []string{"subnet_id", "name", "vpc_id", "cidr_block", "availability_zone", "is_public"}),
		&Command{Name: "create-subnet", Summary: "Create a subnet", Columns: []string{"subnet_id", "name", "vpc_id", "cidr_block", "availability_zone"},
			Flags: withTags(req("vpc-id", "VPC id"), req("cidr-block", "subnet CIDR inside the VPC"), opt("availability-zone", "local-1a|local-1b|local-1c"),
				Flag{Name: "map-public-ip-on-launch", Kind: Bool, Help: "auto-assign public IPs"}),
			Run: func(x *Exec) (any, error) {
				name, tags, err := nameAndTags(x)
				if err != nil {
					return nil, err
				}
				mp, _ := x.Bool("map-public-ip-on-launch")
				return x.Call("POST", "/vpc/subnets", map[string]any{"vpc_id": x.Str("vpc-id"), "cidr_block": x.Str("cidr-block"), "name": name,
					"availability_zone": x.Str("availability-zone"), "map_public_ip_on_launch": mp, "tags": tags})
			}},
		deleteCmd("delete-subnet", "Delete a subnet", "subnet-id", "/vpc/subnets/"),
		&Command{Name: "modify-subnet-attribute", Summary: "Modify subnet attributes", Flags: []Flag{req("subnet-id", "subnet id"), {Name: "map-public-ip-on-launch", Kind: Bool, Required: true}},
			Run: func(x *Exec) (any, error) {
				v, _ := x.Bool("map-public-ip-on-launch")
				return x.Call("PATCH", "/vpc/subnets/"+esc(x.Str("subnet-id")), map[string]any{"map_public_ip_on_launch": v})
			}},
		listCmd("describe-security-groups", "Describe security groups", "/vpc/security-groups", "group-ids", "group_id", []string{"group_id", "group_name", "vpc_id", "description"}),
		&Command{Name: "create-security-group", Summary: "Create a security group", Flags: append([]Flag{req("group-name", "name"), req("description", "description"), opt("vpc-id", "VPC id")}, tagFlags[1:]...),
			Run: func(x *Exec) (any, error) {
				_, tags, err := nameAndTags(x)
				if err != nil {
					return nil, err
				}
				return x.Call("POST", "/vpc/security-groups", map[string]any{"group_name": x.Str("group-name"), "description": x.Str("description"), "vpc_id": x.Str("vpc-id"), "tags": tags})
			}},
		deleteCmd("delete-security-group", "Delete a security group", "group-id", "/vpc/security-groups/"),
		&Command{Name: "authorize-security-group-ingress", Summary: "Add an inbound rule", Run: sgRule("ingress"), Flags: sgFlags(),
			Examples: []string{"cloudsim ec2 authorize-security-group-ingress --group-id sg-... --protocol tcp --port 22 --cidr 203.0.113.0/24"}},
		&Command{Name: "authorize-security-group-egress", Summary: "Add an outbound rule", Run: sgRule("egress"), Flags: sgFlags()},
		&Command{Name: "revoke-security-group-rule", Summary: "Remove a rule by id (see describe-security-groups)", Flags: []Flag{req("group-id", "group id"), req("rule-id", "rule id")},
			Run: func(x *Exec) (any, error) {
				_, err := x.Call("DELETE", "/vpc/security-groups/"+esc(x.Str("group-id"))+"/rules/"+esc(x.Str("rule-id")), nil)
				return nil, err
			}},
		&Command{Name: "create-internet-gateway", Summary: "Create an internet gateway", Flags: tagFlags, Run: func(x *Exec) (any, error) {
			name, tags, err := nameAndTags(x)
			if err != nil {
				return nil, err
			}
			return x.Call("POST", "/vpc/internet-gateways", map[string]any{"name": name, "tags": tags})
		}},
		listCmd("describe-internet-gateways", "Describe internet gateways", "/vpc/internet-gateways", "internet-gateway-ids", "internet_gateway_id", []string{"internet_gateway_id", "name", "vpc_id", "state"}),
		simple("attach-internet-gateway", "Attach an internet gateway to a VPC", "POST", func(x *Exec) string { return "/vpc/internet-gateways/" + esc(x.Str("internet-gateway-id")) + "/attach" },
			func(x *Exec) (any, error) { return map[string]any{"vpc_id": x.Str("vpc-id")}, nil }, req("internet-gateway-id", "igw id"), req("vpc-id", "VPC id")),
		simple("detach-internet-gateway", "Detach an internet gateway", "POST", func(x *Exec) string { return "/vpc/internet-gateways/" + esc(x.Str("internet-gateway-id")) + "/detach" },
			nil, req("internet-gateway-id", "igw id"), opt("vpc-id", "VPC id (ignored)")),
		deleteCmd("delete-internet-gateway", "Delete an internet gateway", "internet-gateway-id", "/vpc/internet-gateways/"),
		&Command{Name: "create-route-table", Summary: "Create a route table", Flags: withTags(req("vpc-id", "VPC id")), Run: func(x *Exec) (any, error) {
			name, tags, err := nameAndTags(x)
			if err != nil {
				return nil, err
			}
			return x.Call("POST", "/vpc/route-tables", map[string]any{"vpc_id": x.Str("vpc-id"), "name": name, "tags": tags})
		}},
		listCmd("describe-route-tables", "Describe route tables", "/vpc/route-tables", "route-table-ids", "route_table_id", []string{"route_table_id", "name", "vpc_id", "is_main", "routes.destination_cidr", "routes.target_id", "subnet_ids"}),
		&Command{Name: "create-route", Summary: "Add a route", Flags: []Flag{req("route-table-id", "route table id"), req("destination-cidr-block", "destination CIDR"),
			opt("gateway-id", "igw-... target"), opt("nat-gateway-id", "nat-... target")},
			Run: func(x *Exec) (any, error) {
				target := x.Str("gateway-id")
				if target == "" {
					target = x.Str("nat-gateway-id")
				}
				if target == "" {
					return nil, usagef("one of --gateway-id or --nat-gateway-id is required")
				}
				return x.Call("POST", "/vpc/route-tables/"+esc(x.Str("route-table-id"))+"/routes", map[string]any{"destination_cidr": x.Str("destination-cidr-block"), "target_id": target})
			}},
		&Command{Name: "delete-route", Summary: "Remove a route", Flags: []Flag{req("route-table-id", "route table id"), req("destination-cidr-block", "destination CIDR")},
			Run: func(x *Exec) (any, error) {
				_, err := x.Call("DELETE", "/vpc/route-tables/"+esc(x.Str("route-table-id"))+"/routes?destination_cidr="+url.QueryEscape(x.Str("destination-cidr-block")), nil)
				return nil, err
			}},
		simple("associate-route-table", "Associate a route table with a subnet", "POST", func(x *Exec) string { return "/vpc/route-tables/" + esc(x.Str("route-table-id")) + "/associations" },
			func(x *Exec) (any, error) { return map[string]any{"subnet_id": x.Str("subnet-id")}, nil }, req("route-table-id", "route table id"), req("subnet-id", "subnet id")),
		deleteCmd("delete-route-table", "Delete a route table", "route-table-id", "/vpc/route-tables/"),
		simple("create-nat-gateway", "Create a NAT gateway in a (public) subnet", "POST", func(x *Exec) string { return "/vpc/nat-gateways" },
			func(x *Exec) (any, error) {
				return map[string]any{"subnet_id": x.Str("subnet-id"), "name": x.Str("name")}, nil
			}, req("subnet-id", "subnet id"), opt("name", "name")),
		listCmd("describe-nat-gateways", "Describe NAT gateways", "/vpc/nat-gateways", "nat-gateway-ids", "nat_gateway_id", []string{"nat_gateway_id", "name", "subnet_id", "state", "public_ip", "private_ip"}),
		&Command{Name: "delete-nat-gateway", Summary: "Delete a NAT gateway", Flags: []Flag{req("nat-gateway-id", "nat id")}, Run: func(x *Exec) (any, error) {
			return x.Call("DELETE", "/vpc/nat-gateways/"+esc(x.Str("nat-gateway-id")), nil)
		}},
		listCmd("describe-network-acls", "Describe network ACLs", "/vpc/network-acls", "network-acl-ids", "network_acl_id", []string{"network_acl_id", "vpc_id", "is_default", "subnet_ids"}),
		listCmd("describe-network-interfaces", "Describe network interfaces", "/vpc/network-interfaces", "network-interface-ids", "network_interface_id", []string{"network_interface_id", "subnet_id", "private_ip", "public_ip", "status"}),
		simple("analyze-reachability", "Check whether traffic can flow between two endpoints (CloudSim extension)", "POST", func(x *Exec) string { return "/vpc/reachability" },
			func(x *Exec) (any, error) {
				p := x.Str("protocol")
				if p == "" {
					p = "tcp"
				}
				return map[string]any{"source": x.Str("source"), "destination": x.Str("destination"), "protocol": p, "port": x.Int("port", 0)}, nil
			}, req("source", "i-..., internet or an IP"), req("destination", "i-..., internet or an IP"), opt("protocol", "tcp|udp|icmp|-1"), Flag{Name: "port", Kind: Int, Help: "port"}),
	)
}

func sgFlags() []Flag {
	return []Flag{req("group-id", "security group id"), opt("protocol", "tcp|udp|icmp|-1 (default tcp)"), opt("port", "port or range, e.g. 22 or 8000-8080"),
		opt("cidr", "source/destination CIDR"), opt("source-group", "source security group id"), opt("description", "rule description")}
}

func deleteCmd(name, summary, flag, prefix string) *Command {
	return &Command{Name: name, Summary: summary, Flags: []Flag{req(flag, "id")}, Run: func(x *Exec) (any, error) {
		_, err := x.Call("DELETE", prefix+esc(x.Str(flag)), nil)
		return nil, err
	}}
}
