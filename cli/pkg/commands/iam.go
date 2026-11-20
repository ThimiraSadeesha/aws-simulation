package commands

import (
	"encoding/json"
	"strings"
)

// policyName accepts a policy name or an ARN (…:policy/Name).
func policyName(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 && strings.HasPrefix(s, "arn:") {
		return s[i+1:]
	}
	return s
}

// jsonDoc reads an inline JSON document or file:// reference and validates it is JSON.
func (x *Exec) jsonDoc(flag string) (json.RawMessage, error) {
	v, err := x.Value(flag)
	if err != nil {
		return nil, err
	}
	if !json.Valid([]byte(v)) {
		return nil, usagef("--%s must be a JSON document or file://path", flag)
	}
	return json.RawMessage(v), nil
}

func attach(kind, flag string, detach bool) func(x *Exec) (any, error) {
	return func(x *Exec) (any, error) {
		m := "POST"
		if detach {
			m = "DELETE"
		}
		_, err := x.Call(m, "/iam/"+kind+"/"+esc(x.Str(flag))+"/attached-policies/"+esc(policyName(x.Str("policy-arn"))), nil)
		return nil, err
	}
}

func putInline(kind, flag string) func(x *Exec) (any, error) {
	return func(x *Exec) (any, error) {
		doc, err := x.jsonDoc("policy-document")
		if err != nil {
			return nil, err
		}
		_, _, err = x.Raw("PUT", "/iam/"+kind+"/"+esc(x.Str(flag))+"/inline-policies/"+esc(x.Str("policy-name")), doc, nil)
		return nil, err
	}
}

func init() {
	user := req("user-name", "IAM user name")
	group := req("group-name", "group name")
	role := req("role-name", "role name")
	pol := req("policy-arn", "policy ARN or name (e.g. CloudSimEC2FullAccess)")
	doc := req("policy-document", "JSON or file://policy.json")
	userCols := []string{"user_name", "arn", "path", "created_at"}
	register("iam", "simulated identity and access management",
		listCmd("list-users", "List IAM users", "/iam/users", "", "", userCols),
		simple("create-user", "Create an IAM user", "POST", func(x *Exec) string { return "/iam/users" },
			func(x *Exec) (any, error) {
				return map[string]any{"user_name": x.Str("user-name"), "path": x.Str("path")}, nil
			}, user, opt("path", "path, e.g. /dev/")),
		simple("get-user", "Show an IAM user with groups, policies and keys", "GET", func(x *Exec) string { return "/iam/users/" + esc(x.Str("user-name")) }, nil, user),
		deleteCmd("delete-user", "Delete an IAM user", "user-name", "/iam/users/"),
		simple("create-access-key", "Create an access key (the secret is shown once)", "POST", func(x *Exec) string { return "/iam/users/" + esc(x.Str("user-name")) + "/access-keys" }, nil, user),
		&Command{Name: "list-access-keys", Summary: "List a user's access keys", Flags: []Flag{user}, Columns: []string{"access_key_id", "status", "created_at"},
			Run: func(x *Exec) (any, error) {
				return x.Call("GET", "/iam/users/"+esc(x.Str("user-name"))+"/access-keys", nil)
			}},
		&Command{Name: "delete-access-key", Summary: "Delete an access key", Flags: []Flag{user, req("access-key-id", "key id")}, Run: func(x *Exec) (any, error) {
			_, err := x.Call("DELETE", "/iam/users/"+esc(x.Str("user-name"))+"/access-keys/"+esc(x.Str("access-key-id")), nil)
			return nil, err
		}},
		&Command{Name: "update-access-key", Summary: "Activate or deactivate an access key", Flags: []Flag{user, req("access-key-id", "key id"), req("status", "Active|Inactive")},
			Run: func(x *Exec) (any, error) {
				return x.Call("PATCH", "/iam/users/"+esc(x.Str("user-name"))+"/access-keys/"+esc(x.Str("access-key-id")), map[string]any{"status": x.Str("status")})
			}},
		&Command{Name: "attach-user-policy", Summary: "Attach a managed policy to a user", Flags: []Flag{user, pol}, Run: attach("users", "user-name", false)},
		&Command{Name: "detach-user-policy", Summary: "Detach a managed policy from a user", Flags: []Flag{user, pol}, Run: attach("users", "user-name", true)},
		&Command{Name: "put-user-policy", Summary: "Add or replace an inline user policy", Flags: []Flag{user, req("policy-name", "inline policy name"), doc}, Run: putInline("users", "user-name")},
		&Command{Name: "delete-user-policy", Summary: "Delete an inline user policy", Flags: []Flag{user, req("policy-name", "inline policy name")}, Run: func(x *Exec) (any, error) {
			_, err := x.Call("DELETE", "/iam/users/"+esc(x.Str("user-name"))+"/inline-policies/"+esc(x.Str("policy-name")), nil)
			return nil, err
		}},
		listCmd("list-policies", "List managed policies", "/iam/policies", "", "", []string{"policy_name", "arn", "is_builtin", "description"}),
		&Command{Name: "create-policy", Summary: "Create a managed policy", Flags: []Flag{req("policy-name", "policy name"), doc, opt("description", "description")},
			Run: func(x *Exec) (any, error) {
				d, err := x.jsonDoc("policy-document")
				if err != nil {
					return nil, err
				}
				return x.Call("POST", "/iam/policies", map[string]any{"policy_name": x.Str("policy-name"), "policy_document": d, "description": x.Str("description")})
			}},
		simple("get-policy", "Show a managed policy", "GET", func(x *Exec) string { return "/iam/policies/" + esc(policyName(x.Str("policy-arn"))) }, nil, pol),
		&Command{Name: "delete-policy", Summary: "Delete a managed policy", Flags: []Flag{pol}, Run: func(x *Exec) (any, error) {
			_, err := x.Call("DELETE", "/iam/policies/"+esc(policyName(x.Str("policy-arn"))), nil)
			return nil, err
		}},
		listCmd("list-roles", "List roles", "/iam/roles", "", "", []string{"role_name", "arn", "description"}),
		&Command{Name: "create-role", Summary: "Create a role", Flags: []Flag{role, req("assume-role-policy-document", "trust policy JSON or file://"), opt("description", "description"),
			Flag{Name: "max-session-duration", Kind: Int, Help: "seconds"}},
			Run: func(x *Exec) (any, error) {
				d, err := x.jsonDoc("assume-role-policy-document")
				if err != nil {
					return nil, err
				}
				return x.Call("POST", "/iam/roles", map[string]any{"role_name": x.Str("role-name"), "assume_role_policy_document": d,
					"description": x.Str("description"), "max_session_duration": x.Int("max-session-duration", 0)})
			}},
		deleteCmd("delete-role", "Delete a role", "role-name", "/iam/roles/"),
		&Command{Name: "attach-role-policy", Summary: "Attach a managed policy to a role", Flags: []Flag{role, pol}, Run: attach("roles", "role-name", false)},
		&Command{Name: "detach-role-policy", Summary: "Detach a managed policy from a role", Flags: []Flag{role, pol}, Run: attach("roles", "role-name", true)},
		&Command{Name: "put-role-policy", Summary: "Add or replace an inline role policy", Flags: []Flag{role, req("policy-name", "name"), doc}, Run: putInline("roles", "role-name")},
		listCmd("list-groups", "List groups", "/iam/groups", "", "", []string{"group_name", "arn", "created_at"}),
		simple("create-group", "Create a group", "POST", func(x *Exec) string { return "/iam/groups" },
			func(x *Exec) (any, error) { return map[string]any{"group_name": x.Str("group-name")}, nil }, group),
		simple("get-group", "Show a group and its members", "GET", func(x *Exec) string { return "/iam/groups/" + esc(x.Str("group-name")) }, nil, group),
		deleteCmd("delete-group", "Delete a group", "group-name", "/iam/groups/"),
		&Command{Name: "add-user-to-group", Summary: "Add a user to a group", Flags: []Flag{group, user}, Run: func(x *Exec) (any, error) {
			_, err := x.Call("POST", "/iam/groups/"+esc(x.Str("group-name"))+"/members/"+esc(x.Str("user-name")), nil)
			return nil, err
		}},
		&Command{Name: "remove-user-from-group", Summary: "Remove a user from a group", Flags: []Flag{group, user}, Run: func(x *Exec) (any, error) {
			_, err := x.Call("DELETE", "/iam/groups/"+esc(x.Str("group-name"))+"/members/"+esc(x.Str("user-name")), nil)
			return nil, err
		}},
		&Command{Name: "attach-group-policy", Summary: "Attach a managed policy to a group", Flags: []Flag{group, pol}, Run: attach("groups", "group-name", false)},
		&Command{Name: "detach-group-policy", Summary: "Detach a managed policy from a group", Flags: []Flag{group, pol}, Run: attach("groups", "group-name", true)},
		&Command{Name: "put-group-policy", Summary: "Add or replace an inline group policy", Flags: []Flag{group, req("policy-name", "name"), doc}, Run: putInline("groups", "group-name")},
		&Command{Name: "simulate-principal-policy", Summary: "Evaluate whether a user or role may perform actions",
			Flags:   []Flag{req("policy-source-arn", "user/role ARN"), {Name: "action-names", Kind: List, Required: true, Help: "e.g. ec2:RunInstances s3:GetObject"}, {Name: "resource-arns", Kind: List, Help: "resource ARNs (default *)"}},
			Columns: []string{"action", "resource", "decision"},
			Run: func(x *Exec) (any, error) {
				res := x.List("resource-arns")
				if len(res) == 0 {
					res = []string{"*"}
				}
				var out []any
				for _, a := range x.List("action-names") {
					for _, r := range res {
						v, err := x.Call("POST", "/iam/simulate", map[string]any{"principal_arn": x.Str("policy-source-arn"), "action": a, "resource_arn": r})
						if err != nil {
							return nil, err
						}
						m, _ := v.(map[string]any)
						out = append(out, map[string]any{"action": a, "resource": r, "decision": m["decision"], "matched_statements": m["matched_statements"]})
					}
				}
				return map[string]any{"evaluation_results": out}, nil
			}},
	)
	register("sts", "simulated security token service",
		simple("get-caller-identity", "Show who you are", "GET", func(x *Exec) string { return "/sts/caller-identity" }, nil),
		simple("assume-role", "Get temporary credentials for a role", "POST", func(x *Exec) string { return "/sts/assume-role" },
			func(x *Exec) (any, error) {
				return map[string]any{"role_arn": x.Str("role-arn"), "role_session_name": x.Str("role-session-name"), "duration_seconds": x.Int("duration-seconds", 0)}, nil
			}, req("role-arn", "role ARN"), req("role-session-name", "session name"), Flag{Name: "duration-seconds", Kind: Int, Help: "lifetime"}),
	)
}
