package labs

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// kind describes how to find and delete one resource type through the public API.
type kind struct {
	name      string
	list      string
	idField   string
	nameField string
	// delete returns the API calls (method, path) that remove the item.
	delete func(id string, item map[string]any) [][2]string
	// gone reports items already deleted/terminated.
	gone func(item map[string]any) bool
	// skip reports items that are never deleted (defaults, built-ins).
	skip func(item map[string]any) bool
	// inVPC: also delete items whose vpc_id belongs to a lab VPC.
	inVPC bool
}

func del(prefix string) func(string, map[string]any) [][2]string {
	return func(id string, _ map[string]any) [][2]string { return [][2]string{{"DELETE", prefix + url.PathEscape(id)}} }
}

func isTrue(m map[string]any, k string) bool { return m[k] == true }

// kinds in deletion order (dependents first).
var kinds = []kind{
	{name: "autoscaling group", list: "/autoscaling/groups", idField: "name", nameField: "name",
		delete: func(id string, _ map[string]any) [][2]string {
			return [][2]string{{"DELETE", "/autoscaling/groups/" + url.PathEscape(id) + "?force=true"}}
		}},
	{name: "load balancer", list: "/elb/load-balancers", idField: "load_balancer_id", nameField: "name", delete: del("/elb/load-balancers/"), inVPC: true,
		gone: func(m map[string]any) bool { return str(m["state"]) == "deleted" }},
	{name: "target group", list: "/elb/target-groups", idField: "target_group_id", nameField: "name", delete: del("/elb/target-groups/"), inVPC: true},
	{name: "alarm", list: "/cloudwatch/alarms", idField: "alarm_name", nameField: "alarm_name", delete: del("/cloudwatch/alarms/")},
	{name: "launch template", list: "/autoscaling/launch-templates", idField: "name", nameField: "name", delete: del("/autoscaling/launch-templates/")},
	{name: "instance", list: "/ec2/instances", idField: "instance_id", nameField: "name", inVPC: true,
		delete: func(id string, _ map[string]any) [][2]string {
			return [][2]string{{"POST", "/ec2/instances/" + url.PathEscape(id) + "/terminate"}}
		},
		gone: func(m map[string]any) bool { return str(m["state"]) == "terminated" }},
	{name: "NAT gateway", list: "/vpc/nat-gateways", idField: "nat_gateway_id", nameField: "name", delete: del("/vpc/nat-gateways/"), inVPC: true,
		gone: func(m map[string]any) bool { return str(m["state"]) == "deleted" }},
	{name: "internet gateway", list: "/vpc/internet-gateways", idField: "internet_gateway_id", nameField: "name", inVPC: true,
		delete: func(id string, m map[string]any) [][2]string {
			calls := [][2]string{}
			if str(m["state"]) == "attached" {
				calls = append(calls, [2]string{"POST", "/vpc/internet-gateways/" + url.PathEscape(id) + "/detach"})
			}
			return append(calls, [2]string{"DELETE", "/vpc/internet-gateways/" + url.PathEscape(id)})
		}},
	{name: "subnet", list: "/vpc/subnets", idField: "subnet_id", nameField: "name", delete: del("/vpc/subnets/"), inVPC: true},
	{name: "route table", list: "/vpc/route-tables", idField: "route_table_id", nameField: "name", delete: del("/vpc/route-tables/"), inVPC: true,
		skip: func(m map[string]any) bool { return isTrue(m, "is_main") }},
	{name: "network ACL", list: "/vpc/network-acls", idField: "network_acl_id", nameField: "name", delete: del("/vpc/network-acls/"), inVPC: true,
		skip: func(m map[string]any) bool { return isTrue(m, "is_default") }},
	{name: "security group", list: "/vpc/security-groups", idField: "group_id", nameField: "group_name", delete: del("/vpc/security-groups/"), inVPC: true,
		skip: func(m map[string]any) bool { return isTrue(m, "is_default") || str(m["group_name"]) == "default" }},
	{name: "VPC", list: "/vpc/vpcs", idField: "vpc_id", nameField: "name", delete: del("/vpc/vpcs/"),
		skip: func(m map[string]any) bool { return isTrue(m, "is_default") }},
	{name: "bucket", list: "/s3/buckets", idField: "name", nameField: "name",
		delete: func(id string, _ map[string]any) [][2]string {
			return [][2]string{{"DELETE", "/s3/buckets/" + url.PathEscape(id) + "?force=true"}}
		}},
	{name: "key pair", list: "/ec2/key-pairs", idField: "name", nameField: "name", delete: del("/ec2/key-pairs/")},
	{name: "IAM user", list: "/iam/users", idField: "user_name", nameField: "user_name", delete: del("/iam/users/")},
	{name: "IAM group", list: "/iam/groups", idField: "group_name", nameField: "group_name", delete: del("/iam/groups/")},
	{name: "IAM role", list: "/iam/roles", idField: "role_name", nameField: "role_name", delete: del("/iam/roles/")},
	{name: "IAM policy", list: "/iam/policies", idField: "policy_name", nameField: "policy_name", delete: del("/iam/policies/"),
		skip: func(m map[string]any) bool { return isTrue(m, "is_builtin") }},
	{name: "log group", list: "/cloudwatch/log-groups", idField: "name", nameField: "name", delete: del("/cloudwatch/log-groups/")},
	{name: "SNS topic", list: "/sns/topics", idField: "name", nameField: "name", delete: del("/sns/topics/")},
	{name: "SQS queue", list: "/sqs/queues", idField: "name", nameField: "name", delete: del("/sqs/queues/")},
}

// ResetReport summarizes a reset.
type ResetReport struct {
	Deleted   []string `json:"deleted"`
	Remaining []string `json:"remaining"`
	Errors    []string `json:"errors"`
}

func (l *Lab) owns(m map[string]any, nameField string) bool {
	if tags, ok := m["tags"].(map[string]any); ok {
		if str(tags["cloudsim:lab"]) == l.ID || strings.HasPrefix(str(tags["Name"]), l.ResourcePrefix) {
			return true
		}
	}
	return strings.HasPrefix(str(m[nameField]), l.ResourcePrefix)
}

// Reset deletes every resource of the account that belongs to the lab (tag cloudsim:lab=<id>,
// name with the lab's resource prefix, or contained in a lab VPC). Deletions go through the API so
// dependency rules and the engine lifecycle apply; it retries in passes until nothing is left or
// the deadline passes (async deletions such as instance termination take a moment).
func Reset(ctx context.Context, c Caller, l *Lab, deadline time.Duration) ResetReport {
	rep := ResetReport{Deleted: []string{}, Remaining: []string{}, Errors: []string{}}
	ev := &evaluator{ctx: ctx, c: c, lab: l}
	end := time.Now().Add(deadline)
	requested := map[string]bool{}
	unavailable := map[string]bool{}
	for {
		var remaining []string
		lastErr := map[string]string{}
		vpcs := map[string]bool{}
		if vl, err := ev.list("/vpc/vpcs", nil); err == nil {
			for _, it := range vl {
				if m, ok := it.(map[string]any); ok && !isTrue(m, "is_default") && l.owns(m, "name") {
					vpcs[str(m["vpc_id"])] = true
				}
			}
		}
		for _, k := range kinds {
			if unavailable[k.list] {
				continue
			}
			items, err := ev.list(k.list, nil)
			if err != nil {
				unavailable[k.list] = true // module not available in this release
				continue
			}
			for _, it := range items {
				m, ok := it.(map[string]any)
				if !ok {
					continue
				}
				id := str(m[k.idField])
				if id == "" || (k.skip != nil && k.skip(m)) {
					continue
				}
				if !l.owns(m, k.nameField) && !(k.inVPC && vpcs[str(m["vpc_id"])]) {
					continue
				}
				label := k.name + " " + id
				if k.gone != nil && k.gone(m) {
					if requested[label] {
						rep.Deleted = appendOnce(rep.Deleted, label)
					}
					continue
				}
				remaining = append(remaining, label)
				if requested[label] && k.gone != nil {
					continue // async deletion in progress
				}
				for _, call := range k.delete(id, m) {
					st, b, err := c.Call(ctx, call[0], call[1], nil)
					if err != nil {
						lastErr[label] = err.Error()
						break
					}
					if st >= 400 && st != 404 {
						if call[0] == "POST" && strings.HasSuffix(call[1], "/detach") {
							continue
						}
						lastErr[label] = apiErr(decodeJSON(b), st).Error()
						break
					}
					requested[label] = true
				}
				if requested[label] && k.gone == nil && lastErr[label] == "" {
					rep.Deleted = appendOnce(rep.Deleted, label)
				}
			}
		}
		if len(remaining) == 0 || time.Now().After(end) || ctx.Err() != nil {
			for _, r := range remaining {
				if !contains(rep.Deleted, r) {
					rep.Remaining = append(rep.Remaining, r)
					if e := lastErr[r]; e != "" {
						rep.Errors = append(rep.Errors, r+": "+e)
					}
				}
			}
			return rep
		}
		select {
		case <-ctx.Done():
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func appendOnce(s []string, v string) []string {
	if contains(s, v) {
		return s
	}
	return append(s, v)
}
