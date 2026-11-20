package tests

import (
	"encoding/json"
	"testing"

	"cloudsim/backend/internal/testutil"
)

func TestSNSFanOutToSQS(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	if r := c.Do("POST", "/api/v1/sns/topics", map[string]any{"name": "bad topic"}); r.Status != 400 {
		t.Fatalf("invalid name: %d", r.Status)
	}
	c.Must(201, "POST", "/api/v1/sns/topics", map[string]any{"name": "orders"})
	if h.Engine.Count("topic") != 1 {
		t.Fatal("topic should be registered with the engine")
	}
	c.Must(201, "POST", "/api/v1/sqs/queues", map[string]any{"name": "billing"})
	c.Must(201, "POST", "/api/v1/sqs/queues", map[string]any{"name": "shipping"})
	for _, bad := range []map[string]any{
		{"protocol": "http", "endpoint": "http://x"}, {"protocol": "sqs", "endpoint": "missing"}, {"protocol": "log", "endpoint": "bad group!"},
	} {
		if r := c.Do("POST", "/api/v1/sns/topics/orders/subscriptions", bad); r.Status != 400 && r.Status != 404 {
			t.Errorf("subscribe %v: %d", bad, r.Status)
		}
	}
	c.Must(201, "POST", "/api/v1/sns/topics/orders/subscriptions", map[string]any{"protocol": "sqs", "endpoint": "billing"})
	arn := c.Must(200, "GET", "/api/v1/sqs/queues/shipping", nil)["arn"].(string)
	c.Must(201, "POST", "/api/v1/sns/topics/orders/subscriptions", map[string]any{"protocol": "sqs", "endpoint": arn, "raw_message_delivery": true})
	c.Must(201, "POST", "/api/v1/sns/topics/orders/subscriptions", map[string]any{"protocol": "log", "endpoint": "/app/orders"})
	if r := c.Do("POST", "/api/v1/sns/topics/orders/subscriptions", map[string]any{"protocol": "sqs", "endpoint": "billing"}); r.Status != 409 {
		t.Fatalf("duplicate subscription: %d", r.Status)
	}
	pub := c.Must(200, "POST", "/api/v1/sns/topics/orders/publish", map[string]any{"message": "order 42 placed", "subject": "new order"})
	if len(pub["deliveries"].([]any)) != 3 {
		t.Fatalf("deliveries: %v", pub)
	}
	b := receive(c, "billing", 10, 30)
	if len(b) != 1 {
		t.Fatalf("billing: %v", b)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(b[0]["body"].(string)), &env); err != nil || env["Message"] != "order 42 placed" || env["Subject"] != "new order" || env["Type"] != "Notification" {
		t.Fatalf("envelope: %v %v", b[0]["body"], err)
	}
	s := receive(c, "shipping", 10, 30)
	if len(s) != 1 || s[0]["body"] != "order 42 placed" {
		t.Fatalf("raw delivery: %v", s)
	}
	// Deleting a subscribed queue makes later deliveries fail (recorded, not fatal).
	c.Must(204, "DELETE", "/api/v1/sqs/queues/billing", nil)
	pub = c.Must(200, "POST", "/api/v1/sns/topics/orders/publish", map[string]any{"message": "order 43"})
	failed := 0
	for _, d := range pub["deliveries"].([]any) {
		if d.(map[string]any)["status"] == "failed" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("expected 1 failed delivery: %v", pub)
	}
	dl := c.Must(200, "GET", "/api/v1/sns/topics/orders/deliveries", nil)
	if len(dl["items"].([]any)) != 6 {
		t.Fatalf("delivery records: %v", dl)
	}
	if r := c.Do("POST", "/api/v1/sns/topics/orders/publish", map[string]any{"message": ""}); r.Status != 400 {
		t.Fatalf("empty message: %d", r.Status)
	}
	c.Must(204, "DELETE", "/api/v1/sns/topics/orders", nil)
	if r := c.Do("GET", "/api/v1/sns/topics/orders", nil); r.Status != 404 {
		t.Fatalf("deleted topic: %d", r.Status)
	}
}
