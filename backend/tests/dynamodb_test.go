package tests

import (
	"net/url"
	"testing"

	"cloudsim/backend/internal/testutil"
)

func TestDynamoDBTablesAndKeyValidation(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	for _, bad := range []map[string]any{
		{"table_name": "ab", "partition_key": map[string]string{"name": "id", "type": "S"}},
		{"table_name": "users"},
		{"table_name": "users", "partition_key": map[string]string{"name": "id", "type": "B"}},
		{"table_name": "users", "partition_key": map[string]string{"name": "id", "type": "S"}, "sort_key": map[string]string{"name": "id", "type": "S"}},
	} {
		if r := c.Do("POST", "/api/v1/dynamodb/tables", bad); r.Status != 400 {
			t.Errorf("%v: %d %s", bad, r.Status, r.Body)
		}
	}
	tb := c.Must(201, "POST", "/api/v1/dynamodb/tables", map[string]any{"table_name": "users", "partition_key": map[string]string{"name": "id", "type": "S"}})
	if tb["status"] != "ACTIVE" || tb["sort_key"] != nil || h.Engine.Count("dynamodb_table") != 1 {
		t.Fatalf("table: %v", tb)
	}
	for _, bad := range []any{
		map[string]any{"name": "no key"}, map[string]any{"id": 5}, map[string]any{"id": ""}, []int{1},
	} {
		if r := c.Do("PUT", "/api/v1/dynamodb/tables/users/items", bad); r.Status != 400 {
			t.Errorf("item %v: %d %s", bad, r.Status, r.Body)
		}
	}
	c.Must(200, "PUT", "/api/v1/dynamodb/tables/users/items", map[string]any{"id": "u1", "name": "Ann", "age": 31, "tags": []string{"a"}})
	c.Must(200, "PUT", "/api/v1/dynamodb/tables/users/items", map[string]any{"id": "u1", "name": "Ann B", "age": 32})
	it := c.Must(200, "GET", "/api/v1/dynamodb/tables/users/items?pk=u1", nil)["item"].(map[string]any)
	if it["name"] != "Ann B" || it["age"].(float64) != 32 || it["tags"] != nil {
		t.Fatalf("put replaces item: %v", it)
	}
	if r := c.Do("GET", "/api/v1/dynamodb/tables/users/items?pk=u1&sk=x", nil); r.Status != 400 {
		t.Fatalf("sk on table without sort key: %d", r.Status)
	}
	if r := c.Do("GET", "/api/v1/dynamodb/tables/users/items?pk=nope", nil); r.Status != 404 {
		t.Fatalf("missing item: %d", r.Status)
	}
	d := c.Must(200, "GET", "/api/v1/dynamodb/tables/users", nil)
	if d["item_count"].(float64) != 1 || d["table_size_bytes"].(float64) <= 0 {
		t.Fatalf("stats: %v", d)
	}
	c.Must(204, "DELETE", "/api/v1/dynamodb/tables/users/items?pk=u1", nil)
	if r := h.Signup("bob").Do("GET", "/api/v1/dynamodb/tables/users", nil); r.Status != 404 {
		t.Fatalf("cross-account: %d", r.Status)
	}
	c.Must(204, "DELETE", "/api/v1/dynamodb/tables/users", nil)
	// Quota enforced by the engine on registration.
	h.Engine.Quota["dynamodb_table"] = 1
	c.Must(201, "POST", "/api/v1/dynamodb/tables", map[string]any{"table_name": "t1", "partition_key": map[string]string{"name": "id", "type": "S"}})
	if r := c.Do("POST", "/api/v1/dynamodb/tables", map[string]any{"table_name": "t2", "partition_key": map[string]string{"name": "id", "type": "S"}}); r.Status != 429 || r.ErrCode() != "QuotaExceeded" {
		t.Fatalf("quota: %d %s", r.Status, r.Body)
	}
}

func TestDynamoDBQueryOrderingAndScanPagination(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	c.Must(201, "POST", "/api/v1/dynamodb/tables", map[string]any{"table_name": "scores",
		"partition_key": map[string]string{"name": "player", "type": "S"}, "sort_key": map[string]string{"name": "score", "type": "N"}})
	for _, sc := range []float64{10, 9, 100, 2.5, -1} {
		c.Must(200, "PUT", "/api/v1/dynamodb/tables/scores/items", map[string]any{"player": "p1", "score": sc})
	}
	c.Must(200, "PUT", "/api/v1/dynamodb/tables/scores/items", map[string]any{"player": "p2", "score": 1})
	if r := c.Do("PUT", "/api/v1/dynamodb/tables/scores/items", map[string]any{"player": "p1", "score": "10"}); r.Status != 400 {
		t.Fatalf("N key given as string: %d", r.Status)
	}
	q := c.Must(200, "POST", "/api/v1/dynamodb/tables/scores/query", map[string]any{"pk": "p1"})
	want := []float64{-1, 2.5, 9, 10, 100}
	items := q["items"].([]any)
	if len(items) != len(want) {
		t.Fatalf("query: %v", q)
	}
	for i, w := range want {
		if items[i].(map[string]any)["score"].(float64) != w {
			t.Fatalf("numeric sort-key ordering: %v", items)
		}
	}
	// Paginated, descending.
	q = c.Must(200, "POST", "/api/v1/dynamodb/tables/scores/query", map[string]any{"pk": "p1", "scan_forward": false, "limit": 2})
	if q["items"].([]any)[0].(map[string]any)["score"].(float64) != 100 || q["last_evaluated_key"] == nil {
		t.Fatalf("desc page: %v", q)
	}
	q = c.Must(200, "POST", "/api/v1/dynamodb/tables/scores/query", map[string]any{"pk": "p1", "scan_forward": false, "limit": 2, "exclusive_start_key": q["last_evaluated_key"]})
	if q["items"].([]any)[0].(map[string]any)["score"].(float64) != 9 {
		t.Fatalf("desc page 2: %v", q)
	}
	if r := c.Do("POST", "/api/v1/dynamodb/tables/scores/query", map[string]any{"pk": "p1", "sk_begins_with": "1"}); r.Status != 400 {
		t.Fatalf("begins_with on N: %d", r.Status)
	}
	got := c.Must(200, "GET", "/api/v1/dynamodb/tables/scores/items?pk=p1&sk=2.50", nil)["item"].(map[string]any)
	if got["score"].(float64) != 2.5 {
		t.Fatalf("numeric key canonicalisation: %v", got)
	}

	// begins_with on S sort keys.
	c.Must(201, "POST", "/api/v1/dynamodb/tables", map[string]any{"table_name": "events",
		"partition_key": map[string]string{"name": "pk", "type": "S"}, "sort_key": map[string]string{"name": "sk", "type": "S"}})
	for _, sk := range []string{"2026-02#b", "2026-01#a", "2025-12#z", "2026-01#c"} {
		c.Must(200, "PUT", "/api/v1/dynamodb/tables/events/items", map[string]any{"pk": "user#1", "sk": sk})
	}
	q = c.Must(200, "POST", "/api/v1/dynamodb/tables/events/query", map[string]any{"pk": "user#1", "sk_begins_with": "2026-01"})
	if q["count"].(float64) != 2 || q["items"].([]any)[0].(map[string]any)["sk"] != "2026-01#a" {
		t.Fatalf("begins_with: %v", q)
	}

	// Scan pagination visits every item exactly once.
	seen := map[string]bool{}
	tok := ""
	pages := 0
	for {
		path := "/api/v1/dynamodb/tables/scores/scan?limit=4"
		if tok != "" {
			path += "&exclusive_start_key=" + url.QueryEscape(tok)
		}
		p := c.Must(200, "GET", path, nil)
		pages++
		for _, it := range p["items"].([]any) {
			m := it.(map[string]any)
			k := m["player"].(string) + "/" + url.QueryEscape(toStr(m["score"]))
			if seen[k] {
				t.Fatalf("duplicate in scan: %s", k)
			}
			seen[k] = true
		}
		if p["last_evaluated_key"] == nil {
			break
		}
		tok = p["last_evaluated_key"].(string)
	}
	if len(seen) != 6 || pages != 2 {
		t.Fatalf("scan: %d items in %d pages", len(seen), pages)
	}
	if r := c.Do("GET", "/api/v1/dynamodb/tables/scores/scan?limit=0x", nil); r.Status != 400 {
		t.Fatalf("bad limit: %d", r.Status)
	}
}

func toStr(v any) string {
	b, _ := jsonMarshal(v)
	return string(b)
}
