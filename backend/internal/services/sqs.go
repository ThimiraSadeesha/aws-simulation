package services

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
)

// ---- helpers shared by the messaging/data modules (sqs, sns, dynamodb, route53)

// registerSimple registers a non-lifecycle resource with the engine (it becomes `available`
// immediately); the engine enforces per-owner quotas and dependency tracking.
func (d *Deps) registerSimple(ctx context.Context, id, kind, account string, deps []string, attrs map[string]any) error {
	_, err := d.Engine.Register(ctx, id, simulator.RegisterRequest{Kind: kind, Owner: account, DependsOn: deps, Attributes: attrs})
	return EngineErr(err)
}

// resyncSimple re-registers a persisted non-lifecycle resource in state `available`.
func (d *Deps) resyncSimple(ctx context.Context, id, kind, account string, deps []string) error {
	st := "available"
	_, err := d.Engine.Register(ctx, id, simulator.RegisterRequest{Kind: kind, Owner: account, State: &st, DependsOn: deps})
	return err
}

func (d *Deps) deregister(ctx context.Context, id string) error {
	return EngineErr(d.Engine.Deregister(ctx, id))
}

// ---- SQS

const (
	sqsMaxBody          = 256 * 1024
	sqsDefaultVT        = 30
	sqsDefaultRetention = 4 * 24 * 3600
)

var queueNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// SQSService implements SQS-style standard queues.
type SQSService struct {
	*Deps
	Repo repositories.SQSRepo
	now  func() time.Time
}

func NewSQSService(d *Deps) *SQSService {
	return &SQSService{Deps: d, Repo: repositories.NewSQSRepo(d.Store.DB), now: time.Now}
}

// QueueView is the API representation of a queue.
type QueueView struct {
	repositories.Queue
	repositories.QueueCounts
	ARN   string `json:"arn"`
	URL   string `json:"url"`
	State string `json:"state"`
}

func QueueARN(account, name string) string { return auth.ARN("sqs", account, name) }

func (s *SQSService) view(ctx context.Context, q *repositories.Queue) (*QueueView, error) {
	c, err := s.Repo.Counts(ctx, q.QueueID, s.now())
	if err != nil {
		return nil, err
	}
	return &QueueView{Queue: *q, QueueCounts: c, ARN: QueueARN(q.AccountID, q.Name),
		URL: "http://sqs.local-1.cloudsim.local/" + q.AccountID + "/" + q.Name, State: "available"}, nil
}

type QueueAttributes struct {
	VisibilityTimeout *int    `json:"visibility_timeout"`
	MessageRetention  *int    `json:"message_retention"`
	DelaySeconds      *int    `json:"delay_seconds"`
	MaxReceiveCount   *int    `json:"max_receive_count"`
	DeadLetterQueue   *string `json:"dead_letter_queue"`
}

type CreateQueueInput struct {
	Name string `json:"name"`
	QueueAttributes
}

func rangeCheck(field string, v *int, lo, hi int) error {
	if v != nil && (*v < lo || *v > hi) {
		return apierr.Validation("%s must be between %d and %d", field, lo, hi)
	}
	return nil
}

// applyAttrs validates and applies attributes onto q (resolving the dead-letter queue).
func (s *SQSService) applyAttrs(ctx context.Context, account string, q *repositories.Queue, a QueueAttributes) error {
	for _, c := range []struct {
		f      string
		v      *int
		lo, hi int
	}{{"visibility_timeout", a.VisibilityTimeout, 0, 43200}, {"message_retention", a.MessageRetention, 60, 1209600},
		{"delay_seconds", a.DelaySeconds, 0, 900}, {"max_receive_count", a.MaxReceiveCount, 1, 1000}} {
		if err := rangeCheck(c.f, c.v, c.lo, c.hi); err != nil {
			return err
		}
	}
	if a.VisibilityTimeout != nil {
		q.VisibilityTimeout = *a.VisibilityTimeout
	}
	if a.MessageRetention != nil {
		q.MessageRetention = *a.MessageRetention
	}
	if a.DelaySeconds != nil {
		q.DelaySeconds = *a.DelaySeconds
	}
	if a.MaxReceiveCount != nil {
		q.MaxReceiveCount = *a.MaxReceiveCount
	}
	if a.DeadLetterQueue != nil {
		name := strings.TrimSpace(*a.DeadLetterQueue)
		if i := strings.LastIndex(name, ":"); strings.HasPrefix(name, "arn:") && i >= 0 {
			name = name[i+1:]
		}
		if name == "" {
			q.DLQQueueID, q.DeadLetterQueue = "", ""
		} else {
			if name == q.Name {
				return apierr.Validation("a queue cannot be its own dead-letter queue")
			}
			dlq, err := s.Repo.GetQueue(ctx, account, name)
			if err != nil {
				return RepoErr(err, "dead-letter queue "+name)
			}
			if dlq.DLQQueueID == q.QueueID && q.QueueID != "" {
				return apierr.Validation("dead-letter queue %s already redrives to %s (cycle)", name, q.Name)
			}
			q.DLQQueueID, q.DeadLetterQueue = dlq.QueueID, dlq.Name
		}
	}
	if (q.DLQQueueID == "") != (q.MaxReceiveCount == 0) {
		return apierr.Validation("dead_letter_queue and max_receive_count must be set together (redrive policy)")
	}
	return nil
}

func (s *SQSService) Create(ctx context.Context, account string, in CreateQueueInput) (*QueueView, error) {
	if strings.HasSuffix(in.Name, ".fifo") {
		return nil, apierr.Validation("FIFO queues are not supported by CloudSim (standard queues only)")
	}
	if !queueNameRe.MatchString(in.Name) {
		return nil, apierr.Validation("queue name must be 1-80 characters: letters, digits, hyphens and underscores")
	}
	if _, err := s.Repo.GetQueue(ctx, account, in.Name); err == nil {
		return nil, apierr.Conflict("queue %s already exists", in.Name)
	}
	now := db.Now()
	q := &repositories.Queue{QueueID: ids.New("queue"), AccountID: account, Name: in.Name, VisibilityTimeout: sqsDefaultVT,
		MessageRetention: sqsDefaultRetention, CreatedAt: now, UpdatedAt: now}
	if err := s.applyAttrs(ctx, account, q, in.QueueAttributes); err != nil {
		return nil, err
	}
	var deps []string
	if q.DLQQueueID != "" {
		deps = []string{q.DLQQueueID}
	}
	if err := s.registerSimple(ctx, q.QueueID, "queue", account, deps, map[string]any{"name": q.Name}); err != nil {
		return nil, err
	}
	if err := s.Repo.InsertQueue(ctx, q); err != nil {
		_ = s.Engine.Deregister(ctx, q.QueueID)
		return nil, RepoErr(err, "queue "+in.Name)
	}
	s.RecordEvent(ctx, account, q.QueueID, "queue", "created", "Queue "+q.Name+" created", nil)
	return s.view(ctx, q)
}

func (s *SQSService) get(ctx context.Context, account, name string) (*repositories.Queue, error) {
	q, err := s.Repo.GetQueue(ctx, account, name)
	if err != nil {
		return nil, RepoErr(err, "queue "+name)
	}
	return q, nil
}

func (s *SQSService) Get(ctx context.Context, account, name string) (*QueueView, error) {
	q, err := s.get(ctx, account, name)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, q)
}

func (s *SQSService) List(ctx context.Context, account string) ([]QueueView, error) {
	l, err := s.Repo.ListQueues(ctx, account)
	if err != nil {
		return nil, err
	}
	out := make([]QueueView, 0, len(l))
	for i := range l {
		v, err := s.view(ctx, &l[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

func (s *SQSService) Update(ctx context.Context, account, name string, a QueueAttributes) (*QueueView, error) {
	q, err := s.get(ctx, account, name)
	if err != nil {
		return nil, err
	}
	oldDLQ := q.DLQQueueID
	if err := s.applyAttrs(ctx, account, q, a); err != nil {
		return nil, err
	}
	if q.DLQQueueID != oldDLQ {
		var deps []string
		if q.DLQQueueID != "" {
			deps = []string{q.DLQQueueID}
		}
		if err := s.registerSimple(ctx, q.QueueID, "queue", account, deps, map[string]any{"name": q.Name}); err != nil {
			return nil, err
		}
	}
	q.UpdatedAt = db.Now()
	if err := s.Repo.UpdateQueue(ctx, q); err != nil {
		return nil, err
	}
	s.RecordEvent(ctx, account, q.QueueID, "queue", "updated", "Queue "+q.Name+" attributes updated", a)
	return s.view(ctx, q)
}

func (s *SQSService) Delete(ctx context.Context, account, name string) error {
	q, err := s.get(ctx, account, name)
	if err != nil {
		return err
	}
	users, err := s.Repo.QueuesUsingDLQ(ctx, q.QueueID)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return apierr.Dependency("queue %s is the dead-letter queue of: %s", name, strings.Join(users, ", "))
	}
	if err := s.deregister(ctx, q.QueueID); err != nil {
		return err
	}
	if err := s.Repo.DeleteQueue(ctx, q.QueueID); err != nil {
		return RepoErr(err, "queue "+name)
	}
	s.RecordEvent(ctx, account, q.QueueID, "queue", "deleted", "Queue "+name+" deleted", nil)
	return nil
}

type SendMessageInput struct {
	Body         string `json:"body"`
	DelaySeconds *int   `json:"delay_seconds"`
}

type SentMessage struct {
	MessageID string `json:"message_id"`
	MD5OfBody string `json:"md5_of_body"`
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func (s *SQSService) Send(ctx context.Context, account, name string, in SendMessageInput) (*SentMessage, error) {
	q, err := s.get(ctx, account, name)
	if err != nil {
		return nil, err
	}
	return s.sendTo(ctx, q, in)
}

func (s *SQSService) sendTo(ctx context.Context, q *repositories.Queue, in SendMessageInput) (*SentMessage, error) {
	if in.Body == "" {
		return nil, apierr.Validation("message body must not be empty")
	}
	if len(in.Body) > sqsMaxBody {
		return nil, apierr.Validation("message body must be at most %d bytes", sqsMaxBody)
	}
	if !utf8.ValidString(in.Body) {
		return nil, apierr.Validation("message body must be valid UTF-8")
	}
	if err := rangeCheck("delay_seconds", in.DelaySeconds, 0, 900); err != nil {
		return nil, err
	}
	delay := q.DelaySeconds
	if in.DelaySeconds != nil {
		delay = *in.DelaySeconds
	}
	now := s.now()
	m := &repositories.Message{MessageID: ids.Token()[:32], QueueID: q.QueueID, Body: in.Body, MD5OfBody: md5hex(in.Body),
		SentAt: db.Fmt(now), VisibleAt: db.Fmt(now.Add(time.Duration(delay) * time.Second)),
		ExpiresAt: db.Fmt(now.Add(time.Duration(q.MessageRetention) * time.Second))}
	m.MessageID = fmt.Sprintf("%s-%s-%s-%s-%s", m.MessageID[:8], m.MessageID[8:12], m.MessageID[12:16], m.MessageID[16:20], m.MessageID[20:32])
	if err := s.Repo.InsertMessage(ctx, m); err != nil {
		return nil, err
	}
	return &SentMessage{MessageID: m.MessageID, MD5OfBody: m.MD5OfBody}, nil
}

// SendByName is used by other modules (SNS fan-out) to enqueue into a queue of the same account.
func (s *SQSService) SendByName(ctx context.Context, account, name, body string) (*SentMessage, error) {
	return s.Send(ctx, account, name, SendMessageInput{Body: body})
}

// Receive returns up to max messages, hiding them for the visibility timeout (queue default or override).
func (s *SQSService) Receive(ctx context.Context, account, name string, max int, vt *int) ([]repositories.Message, error) {
	q, err := s.get(ctx, account, name)
	if err != nil {
		return nil, err
	}
	if max < 1 || max > 10 {
		return nil, apierr.Validation("max must be between 1 and 10")
	}
	if err := rangeCheck("visibility_timeout", vt, 0, 43200); err != nil {
		return nil, err
	}
	timeout := q.VisibilityTimeout
	if vt != nil {
		timeout = *vt
	}
	res, err := s.Repo.Receive(ctx, q, max, time.Duration(timeout)*time.Second, s.now(), ids.Token)
	if err != nil {
		return nil, err
	}
	for _, m := range res.MovedDLQ {
		s.RecordEvent(ctx, account, q.QueueID, "queue", "moved_to_dlq",
			fmt.Sprintf("Message %s exceeded max_receive_count %d and was moved to %s", m.MessageID, q.MaxReceiveCount, q.DeadLetterQueue),
			map[string]any{"message_id": m.MessageID, "dead_letter_queue": q.DeadLetterQueue})
	}
	return res.Messages, nil
}

func (s *SQSService) DeleteMessage(ctx context.Context, account, name, receipt string) error {
	q, err := s.get(ctx, account, name)
	if err != nil {
		return err
	}
	ok, err := s.Repo.DeleteMessage(ctx, q.QueueID, receipt)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.NotFound("receipt handle is invalid or expired (each receive issues a new handle)")
	}
	return nil
}

func (s *SQSService) Purge(ctx context.Context, account, name string) (int64, error) {
	q, err := s.get(ctx, account, name)
	if err != nil {
		return 0, err
	}
	n, err := s.Repo.Purge(ctx, q.QueueID)
	if err != nil {
		return 0, err
	}
	s.RecordEvent(ctx, account, q.QueueID, "queue", "purged", fmt.Sprintf("Queue %s purged (%d messages)", name, n), nil)
	return n, nil
}

// CleanupExpired deletes messages older than their queue's retention period.
func (s *SQSService) CleanupExpired(ctx context.Context) (int64, error) {
	return s.Repo.DeleteExpired(ctx, s.now())
}

// Resync re-registers queues with the engine (dead-letter queues before the queues using them).
func (s *SQSService) Resync(ctx context.Context) error {
	qs, err := s.Repo.ListAllQueues(ctx)
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for len(done) < len(qs) {
		progress := false
		for _, q := range qs {
			if done[q.QueueID] || (q.DLQQueueID != "" && !done[q.DLQQueueID]) {
				continue
			}
			var deps []string
			if q.DLQQueueID != "" {
				deps = []string{q.DLQQueueID}
			}
			if err := s.resyncSimple(ctx, q.QueueID, "queue", q.AccountID, deps); err != nil {
				return err
			}
			done[q.QueueID], progress = true, true
		}
		if !progress {
			return errors.New("sqs resync: dead-letter queue cycle detected")
		}
	}
	return nil
}
