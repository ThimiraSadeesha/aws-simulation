package commands

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// resolveID turns an ARN into the resource id by looking it up in a list endpoint; ids pass through.
func (x *Exec) resolveID(v, listPath, idField string) (string, error) {
	if !strings.HasPrefix(v, "arn:") {
		return v, nil
	}
	items, err := x.ListAll(listPath, nil)
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && scalarString(m["arn"]) == v {
			return scalarString(m[idField]), nil
		}
	}
	if i := strings.LastIndex(v, "/"); i >= 0 {
		return v[i+1:], nil
	}
	return v, nil
}

func lastSeg(v, sep string) string {
	if i := strings.LastIndex(v, sep); i >= 0 {
		return v[i+1:]
	}
	return v
}

// timeArg accepts RFC 3339 or relative "-15m"/"-1h" (ago).
func timeArg(v string) string {
	if strings.HasPrefix(v, "-") {
		if d, err := time.ParseDuration(v[1:]); err == nil {
			return time.Now().Add(-d).UTC().Format(time.RFC3339)
		}
	}
	return v
}

// dims parses "Name=InstanceId,Value=i-1" tokens (or InstanceId=i-1) into a map.
func dims(vals []string) map[string]string {
	out := map[string]string{}
	for _, v := range vals {
		m := Shorthand(v)
		if len(m["Name"]) == 1 && len(m["Value"]) > 0 {
			out[m["Name"][0]] = strings.Join(m["Value"], ",")
			continue
		}
		if k, val, ok := strings.Cut(v, "="); ok {
			out[k] = val
		}
	}
	return out
}

func tgID(x *Exec, flag string) (string, error) {
	return x.resolveID(x.Str(flag), "/elb/target-groups", "target_group_id")
}

func lbID(x *Exec) (string, error) {
	return x.resolveID(x.Str("load-balancer-arn"), "/elb/load-balancers", "load_balancer_id")
}

func init() {
	tgArn := req("target-group-arn", "target group ARN or id")
	lbArn := req("load-balancer-arn", "load balancer ARN or id")
	register("elbv2", "simulated application load balancing",
		&Command{Name: "create-target-group", Summary: "Create a target group",
			Flags: []Flag{req("name", "name"), opt("protocol", "HTTP (default)"), {Name: "port", Kind: Int, Help: "port (default 80)"}, req("vpc-id", "VPC id"),
				opt("health-check-path", "health check path (default /)"), {Name: "health-check-interval-seconds", Kind: Int}, {Name: "healthy-threshold-count", Kind: Int}, {Name: "unhealthy-threshold-count", Kind: Int}},
			Run: func(x *Exec) (any, error) {
				p := x.Str("protocol")
				if p == "" {
					p = "HTTP"
				}
				hc := map[string]any{"path": x.Str("health-check-path")}
				if hc["path"] == "" {
					hc["path"] = "/"
				}
				for f, k := range map[string]string{"health-check-interval-seconds": "interval_s", "healthy-threshold-count": "healthy_threshold", "unhealthy-threshold-count": "unhealthy_threshold"} {
					if x.Has(f) {
						hc[k] = x.Int(f, 0)
					}
				}
				return x.Call("POST", "/elb/target-groups", map[string]any{"name": x.Str("name"), "protocol": p, "port": x.Int("port", 80), "vpc_id": x.Str("vpc-id"), "health_check": hc})
			}},
		listCmd("describe-target-groups", "Describe target groups", "/elb/target-groups", "names", "name", []string{"target_group_id", "name", "protocol", "port", "vpc_id", "algorithm"}),
		&Command{Name: "delete-target-group", Summary: "Delete a target group", Flags: []Flag{tgArn}, Run: func(x *Exec) (any, error) {
			id, err := tgID(x, "target-group-arn")
			if err != nil {
				return nil, err
			}
			_, err = x.Call("DELETE", "/elb/target-groups/"+esc(id), nil)
			return nil, err
		}},
		&Command{Name: "register-targets", Summary: "Register instances with a target group", Flags: []Flag{tgArn, {Name: "targets", Kind: List, Required: true, Help: "Id=i-...[,Port=80] ..."}},
			Run: func(x *Exec) (any, error) {
				id, err := tgID(x, "target-group-arn")
				if err != nil {
					return nil, err
				}
				var ts []map[string]any
				for _, t := range x.Flags["targets"] {
					m := Shorthand(t)
					tid := t
					if len(m["Id"]) > 0 {
						tid = m["Id"][0]
					}
					e := map[string]any{"id": tid}
					if len(m["Port"]) > 0 {
						var p int
						_ = json.Unmarshal([]byte(m["Port"][0]), &p)
						e["port"] = p
					}
					ts = append(ts, e)
				}
				return x.Call("POST", "/elb/target-groups/"+esc(id)+"/targets", map[string]any{"targets": ts})
			}},
		&Command{Name: "deregister-targets", Summary: "Deregister instances", Flags: []Flag{tgArn, {Name: "targets", Kind: List, Required: true, Help: "Id=i-..."}},
			Run: func(x *Exec) (any, error) {
				id, err := tgID(x, "target-group-arn")
				if err != nil {
					return nil, err
				}
				for _, t := range x.Flags["targets"] {
					tid := t
					if m := Shorthand(t); len(m["Id"]) > 0 {
						tid = m["Id"][0]
					}
					if _, err := x.Call("DELETE", "/elb/target-groups/"+esc(id)+"/targets/"+esc(tid), nil); err != nil {
						return nil, err
					}
				}
				return nil, nil
			}},
		&Command{Name: "describe-target-health", Summary: "Show target health", Flags: []Flag{tgArn}, Columns: []string{"id", "port", "health", "reason"},
			Run: func(x *Exec) (any, error) {
				id, err := tgID(x, "target-group-arn")
				if err != nil {
					return nil, err
				}
				return x.Call("GET", "/elb/target-groups/"+esc(id)+"/health", nil)
			}},
		&Command{Name: "create-load-balancer", Summary: "Create a load balancer",
			Flags: []Flag{req("name", "name"), {Name: "subnets", Kind: List, Required: true, Help: "subnet ids"}, {Name: "security-groups", Kind: List, Help: "security group ids"}, opt("scheme", "internet-facing|internal")},
			Run: func(x *Exec) (any, error) {
				sch := x.Str("scheme")
				if sch == "" {
					sch = "internet-facing"
				}
				return x.Call("POST", "/elb/load-balancers", map[string]any{"name": x.Str("name"), "scheme": sch, "subnet_ids": x.List("subnets"), "security_group_ids": x.List("security-groups")})
			}},
		listCmd("describe-load-balancers", "Describe load balancers", "/elb/load-balancers", "names", "name", []string{"load_balancer_id", "name", "dns_name", "scheme", "state", "vpc_id"}),
		&Command{Name: "delete-load-balancer", Summary: "Delete a load balancer", Flags: []Flag{lbArn}, Run: func(x *Exec) (any, error) {
			id, err := lbID(x)
			if err != nil {
				return nil, err
			}
			_, err = x.Call("DELETE", "/elb/load-balancers/"+esc(id), nil)
			return nil, err
		}},
		&Command{Name: "create-listener", Summary: "Add a listener forwarding to a target group",
			Flags: []Flag{lbArn, opt("protocol", "HTTP (default)"), {Name: "port", Kind: Int, Help: "port (default 80)"}, {Name: "default-actions", Kind: List, Required: true, Help: "Type=forward,TargetGroupArn=<arn|id>"}},
			Run: func(x *Exec) (any, error) {
				id, err := lbID(x)
				if err != nil {
					return nil, err
				}
				m := Shorthand(strings.Join(x.Flags["default-actions"], ","))
				tg := ""
				if len(m["TargetGroupArn"]) > 0 {
					tg = m["TargetGroupArn"][0]
				} else {
					var arr []struct {
						TargetGroupArn string `json:"TargetGroupArn"`
					}
					if json.Unmarshal([]byte(x.Str("default-actions")), &arr) == nil && len(arr) > 0 {
						tg = arr[0].TargetGroupArn
					}
				}
				if tg == "" {
					return nil, usagef("--default-actions needs TargetGroupArn=<arn|id>")
				}
				if tg, err = x.resolveID(tg, "/elb/target-groups", "target_group_id"); err != nil {
					return nil, err
				}
				p := x.Str("protocol")
				if p == "" {
					p = "HTTP"
				}
				return x.Call("POST", "/elb/load-balancers/"+esc(id)+"/listeners", map[string]any{"protocol": p, "port": x.Int("port", 80), "default_target_group_id": tg})
			}},
		&Command{Name: "simulate-traffic", Summary: "Send simulated requests through a load balancer (CloudSim extension)",
			Flags: []Flag{lbArn, {Name: "requests", Kind: Int, Help: "number of requests (default 1000)"}, {Name: "listener-port", Kind: Int}, {Name: "duration-seconds", Kind: Int}},
			Run: func(x *Exec) (any, error) {
				id, err := lbID(x)
				if err != nil {
					return nil, err
				}
				b := map[string]any{"requests": x.Int("requests", 1000)}
				if x.Has("listener-port") {
					b["listener_port"] = x.Int("listener-port", 80)
				}
				if x.Has("duration-seconds") {
					b["duration_s"] = x.Int("duration-seconds", 60)
				}
				return x.Call("POST", "/elb/load-balancers/"+esc(id)+"/simulate", b)
			}},
	)

	asg := req("auto-scaling-group-name", "group name")
	register("autoscaling", "simulated auto scaling",
		&Command{Name: "create-launch-template", Summary: "Create a launch template",
			Flags: []Flag{req("launch-template-name", "name"), opt("image-id", "AMI id"), opt("instance-type", "type"), {Name: "security-group-ids", Kind: List}, opt("key-name", "key pair"),
				opt("user-data", "script or file://"), opt("workload-profile", "idle|steady|spiky|stress|custom"), opt("launch-template-data", "JSON {ImageId, InstanceType, SecurityGroupIds, KeyName, UserData}")},
			Run: func(x *Exec) (any, error) {
				ud, err := x.Value("user-data")
				if err != nil {
					return nil, err
				}
				b := map[string]any{"name": x.Str("launch-template-name"), "image_id": x.Str("image-id"), "instance_type": x.Str("instance-type"),
					"security_group_ids": x.List("security-group-ids"), "key_name": x.Str("key-name"), "user_data": ud, "workload_profile": x.Str("workload-profile")}
				if d := x.Str("launch-template-data"); d != "" {
					raw, err := x.resolveValue(d)
					if err != nil {
						return nil, err
					}
					var j struct {
						ImageId, InstanceType, KeyName, UserData string
						SecurityGroupIds                         []string
					}
					if err := json.Unmarshal([]byte(raw), &j); err != nil {
						return nil, usagef("invalid --launch-template-data: %v", err)
					}
					for k, v := range map[string]string{"image_id": j.ImageId, "instance_type": j.InstanceType, "key_name": j.KeyName, "user_data": j.UserData} {
						if v != "" {
							b[k] = v
						}
					}
					if len(j.SecurityGroupIds) > 0 {
						b["security_group_ids"] = j.SecurityGroupIds
					}
				}
				if b["instance_type"] == "" {
					b["instance_type"] = "t3.micro"
				}
				return x.Call("POST", "/autoscaling/launch-templates", b)
			}},
		listCmd("describe-launch-templates", "List launch templates", "/autoscaling/launch-templates", "launch-template-names", "name", []string{"name", "image_id", "instance_type", "workload_profile"}),
		&Command{Name: "create-auto-scaling-group", Summary: "Create an auto scaling group",
			Flags: []Flag{asg, req("launch-template", "LaunchTemplateName=<name> or the name"), {Name: "min-size", Kind: Int, Required: true}, {Name: "max-size", Kind: Int, Required: true},
				{Name: "desired-capacity", Kind: Int}, req("vpc-zone-identifier", "comma separated subnet ids"), {Name: "target-group-arns", Kind: List},
				opt("health-check-type", "EC2|ELB"), {Name: "default-cooldown", Kind: Int}},
			Run: func(x *Exec) (any, error) {
				lt := x.Str("launch-template")
				if m := Shorthand(lt); len(m["LaunchTemplateName"]) > 0 {
					lt = m["LaunchTemplateName"][0]
				}
				var tgs []string
				for _, t := range x.List("target-group-arns") {
					id, err := x.resolveID(t, "/elb/target-groups", "target_group_id")
					if err != nil {
						return nil, err
					}
					tgs = append(tgs, id)
				}
				b := map[string]any{"name": x.Str("auto-scaling-group-name"), "launch_template": lt, "min_size": x.Int("min-size", 0), "max_size": x.Int("max-size", 0),
					"desired_capacity": x.Int("desired-capacity", x.Int("min-size", 0)), "subnet_ids": x.List("vpc-zone-identifier"), "target_group_ids": tgs}
				if v := x.Str("health-check-type"); v != "" {
					b["health_check_type"] = v
				}
				if x.Has("default-cooldown") {
					b["cooldown_s"] = x.Int("default-cooldown", 60)
				}
				return x.Call("POST", "/autoscaling/groups", b)
			}},
		listCmd("describe-auto-scaling-groups", "Describe auto scaling groups", "/autoscaling/groups", "auto-scaling-group-names", "name", []string{"name", "launch_template", "min_size", "max_size", "desired_capacity", "status"}),
		&Command{Name: "update-auto-scaling-group", Summary: "Change min/max/desired", Flags: []Flag{asg, {Name: "min-size", Kind: Int}, {Name: "max-size", Kind: Int}, {Name: "desired-capacity", Kind: Int}},
			Run: func(x *Exec) (any, error) {
				b := map[string]any{}
				for f, k := range map[string]string{"min-size": "min_size", "max-size": "max_size", "desired-capacity": "desired_capacity"} {
					if x.Has(f) {
						b[k] = x.Int(f, 0)
					}
				}
				return x.Call("PATCH", "/autoscaling/groups/"+esc(x.Str("auto-scaling-group-name")), b)
			}},
		simple("set-desired-capacity", "Set the desired capacity", "PATCH", func(x *Exec) string { return "/autoscaling/groups/" + esc(x.Str("auto-scaling-group-name")) },
			func(x *Exec) (any, error) {
				return map[string]any{"desired_capacity": x.Int("desired-capacity", 0)}, nil
			}, asg, Flag{Name: "desired-capacity", Kind: Int, Required: true}),
		&Command{Name: "delete-auto-scaling-group", Summary: "Delete a group", Flags: []Flag{asg, {Name: "force-delete", Kind: Bool, Help: "terminate instances"}}, Run: func(x *Exec) (any, error) {
			p := "/autoscaling/groups/" + esc(x.Str("auto-scaling-group-name"))
			if f, _ := x.Bool("force-delete"); f {
				p += "?force=true"
			}
			_, err := x.Call("DELETE", p, nil)
			return nil, err
		}},
		&Command{Name: "put-scaling-policy", Summary: "Add a target tracking or step scaling policy",
			Flags: []Flag{asg, req("policy-name", "name"), opt("policy-type", "TargetTrackingScaling (default) | StepScaling"), {Name: "target-value", Kind: Float, Help: "target CPU % (target tracking)"},
				opt("target-tracking-configuration", "TargetValue=50 or JSON"), {Name: "threshold", Kind: Float, Help: "step: CPU threshold"}, opt("comparison", "step: >= or <="), {Name: "adjustment", Kind: Int, Help: "step: instances to add/remove"}},
			Run: func(x *Exec) (any, error) {
				b := map[string]any{"name": x.Str("policy-name")}
				if strings.EqualFold(x.Str("policy-type"), "StepScaling") || x.Str("policy-type") == "step" {
					cmp := x.Str("comparison")
					if cmp == "" {
						cmp = ">="
					}
					b["type"], b["threshold"], b["comparison"], b["adjustment"] = "step", x.Float("threshold", 80), cmp, x.Int("adjustment", 1)
				} else {
					tv := x.Float("target-value", 0)
					if c := x.Str("target-tracking-configuration"); c != "" {
						if m := Shorthand(c); len(m["TargetValue"]) > 0 {
							_ = json.Unmarshal([]byte(m["TargetValue"][0]), &tv)
						} else {
							var j struct{ TargetValue float64 }
							if json.Unmarshal([]byte(c), &j) == nil {
								tv = j.TargetValue
							}
						}
					}
					if tv <= 0 {
						return nil, usagef("--target-value (or --target-tracking-configuration TargetValue=N) is required")
					}
					b["type"], b["target_value"] = "target_tracking", tv
				}
				return x.Call("POST", "/autoscaling/groups/"+esc(x.Str("auto-scaling-group-name"))+"/policies", b)
			}},
		simple("describe-policies", "List scaling policies of a group", "GET", func(x *Exec) string {
			return "/autoscaling/groups/" + esc(x.Str("auto-scaling-group-name")) + "/policies"
		}, nil, asg),
		simple("describe-scaling-activities", "Show scaling activities", "GET", func(x *Exec) string {
			return "/autoscaling/groups/" + esc(x.Str("auto-scaling-group-name")) + "/activities"
		}, nil, asg),
		simple("execute-policy-evaluation", "Run one scaling evaluation now (CloudSim extension)", "POST", func(x *Exec) string {
			return "/autoscaling/groups/" + esc(x.Str("auto-scaling-group-name")) + "/evaluate"
		}, nil, asg),
	)

	register("cloudwatch", "simulated metrics and alarms",
		&Command{Name: "get-metric-statistics", Summary: "Get datapoints of a metric", Columns: []string{"ts", "value"},
			Flags: []Flag{req("namespace", "e.g. CloudSim/EC2"), req("metric-name", "e.g. CPUUtilization"), {Name: "dimensions", Kind: List, Help: "Name=InstanceId,Value=i-..."},
				opt("start-time", "RFC 3339 or -1h"), opt("end-time", "RFC 3339"), {Name: "period", Kind: Int, Help: "seconds (default 60)"}, {Name: "statistics", Kind: List, Help: "Average|Maximum|Minimum|Sum|SampleCount"}},
			Run: func(x *Exec) (any, error) {
				q := url.Values{"namespace": {x.Str("namespace")}, "metric_name": {x.Str("metric-name")}}
				for k, v := range dims(x.Flags["dimensions"]) {
					q.Add("dimension", k+":"+v)
				}
				if v := x.Str("start-time"); v != "" {
					q.Set("start", timeArg(v))
				}
				if v := x.Str("end-time"); v != "" {
					q.Set("end", timeArg(v))
				}
				q.Set("period", x.Str("period"))
				if q.Get("period") == "" {
					q.Set("period", "60")
				}
				st := x.List("statistics")
				if len(st) == 0 {
					st = []string{"Average"}
				}
				q.Set("stat", st[0])
				return x.Call("GET", "/cloudwatch/metrics?"+q.Encode(), nil)
			}},
		listCmd("list-metrics", "List metrics", "/cloudwatch/metrics/list", "", "", []string{"namespace", "metric_name", "dimensions"}),
		&Command{Name: "put-metric-data", Summary: "Publish a custom metric datapoint",
			Flags: []Flag{req("namespace", "custom namespace (not CloudSim/*)"), req("metric-name", "name"), {Name: "value", Kind: Float, Required: true}, {Name: "dimensions", Kind: List}, opt("unit", "unit")},
			Run: func(x *Exec) (any, error) {
				d := map[string]any{"metric_name": x.Str("metric-name"), "value": x.Float("value", 0), "dimensions": dims(x.Flags["dimensions"])}
				if u := x.Str("unit"); u != "" {
					d["unit"] = u
				}
				return x.Call("POST", "/cloudwatch/metrics", map[string]any{"namespace": x.Str("namespace"), "metric_data": []any{d}})
			}},
		&Command{Name: "put-metric-alarm", Summary: "Create or update an alarm",
			Flags: []Flag{req("alarm-name", "name"), req("namespace", "namespace"), req("metric-name", "metric"), {Name: "dimensions", Kind: List}, opt("statistic", "Average (default)"),
				{Name: "period", Kind: Int}, {Name: "evaluation-periods", Kind: Int}, {Name: "threshold", Kind: Float, Required: true}, req("comparison-operator", "GreaterThanThreshold|..."),
				{Name: "alarm-actions", Kind: List, Help: "ARNs of scaling policies or SNS topics"}},
			Run: func(x *Exec) (any, error) {
				st := x.Str("statistic")
				if st == "" {
					st = "Average"
				}
				acts := x.List("alarm-actions")
				if acts == nil {
					acts = []string{}
				}
				return x.Call("POST", "/cloudwatch/alarms", map[string]any{"alarm_name": x.Str("alarm-name"), "namespace": x.Str("namespace"), "metric_name": x.Str("metric-name"),
					"dimensions": dims(x.Flags["dimensions"]), "statistic": st, "period": x.Int("period", 60), "evaluation_periods": x.Int("evaluation-periods", 1),
					"threshold": x.Float("threshold", 0), "comparison_operator": x.Str("comparison-operator"), "alarm_actions": acts})
			}},
		listCmd("describe-alarms", "Describe alarms", "/cloudwatch/alarms", "alarm-names", "alarm_name", []string{"alarm_name", "metric_name", "threshold", "state", "state_reason"},
			Flag{Name: "state-value", Help: "OK|ALARM|INSUFFICIENT_DATA"}),
		&Command{Name: "delete-alarms", Summary: "Delete alarms", Flags: []Flag{{Name: "alarm-names", Kind: List, Required: true}}, Run: func(x *Exec) (any, error) {
			for _, n := range x.List("alarm-names") {
				if _, err := x.Call("DELETE", "/cloudwatch/alarms/"+esc(n), nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}},
		simple("describe-alarm-history", "Show alarm state history", "GET", func(x *Exec) string { return "/cloudwatch/alarms/" + esc(x.Str("alarm-name")) + "/history" }, nil, req("alarm-name", "name")),
	)
	// describe-alarms --state-value is applied client-side (state field)
	da := registry["cloudwatch"].Commands["describe-alarms"]
	inner := da.Run
	da.Run = func(x *Exec) (any, error) {
		sv := x.Str("state-value")
		delete(x.Flags, "state-value")
		v, err := inner(x)
		if err != nil || sv == "" {
			return v, err
		}
		m := v.(map[string]any)
		m["items"] = applyFilters(m["items"].([]any), []Filter{{Name: "state", Values: []string{sv}}})
		if m["items"] == nil {
			m["items"] = []any{}
		}
		return m, nil
	}

	lg := req("log-group-name", "log group")
	register("logs", "simulated log groups and log events",
		listCmd("describe-log-groups", "List log groups", "/cloudwatch/log-groups", "", "", []string{"name", "retention_days", "stored_bytes", "created_at"}),
		simple("create-log-group", "Create a log group", "POST", func(x *Exec) string { return "/cloudwatch/log-groups" },
			func(x *Exec) (any, error) { return map[string]any{"name": x.Str("log-group-name")}, nil }, lg),
		deleteCmd("delete-log-group", "Delete a log group", "log-group-name", "/cloudwatch/log-groups/"),
		simple("describe-log-streams", "List streams of a log group", "GET", func(x *Exec) string { return "/cloudwatch/log-groups/" + esc(x.Str("log-group-name")) + "/streams" }, nil, lg),
		simple("put-log-events", "Write log events", "POST", func(x *Exec) string {
			return "/cloudwatch/log-groups/" + esc(x.Str("log-group-name")) + "/streams/" + esc(x.Str("log-stream-name")) + "/events"
		}, func(x *Exec) (any, error) {
			var evs []any
			for _, m := range x.Flags["messages"] {
				evs = append(evs, map[string]any{"message": m})
			}
			return map[string]any{"events": evs}, nil
		}, lg, req("log-stream-name", "stream"), Flag{Name: "messages", Kind: List, Required: true, Help: "messages"}),
		&Command{Name: "filter-log-events", Summary: "Search log events", Columns: []string{"ts", "stream", "message"},
			Flags: []Flag{lg, opt("filter-pattern", `terms, "phrase", -exclude, ?any`), {Name: "log-stream-names", Kind: List}, opt("start-time", "RFC 3339 or -1h"), opt("end-time", "RFC 3339"), {Name: "limit", Kind: Int}},
			Run: func(x *Exec) (any, error) {
				q := url.Values{}
				if v := x.Str("filter-pattern"); v != "" {
					q.Set("filter_pattern", v)
				}
				if s := x.List("log-stream-names"); len(s) > 0 {
					q.Set("stream", s[0])
				}
				if v := x.Str("start-time"); v != "" {
					q.Set("start", timeArg(v))
				}
				if v := x.Str("end-time"); v != "" {
					q.Set("end", timeArg(v))
				}
				if x.Has("limit") {
					q.Set("limit", x.Str("limit"))
				}
				return x.Call("GET", "/cloudwatch/log-groups/"+esc(x.Str("log-group-name"))+"/events?"+q.Encode(), nil)
			}},
	)

	queue := func(x *Exec) string {
		n := x.Str("queue-url")
		if n == "" {
			n = x.Str("queue-name")
		}
		return esc(lastSeg(n, "/"))
	}
	qflags := []Flag{opt("queue-url", "queue URL or name"), opt("queue-name", "queue name")}
	register("sqs", "simulated message queues",
		simple("create-queue", "Create a queue", "POST", func(x *Exec) string { return "/sqs/queues" },
			func(x *Exec) (any, error) { return map[string]any{"name": x.Str("queue-name")}, nil }, req("queue-name", "name")),
		listCmd("list-queues", "List queues", "/sqs/queues", "", "", nil),
		simple("delete-queue", "Delete a queue", "DELETE", func(x *Exec) string { return "/sqs/queues/" + queue(x) }, nil, qflags...),
		simple("send-message", "Send a message", "POST", func(x *Exec) string { return "/sqs/queues/" + queue(x) + "/messages" },
			func(x *Exec) (any, error) {
				b := map[string]any{"body": x.Str("message-body")}
				if x.Has("delay-seconds") {
					b["delay_seconds"] = x.Int("delay-seconds", 0)
				}
				return b, nil
			}, append(qflags, req("message-body", "body"), Flag{Name: "delay-seconds", Kind: Int})...),
		simple("receive-message", "Receive messages", "GET", func(x *Exec) string {
			return "/sqs/queues/" + queue(x) + "/messages?max=" + url.QueryEscape(defaultStr(x.Str("max-number-of-messages"), "1")) + "&visibility_timeout=" + url.QueryEscape(defaultStr(x.Str("visibility-timeout"), "30"))
		}, nil, append(qflags, Flag{Name: "max-number-of-messages", Kind: Int}, Flag{Name: "visibility-timeout", Kind: Int})...),
		simple("delete-message", "Delete a received message", "DELETE", func(x *Exec) string { return "/sqs/queues/" + queue(x) + "/messages/" + esc(x.Str("receipt-handle")) }, nil,
			append(qflags, req("receipt-handle", "receipt handle"))...),
		simple("purge-queue", "Delete all messages", "POST", func(x *Exec) string { return "/sqs/queues/" + queue(x) + "/purge" }, nil, qflags...),
	)
	topic := func(x *Exec) string {
		n := x.Str("topic-arn")
		if n == "" {
			n = x.Str("name")
		}
		return esc(lastSeg(n, ":"))
	}
	tflags := []Flag{opt("topic-arn", "topic ARN or name"), opt("name", "topic name")}
	register("sns", "simulated notifications (pub/sub)",
		simple("create-topic", "Create a topic", "POST", func(x *Exec) string { return "/sns/topics" },
			func(x *Exec) (any, error) { return map[string]any{"name": x.Str("name")}, nil }, req("name", "name")),
		listCmd("list-topics", "List topics", "/sns/topics", "", "", nil),
		simple("delete-topic", "Delete a topic", "DELETE", func(x *Exec) string { return "/sns/topics/" + topic(x) }, nil, tflags...),
		simple("subscribe", "Subscribe a queue or log group", "POST", func(x *Exec) string { return "/sns/topics/" + topic(x) + "/subscriptions" },
			func(x *Exec) (any, error) {
				return map[string]any{"protocol": x.Str("protocol"), "endpoint": endpointName(x.Str("notification-endpoint"))}, nil
			}, append(tflags, req("protocol", "sqs|log"), req("notification-endpoint", "queue name/URL or log group"))...),
		simple("publish", "Publish a message", "POST", func(x *Exec) string { return "/sns/topics/" + topic(x) + "/publish" },
			func(x *Exec) (any, error) {
				return map[string]any{"message": x.Str("message"), "subject": x.Str("subject")}, nil
			},
			append(tflags, req("message", "message"), opt("subject", "subject"))...),
	)
}

func defaultStr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func endpointName(v string) string {
	switch {
	case strings.HasPrefix(v, "arn:"):
		return lastSeg(v, ":")
	case strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://"):
		return lastSeg(v, "/")
	}
	return v
}
