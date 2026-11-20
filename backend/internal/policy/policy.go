// Package policy implements the IAM-style policy language: parsing, validation and evaluation.
//
// Evaluation logic (a documented subset of AWS's):
//  1. Any matching Deny statement in any applicable policy ⇒ explicitDeny.
//  2. Otherwise any matching Allow in an identity policy or a resource policy ⇒ allowed.
//  3. Otherwise ⇒ implicitDeny.
//
// Permission boundaries, SCPs and session policies are not modeled.
package policy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// StringList accepts either a JSON string or an array of strings.
type StringList []string

func (s *StringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*s = StringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("expected string or array of strings")
	}
	*s = many
	return nil
}

// PrincipalSpec is the Principal element of resource/trust policies.
// "*" is decoded as Any=true; {"CloudSim": [...]} / {"AWS": [...]} / {"Service": [...]} as Values.
type PrincipalSpec struct {
	Any    bool
	Values map[string]StringList
}

func (p *PrincipalSpec) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if s != "*" {
			return fmt.Errorf(`Principal string must be "*"`)
		}
		p.Any = true
		return nil
	}
	m := map[string]StringList{}
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("invalid Principal: %v", err)
	}
	p.Values = m
	return nil
}

func (p PrincipalSpec) MarshalJSON() ([]byte, error) {
	if p.Any {
		return json.Marshal("*")
	}
	return json.Marshal(p.Values)
}

// Matches reports whether principalARN is named by this spec.
func (p *PrincipalSpec) Matches(principalARN, accountID string) bool {
	if p == nil {
		return false
	}
	if p.Any {
		return true
	}
	for typ, vals := range p.Values {
		for _, v := range vals {
			if v == "*" {
				return true
			}
			switch typ {
			case "CloudSim", "AWS":
				if v == principalARN || wildcardMatch(v, principalARN, false) {
					return true
				}
				// Account principal: a bare account id or arn:...:root names every principal in that account.
				if (v == accountID || v == "arn:cloudsim:iam::"+accountID+":root") && accountID != "" &&
					strings.Contains(principalARN, ":"+accountID+":") {
					return true
				}
			}
		}
	}
	return false
}

type Statement struct {
	Sid         string                           `json:"Sid,omitempty"`
	Effect      string                           `json:"Effect"`
	Principal   *PrincipalSpec                   `json:"Principal,omitempty"`
	Action      StringList                       `json:"Action,omitempty"`
	NotAction   StringList                       `json:"NotAction,omitempty"`
	Resource    StringList                       `json:"Resource,omitempty"`
	NotResource StringList                       `json:"NotResource,omitempty"`
	Condition   map[string]map[string]StringList `json:"Condition,omitempty"`
}

type Document struct {
	Version   string      `json:"Version"`
	Statement []Statement `json:"Statement"`
}

// UnmarshalJSON accepts Statement as a single object or an array.
func (d *Document) UnmarshalJSON(b []byte) error {
	var raw struct {
		Version   string          `json:"Version"`
		Statement json.RawMessage `json:"Statement"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	d.Version = raw.Version
	if len(raw.Statement) == 0 {
		return nil
	}
	if raw.Statement[0] == '{' {
		var s Statement
		if err := json.Unmarshal(raw.Statement, &s); err != nil {
			return err
		}
		d.Statement = []Statement{s}
		return nil
	}
	return json.Unmarshal(raw.Statement, &d.Statement)
}

// Parse decodes a policy document from JSON text.
func Parse(text string) (*Document, error) {
	var d Document
	dec := json.NewDecoder(strings.NewReader(text))
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("policy is not valid JSON: %v", err)
	}
	return &d, nil
}

// Kind distinguishes validation rules.
type Kind int

const (
	Identity Kind = iota // attached to users/groups/roles: Principal forbidden, Resource required
	Resource             // bucket policies: Principal required, Resource required
	Trust                // role trust policies: Principal required, Resource forbidden
)

var actionRe = regexp.MustCompile(`^(\*|[a-z0-9-]+:[A-Za-z0-9*?]+)$`)

var conditionOps = map[string]bool{
	"StringEquals": true, "StringNotEquals": true, "StringEqualsIgnoreCase": true,
	"StringLike": true, "StringNotLike": true, "Bool": true,
	"NumericEquals": true, "NumericLessThan": true, "NumericGreaterThan": true,
	"NumericLessThanEquals": true, "NumericGreaterThanEquals": true,
}

// Validate returns a list of human-readable problems (empty = valid).
func Validate(text string, kind Kind) []string {
	d, err := Parse(text)
	if err != nil {
		return []string{err.Error()}
	}
	var errs []string
	if d.Version != "2012-10-17" && d.Version != "2008-10-17" {
		errs = append(errs, `Version must be "2012-10-17"`)
	}
	if len(d.Statement) == 0 {
		errs = append(errs, "Statement must contain at least one statement")
	}
	sids := map[string]bool{}
	for i, s := range d.Statement {
		at := fmt.Sprintf("Statement[%d]", i)
		if s.Sid != "" {
			if sids[s.Sid] {
				errs = append(errs, at+": duplicate Sid "+s.Sid)
			}
			sids[s.Sid] = true
		}
		if s.Effect != "Allow" && s.Effect != "Deny" {
			errs = append(errs, at+`: Effect must be "Allow" or "Deny"`)
		}
		if (len(s.Action) == 0) == (len(s.NotAction) == 0) {
			errs = append(errs, at+": exactly one of Action or NotAction is required")
		}
		for _, a := range append(append([]string{}, s.Action...), s.NotAction...) {
			if !actionRe.MatchString(a) {
				errs = append(errs, fmt.Sprintf("%s: invalid action %q (expected service:Operation)", at, a))
			}
		}
		hasRes := len(s.Resource) > 0 || len(s.NotResource) > 0
		if len(s.Resource) > 0 && len(s.NotResource) > 0 {
			errs = append(errs, at+": use only one of Resource or NotResource")
		}
		switch kind {
		case Identity:
			if s.Principal != nil {
				errs = append(errs, at+": Principal is not allowed in identity-based policies")
			}
			if !hasRes {
				errs = append(errs, at+": Resource is required")
			}
		case Resource:
			if s.Principal == nil {
				errs = append(errs, at+": Principal is required in resource-based policies")
			}
			if !hasRes {
				errs = append(errs, at+": Resource is required")
			}
		case Trust:
			if s.Principal == nil {
				errs = append(errs, at+": Principal is required in trust policies")
			}
			if hasRes {
				errs = append(errs, at+": Resource is not allowed in trust policies")
			}
		}
		for op := range s.Condition {
			if !conditionOps[op] {
				errs = append(errs, fmt.Sprintf("%s: unsupported condition operator %q", at, op))
			}
		}
	}
	return errs
}

// Request is an authorization question.
type Request struct {
	PrincipalARN string
	AccountID    string
	Action       string
	Resource     string
	Context      map[string]string // condition keys, e.g. cloudsim:username, s3:prefix
}

// Named is a policy plus a label used in explanations.
type Named struct {
	Name     string
	Document *Document
}

type Decision string

const (
	Allowed      Decision = "allowed"
	ExplicitDeny Decision = "explicitDeny"
	ImplicitDeny Decision = "implicitDeny"
)

type Match struct {
	Policy string `json:"policy"`
	Sid    string `json:"sid"`
	Effect string `json:"effect"`
}

type Result struct {
	Decision Decision `json:"decision"`
	Matched  []Match  `json:"matched_statements"`
}

func (r Result) Allowed() bool { return r.Decision == Allowed }

// Evaluate applies identity policies and (optionally) resource policies to req.
func Evaluate(req Request, identity []Named, resource []Named) Result {
	var allows, denies []Match
	check := func(pols []Named, isResource bool) {
		for _, p := range pols {
			if p.Document == nil {
				continue
			}
			for _, s := range p.Document.Statement {
				if isResource && !s.Principal.Matches(req.PrincipalARN, req.AccountID) {
					continue
				}
				if !statementMatches(s, req) {
					continue
				}
				m := Match{Policy: p.Name, Sid: s.Sid, Effect: s.Effect}
				if s.Effect == "Deny" {
					denies = append(denies, m)
				} else if s.Effect == "Allow" {
					allows = append(allows, m)
				}
			}
		}
	}
	check(identity, false)
	check(resource, true)
	switch {
	case len(denies) > 0:
		return Result{Decision: ExplicitDeny, Matched: denies}
	case len(allows) > 0:
		return Result{Decision: Allowed, Matched: allows}
	default:
		return Result{Decision: ImplicitDeny, Matched: []Match{}}
	}
}

// EvaluateTrust decides whether principalARN may assume a role with the given trust policy.
func EvaluateTrust(trust *Document, principalARN, accountID string) bool {
	req := Request{PrincipalARN: principalARN, AccountID: accountID, Action: "sts:AssumeRole"}
	allowed := false
	for _, s := range trust.Statement {
		if !s.Principal.Matches(principalARN, accountID) || !actionMatches(s, req.Action) {
			continue
		}
		if !conditionsMatch(s.Condition, req) {
			continue
		}
		if s.Effect == "Deny" {
			return false
		}
		allowed = true
	}
	return allowed
}

func statementMatches(s Statement, req Request) bool {
	if !actionMatches(s, req.Action) {
		return false
	}
	if len(s.Resource) > 0 && !anyMatch(s.Resource, req, false) {
		return false
	}
	if len(s.NotResource) > 0 && anyMatch(s.NotResource, req, false) {
		return false
	}
	return conditionsMatch(s.Condition, req)
}

func actionMatches(s Statement, action string) bool {
	if len(s.Action) > 0 {
		for _, a := range s.Action {
			if wildcardMatch(a, action, true) {
				return true
			}
		}
		return false
	}
	for _, a := range s.NotAction {
		if wildcardMatch(a, action, true) {
			return false
		}
	}
	return true
}

func anyMatch(patterns []string, req Request, fold bool) bool {
	for _, p := range patterns {
		if wildcardMatch(substitute(p, req), req.Resource, fold) {
			return true
		}
	}
	return false
}

// substitute expands policy variables like ${aws:username} / ${cloudsim:username}.
func substitute(p string, req Request) string {
	if !strings.Contains(p, "${") {
		return p
	}
	for k, v := range req.Context {
		p = strings.ReplaceAll(p, "${"+k+"}", v)
		if strings.HasPrefix(k, "cloudsim:") {
			p = strings.ReplaceAll(p, "${aws:"+strings.TrimPrefix(k, "cloudsim:")+"}", v)
		}
	}
	return p
}

func ctxValue(req Request, key string) (string, bool) {
	if v, ok := req.Context[key]; ok {
		return v, true
	}
	if strings.HasPrefix(key, "aws:") {
		v, ok := req.Context["cloudsim:"+strings.TrimPrefix(key, "aws:")]
		return v, ok
	}
	return "", false
}

func conditionsMatch(cond map[string]map[string]StringList, req Request) bool {
	// Deterministic order for explainability.
	ops := make([]string, 0, len(cond))
	for op := range cond {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	for _, op := range ops {
		for key, vals := range cond[op] {
			actual, present := ctxValue(req, key)
			if !conditionHolds(op, actual, present, vals, req) {
				return false
			}
		}
	}
	return true
}

func conditionHolds(op, actual string, present bool, vals []string, req Request) bool {
	negated := strings.Contains(op, "Not")
	if !present {
		return negated
	}
	any := func(f func(string) bool) bool {
		for _, v := range vals {
			if f(substitute(v, req)) {
				return true
			}
		}
		return false
	}
	switch op {
	case "StringEquals":
		return any(func(v string) bool { return v == actual })
	case "StringNotEquals":
		return !any(func(v string) bool { return v == actual })
	case "StringEqualsIgnoreCase":
		return any(func(v string) bool { return strings.EqualFold(v, actual) })
	case "StringLike":
		return any(func(v string) bool { return wildcardMatch(v, actual, false) })
	case "StringNotLike":
		return !any(func(v string) bool { return wildcardMatch(v, actual, false) })
	case "Bool":
		return any(func(v string) bool { return strings.EqualFold(v, actual) })
	}
	a, err := strconv.ParseFloat(actual, 64)
	if err != nil {
		return false
	}
	return any(func(v string) bool {
		b, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return false
		}
		switch op {
		case "NumericEquals":
			return a == b
		case "NumericLessThan":
			return a < b
		case "NumericGreaterThan":
			return a > b
		case "NumericLessThanEquals":
			return a <= b
		case "NumericGreaterThanEquals":
			return a >= b
		}
		return false
	})
}

// wildcardMatch matches s against pattern containing * (any run) and ? (one char).
func wildcardMatch(pattern, s string, fold bool) bool {
	if fold {
		pattern, s = strings.ToLower(pattern), strings.ToLower(s)
	}
	p, i := 0, 0
	star, mark := -1, 0
	for i < len(s) {
		switch {
		case p < len(pattern) && (pattern[p] == '?' || pattern[p] == s[i]):
			p++
			i++
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, i
			p++
		case star >= 0:
			p = star + 1
			mark++
			i = mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// WildcardMatch is exported for other packages (e.g. ARN matching in lab validators).
func WildcardMatch(pattern, s string) bool { return wildcardMatch(pattern, s, false) }
