package tests

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"cloudsim/backend/internal/services"
	"cloudsim/backend/internal/testutil"
)

func receive(c *testutil.Client, q string, max, vt int) []map[string]any {
	m := c.Must(200, "GET", fmt.Sprintf("/api/v1/sqs/queues/%s/messages?max=%d&visibility_timeout=%d", q, max, vt), nil)
	out := []map[string]any{}
	for _, x := range m["messages"].([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

func TestSQSQueueCRUDAndValidation(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	for _, bad := range []map[string]any{
		{"name": "bad name"}, {"name": strings.Repeat("a", 81)}, {"name": "jobs.fifo"},
		{"name": "q", "visibility_timeout": 50000}, {"name": "q", "message_retention": 10},
		{"name": "q", "delay_seconds": 901}, {"name": "q", "max_receive_count": 3},
		{"name": "q", "dead_letter_queue": "missing", "max_receive_count": 3},
	} {
		if r := c.Do("POST", "/api/v1/sqs/queues", bad); r.Status != 400 && r.Status != 404 {
			t.Errorf("%v: want 400/404, got %d %s", bad, r.Status, r.Body)
		}
	}
	q := c.Must(201, "POST", "/api/v1/sqs/queues", map[string]any{"name": "jobs", "visibility_timeout": 45})
	if q["visibility_timeout"].(float64) != 45 || q["message_retention"].(float64) != 345600 || q["state"] != "available" ||
		!strings.HasPrefix(q["arn"].(string), "arn:cloudsim:sqs:") {
		t.Fatalf("queue: %v", q)
	}
	if h.Engine.Count("queue") != 1 {
		t.Fatal("queue should be registered with the engine")
	}
	if r := c.Do("POST", "/api/v1/sqs/queues", map[string]any{"name": "jobs"}); r.Status != 409 {
		t.Fatalf("duplicate: %d", r.Status)
	}
	l := c.Must(200, "GET", "/api/v1/sqs/queues", nil)
	if l["total"].(float64) != 1 {
		t.Fatalf("list: %v", l)
	}
	if r := c.Do("POST", "/api/v1/sqs/queues/jobs/messages", map[string]any{"body": ""}); r.Status != 400 {
		t.Fatalf("empty body: %d", r.Status)
	}
	c.Must(201, "POST", "/api/v1/sqs/queues/jobs/messages", map[string]any{"body": "hello"})
	c.Must(201, "POST", "/api/v1/sqs/queues/jobs/messages", map[string]any{"body": "later", "delay_seconds": 60})
	g := c.Must(200, "GET", "/api/v1/sqs/queues/jobs", nil)
	if g["approximate_number_of_messages"].(float64) != 1 || g["approximate_number_of_messages_delayed"].(float64) != 1 {
		t.Fatalf("counts: %v", g)
	}
	msgs := receive(c, "jobs", 10, 30)
	if len(msgs) != 1 || msgs[0]["body"] != "hello" || msgs[0]["md5_of_body"] != "5d41402abc4b2a76b9719d911017c592" {
		t.Fatalf("receive (delayed message must stay hidden): %v", msgs)
	}
	g = c.Must(200, "GET", "/api/v1/sqs/queues/jobs", nil)
	if g["approximate_number_of_messages_not_visible"].(float64) != 1 {
		t.Fatalf("in-flight count: %v", g)
	}
	c.Must(204, "DELETE", "/api/v1/sqs/queues/jobs/messages/"+msgs[0]["receipt_handle"].(string), nil)
	p := c.Must(200, "POST", "/api/v1/sqs/queues/jobs/purge", nil)
	if p["purged"].(float64) != 1 {
		t.Fatalf("purge: %v", p)
	}
	// Account isolation: another account cannot see the queue.
	other := h.Signup("bob")
	if r := other.Do("GET", "/api/v1/sqs/queues/jobs", nil); r.Status != 404 {
		t.Fatalf("cross-account: %d", r.Status)
	}
	c.Must(204, "DELETE", "/api/v1/sqs/queues/jobs", nil)
	if h.Engine.Count("queue") != 0 {
		t.Fatal("queue should be deregistered")
	}
}

func TestSQSVisibilityTimeoutRedeliveryAndDLQ(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	c.Must(201, "POST", "/api/v1/sqs/queues", map[string]any{"name": "dlq"})
	c.Must(201, "POST", "/api/v1/sqs/queues", map[string]any{"name": "work", "dead_letter_queue": "dlq", "max_receive_count": 2})
	if r := c.Do("DELETE", "/api/v1/sqs/queues/dlq", nil); r.Status != 409 {
		t.Fatalf("deleting a DLQ in use: %d %s", r.Status, r.Body)
	}
	c.Must(201, "POST", "/api/v1/sqs/queues/work/messages", map[string]any{"body": "job-1"})

	first := receive(c, "work", 1, 1)
	if len(first) != 1 || first[0]["receive_count"].(float64) != 1 {
		t.Fatalf("first receive: %v", first)
	}
	if again := receive(c, "work", 1, 1); len(again) != 0 {
		t.Fatalf("message must be invisible during the visibility timeout: %v", again)
	}
	time.Sleep(1100 * time.Millisecond)
	second := receive(c, "work", 1, 0)
	if len(second) != 1 || second[0]["receive_count"].(float64) != 2 || second[0]["message_id"] != first[0]["message_id"] {
		t.Fatalf("redelivery: %v", second)
	}
	if second[0]["receipt_handle"] == first[0]["receipt_handle"] {
		t.Fatal("receipt handle must rotate on each receive")
	}
	if r := c.Do("DELETE", "/api/v1/sqs/queues/work/messages/"+first[0]["receipt_handle"].(string), nil); r.Status != 404 {
		t.Fatalf("stale receipt handle: %d", r.Status)
	}
	// Third receive exceeds max_receive_count=2: the message moves to the DLQ.
	if third := receive(c, "work", 1, 0); len(third) != 0 {
		t.Fatalf("message should have been redriven: %v", third)
	}
	d := receive(c, "dlq", 10, 30)
	if len(d) != 1 || d[0]["body"] != "job-1" {
		t.Fatalf("dlq: %v", d)
	}
	ev := c.Must(200, "GET", "/api/v1/events?type=moved_to_dlq", nil)
	if !strings.Contains(string(fmt.Sprint(ev)), "moved to dlq") {
		t.Fatalf("DLQ event: %v", ev)
	}
}

func TestSQSConcurrentReceiversNoDoubleDelivery(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	c.Must(201, "POST", "/api/v1/sqs/queues", map[string]any{"name": "fanin"})
	const n = 40
	for i := 0; i < n; i++ {
		c.Must(201, "POST", "/api/v1/sqs/queues/fanin/messages", map[string]any{"body": fmt.Sprintf("m%d", i)})
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				r := c.Do("GET", "/api/v1/sqs/queues/fanin/messages?max=3&visibility_timeout=120", nil)
				if r.Status != 200 {
					errs <- fmt.Sprintf("receive: %d %s", r.Status, r.Body)
					return
				}
				var out struct {
					Messages []struct {
						Body string `json:"body"`
					} `json:"messages"`
				}
				_ = r.JSON(&out)
				if len(out.Messages) == 0 {
					return
				}
				mu.Lock()
				for _, m := range out.Messages {
					seen[m.Body]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct messages, got %d", n, len(seen))
	}
	for b, k := range seen {
		if k != 1 {
			t.Fatalf("message %s delivered %d times within the visibility window", b, k)
		}
	}
}

func TestSQSRetentionCleanup(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	c.Must(201, "POST", "/api/v1/sqs/queues", map[string]any{"name": "old", "message_retention": 60})
	c.Must(201, "POST", "/api/v1/sqs/queues/old/messages", map[string]any{"body": "x"})
	if _, err := h.DB.Exec(`UPDATE sqs_messages SET expires_at='2000-01-01T00:00:00.000Z'`); err != nil {
		t.Fatal(err)
	}
	v, _ := h.Srv.Ext.Load("sqs")
	n, err := v.(*services.SQSService).CleanupExpired(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
}
