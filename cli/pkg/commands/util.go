package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode/utf8"
)

// Split tokenizes a command line like a POSIX shell would (quotes and backslash escapes) without
// any expansion. It never executes anything.
func Split(line string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inTok := false
	var quote rune
	esc := false
	for _, r := range line {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				esc = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			esc, inTok = true, true
		case r == '\'' || r == '"':
			quote, inTok = r, true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inTok {
				out = append(out, cur.String())
				cur.Reset()
				inTok = false
			}
		default:
			cur.WriteRune(r)
			inTok = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if esc {
		return nil, errors.New("trailing backslash")
	}
	if inTok {
		out = append(out, cur.String())
	}
	return out, nil
}

var shortKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// Shorthand parses AWS shorthand "Name=x,Values=a,b" into key -> values.
func Shorthand(s string) map[string][]string {
	out := map[string][]string{}
	last := ""
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, "=")
		if ok && shortKeyRe.MatchString(k) {
			last = k
			out[k] = append(out[k], v)
			continue
		}
		if last != "" {
			out[last] = append(out[last], part)
		}
	}
	return out
}

// Filter is a describe-* filter.
type Filter struct {
	Name   string
	Values []string
}

// ParseFilters accepts shorthand tokens or a JSON array.
func ParseFilters(vals []string) ([]Filter, error) {
	var out []Filter
	for _, v := range vals {
		if strings.HasPrefix(strings.TrimSpace(v), "[") {
			var arr []struct {
				Name   string   `json:"Name"`
				Values []string `json:"Values"`
			}
			if err := json.Unmarshal([]byte(v), &arr); err != nil {
				return nil, usagef("invalid --filters JSON: %v", err)
			}
			for _, f := range arr {
				out = append(out, Filter{f.Name, f.Values})
			}
			continue
		}
		m := Shorthand(v)
		if len(m["Name"]) != 1 || len(m["Values"]) == 0 {
			return nil, usagef("invalid filter %q; expected Name=<name>,Values=<v1>,<v2>", v)
		}
		out = append(out, Filter{m["Name"][0], m["Values"]})
	}
	return out, nil
}

var filterFields = map[string]string{
	"instance-state-name": "state", "instance-id": "instance_id", "group-id": "group_id", "key-name": "key_name",
	"cidr": "cidr_block", "cidr-block": "cidr_block", "isDefault": "is_default", "default-for-az": "is_default",
}

// filterField maps an AWS-style filter name to the JSON field path of CloudSim objects.
func filterField(name string) string {
	if strings.HasPrefix(name, "tag:") {
		return "tags." + strings.TrimPrefix(name, "tag:")
	}
	if f, ok := filterFields[name]; ok {
		return f
	}
	return strings.ReplaceAll(name, "-", "_")
}

func applyFilters(items []any, fs []Filter) []any {
	if len(fs) == 0 {
		return items
	}
	var out []any
	for _, it := range items {
		ok := true
		for _, f := range fs {
			vals := Lookup1(it, filterField(f.Name))
			hit := false
			for _, got := range vals {
				for _, want := range f.Values {
					if m, _ := path.Match(want, scalarString(got)); m || want == scalarString(got) {
						hit = true
					}
				}
			}
			if !hit {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	return out
}

func filterIDs(items []any, field string, ids []string) []any {
	if len(ids) == 0 {
		return items
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []any
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && want[scalarString(m[field])] {
			out = append(out, it)
		}
	}
	return out
}

var tagPairRe = regexp.MustCompile(`\{\s*Key=([^,}]*),\s*Value=([^}]*)\}`)

// ParseTags parses "Key=k,Value=v" tokens, tag-specifications shorthand
// "ResourceType=instance,Tags=[{Key=Name,Value=web}]" or JSON.
func ParseTags(vals []string) (map[string]string, error) {
	tags := map[string]string{}
	for _, v := range vals {
		t := strings.TrimSpace(v)
		if strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{") {
			var specs []struct {
				Key   string `json:"Key"`
				Value string `json:"Value"`
				Tags  []struct {
					Key   string `json:"Key"`
					Value string `json:"Value"`
				} `json:"Tags"`
			}
			if !strings.HasPrefix(t, "[") {
				t = "[" + t + "]"
			}
			if err := json.Unmarshal([]byte(t), &specs); err != nil {
				return nil, usagef("invalid tags JSON: %v", err)
			}
			for _, s := range specs {
				if s.Key != "" {
					tags[s.Key] = s.Value
				}
				for _, tg := range s.Tags {
					tags[tg.Key] = tg.Value
				}
			}
			continue
		}
		if ms := tagPairRe.FindAllStringSubmatch(t, -1); len(ms) > 0 {
			for _, m := range ms {
				tags[strings.TrimSpace(m[1])] = strings.TrimSpace(m[2])
			}
			continue
		}
		m := Shorthand(t)
		if len(m["Key"]) == 1 {
			tags[m["Key"][0]] = strings.Join(m["Value"], ",")
			continue
		}
		if k, val, ok := strings.Cut(t, "="); ok && k != "ResourceType" {
			tags[k] = val
			continue
		}
		return nil, usagef("invalid tag %q; expected Key=<k>,Value=<v>", v)
	}
	return tags, nil
}

// ---- --query

// Query evaluates a simple dotted path: a.b, a[0], a[], a[].b, [0].
func Query(v any, q string) (any, error) {
	q = strings.TrimSpace(q)
	if q == "" || q == "@" {
		return v, nil
	}
	segs, err := querySegments(q)
	if err != nil {
		return nil, err
	}
	return evalQuery(v, segs), nil
}

type qseg struct {
	field   string
	index   *int
	project bool
}

func querySegments(q string) ([]qseg, error) {
	var segs []qseg
	for _, part := range strings.Split(q, ".") {
		if part == "" {
			return nil, usagef("invalid --query %q", q)
		}
		name := part
		var idxs []string
		if i := strings.Index(part, "["); i >= 0 {
			name = part[:i]
			rest := part[i:]
			for rest != "" {
				if !strings.HasPrefix(rest, "[") {
					return nil, usagef("invalid --query %q", q)
				}
				j := strings.Index(rest, "]")
				if j < 0 {
					return nil, usagef("invalid --query %q: missing ]", q)
				}
				idxs = append(idxs, rest[1:j])
				rest = rest[j+1:]
			}
		}
		if name != "" {
			segs = append(segs, qseg{field: name})
		}
		for _, ix := range idxs {
			if ix == "" || ix == "*" {
				segs = append(segs, qseg{project: true})
				continue
			}
			n, err := strconv.Atoi(ix)
			if err != nil {
				return nil, usagef("invalid index %q in --query", ix)
			}
			segs = append(segs, qseg{index: &n})
		}
	}
	return segs, nil
}

func evalQuery(v any, segs []qseg) any {
	for i, s := range segs {
		switch {
		case s.project:
			arr, ok := v.([]any)
			if !ok {
				return nil
			}
			out := []any{}
			for _, e := range arr {
				r := evalQuery(e, segs[i+1:])
				if r == nil {
					continue
				}
				if sub, ok := r.([]any); ok && hasProject(segs[i+1:]) {
					out = append(out, sub...)
				} else {
					out = append(out, r)
				}
			}
			return out
		case s.index != nil:
			arr, ok := v.([]any)
			if !ok {
				return nil
			}
			n := *s.index
			if n < 0 {
				n += len(arr)
			}
			if n < 0 || n >= len(arr) {
				return nil
			}
			v = arr[n]
		default:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[s.field]
		}
	}
	return v
}

func hasProject(segs []qseg) bool {
	for _, s := range segs {
		if s.project {
			return true
		}
	}
	return false
}

// Lookup1 resolves a dotted field path returning all matching values (arrays fan out).
func Lookup1(v any, p string) []any {
	cur := []any{v}
	for _, part := range strings.Split(p, ".") {
		var next []any
		for _, c := range cur {
			switch t := c.(type) {
			case map[string]any:
				if x, ok := t[part]; ok && x != nil {
					next = append(next, x)
				}
			case []any:
				for _, e := range t {
					if m, ok := e.(map[string]any); ok {
						if x, ok := m[part]; ok && x != nil {
							next = append(next, x)
						}
					}
				}
			}
		}
		cur = next
	}
	var out []any
	for _, c := range cur {
		if arr, ok := c.([]any); ok {
			out = append(out, arr...)
		} else {
			out = append(out, c)
		}
	}
	return out
}

// ---- output formatting

// Format writes v as json, table or text after applying query.
func Format(w io.Writer, v any, format, query string, columns []string) error {
	if query != "" {
		var err error
		if v, err = Query(v, query); err != nil {
			return err
		}
		columns = nil
	}
	switch format {
	case "table":
		writeTable(w, v, columns)
	case "text":
		writeText(w, v, columns)
	default:
		b, err := json.MarshalIndent(v, "", "    ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(b))
	}
	return nil
}

func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case map[string]any, []any:
		b, _ := json.Marshal(t)
		return string(b)
	}
	return fmt.Sprint(v)
}

func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	}
	return true
}

// rowsOf returns the list of objects to tabulate: an array, a map's "items", or its only array field.
func rowsOf(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case map[string]any:
		if items, ok := t["items"].([]any); ok {
			return items, true
		}
		var found []any
		n := 0
		for _, val := range t {
			if arr, ok := val.([]any); ok {
				found = arr
				n++
			}
		}
		if n == 1 && len(t) <= 2 {
			return found, true
		}
	}
	return nil, false
}

func autoColumns(rows []any) []string {
	seen := map[string]bool{}
	var cols []string
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range m {
			if !seen[k] && isScalar(v) {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	rank := func(k string) int {
		switch {
		case strings.HasSuffix(k, "_id") || k == "id":
			return 0
		case strings.Contains(k, "name"):
			return 1
		case k == "state" || k == "status":
			return 2
		}
		return 3
	}
	sort.Slice(cols, func(i, j int) bool {
		if rank(cols[i]) != rank(cols[j]) {
			return rank(cols[i]) < rank(cols[j])
		}
		return cols[i] < cols[j]
	})
	if len(cols) > 8 {
		cols = cols[:8]
	}
	return cols
}

func cell(m map[string]any, col string) string {
	vals := Lookup1(m, col)
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, scalarString(v))
	}
	s := strings.Join(parts, ",")
	if utf8.RuneCountInString(s) > 60 {
		s = string([]rune(s)[:57]) + "..."
	}
	return strings.ReplaceAll(s, "\n", " ")
}

func writeTable(w io.Writer, v any, columns []string) {
	if rows, ok := rowsOf(v); ok {
		if len(rows) == 0 {
			fmt.Fprintln(w, "(no results)")
			return
		}
		if _, isObj := rows[0].(map[string]any); !isObj {
			for _, r := range rows {
				fmt.Fprintln(w, scalarString(r))
			}
			return
		}
		cols := columns
		if len(cols) == 0 {
			cols = autoColumns(rows)
		}
		data := [][]string{cols}
		for _, r := range rows {
			m, _ := r.(map[string]any)
			line := make([]string, len(cols))
			for i, c := range cols {
				line[i] = cell(m, c)
			}
			data = append(data, line)
		}
		drawTable(w, data, true)
		return
	}
	if m, ok := v.(map[string]any); ok {
		data := [][]string{{"field", "value"}}
		for _, k := range keys(m) {
			data = append(data, []string{k, cell(m, k)})
		}
		drawTable(w, data, true)
		return
	}
	fmt.Fprintln(w, scalarString(v))
}

func drawTable(w io.Writer, data [][]string, header bool) {
	widths := make([]int, len(data[0]))
	for _, r := range data {
		for i, c := range r {
			widths[i] = max(widths[i], utf8.RuneCountInString(c))
		}
	}
	sep := "+"
	for _, wd := range widths {
		sep += strings.Repeat("-", wd+2) + "+"
	}
	fmt.Fprintln(w, sep)
	for ri, r := range data {
		line := "|"
		for i, c := range r {
			line += " " + c + strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)) + " |"
		}
		fmt.Fprintln(w, line)
		if ri == 0 && header {
			fmt.Fprintln(w, sep)
		}
	}
	fmt.Fprintln(w, sep)
}

func writeText(w io.Writer, v any, columns []string) {
	tw := tabwriter.NewWriter(w, 0, 0, 1, ' ', 0)
	defer tw.Flush()
	if rows, ok := rowsOf(v); ok {
		cols := columns
		if len(cols) == 0 {
			cols = autoColumns(rows)
		}
		for _, r := range rows {
			m, isObj := r.(map[string]any)
			if !isObj {
				fmt.Fprintln(tw, scalarString(r))
				continue
			}
			line := make([]string, len(cols))
			for i, c := range cols {
				line[i] = cell(m, c)
			}
			fmt.Fprintln(tw, strings.Join(line, "\t"))
		}
		return
	}
	switch t := v.(type) {
	case map[string]any:
		for _, k := range keys(t) {
			fmt.Fprintf(tw, "%s\t%s\n", strings.ToUpper(k), scalarString(t[k]))
		}
	default:
		fmt.Fprintln(tw, scalarString(v))
	}
}
