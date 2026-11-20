package repositories

import (
	"context"
	"database/sql"
	"time"

	"cloudsim/backend/internal/db"
)

// Queue is an SQS-style standard queue.
type Queue struct {
	QueueID           string `json:"queue_id"`
	AccountID         string `json:"-"`
	Name              string `json:"name"`
	VisibilityTimeout int    `json:"visibility_timeout"`
	MessageRetention  int    `json:"message_retention"`
	DelaySeconds      int    `json:"delay_seconds"`
	MaxReceiveCount   int    `json:"max_receive_count"`
	DLQQueueID        string `json:"-"`
	DeadLetterQueue   string `json:"dead_letter_queue"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// QueueCounts are approximate message counts.
type QueueCounts struct {
	Visible    int `json:"approximate_number_of_messages"`
	NotVisible int `json:"approximate_number_of_messages_not_visible"`
	Delayed    int `json:"approximate_number_of_messages_delayed"`
}

// Message is a stored queue message.
type Message struct {
	MessageID       string `json:"message_id"`
	QueueID         string `json:"-"`
	Body            string `json:"body"`
	MD5OfBody       string `json:"md5_of_body"`
	SentAt          string `json:"sent_at"`
	VisibleAt       string `json:"-"`
	ExpiresAt       string `json:"-"`
	ReceiveCount    int    `json:"receive_count"`
	ReceiptHandle   string `json:"receipt_handle,omitempty"`
	FirstReceivedAt string `json:"first_received_at,omitempty"`
}

// ReceiveResult is the outcome of one receive transaction.
type ReceiveResult struct {
	Messages []Message
	MovedDLQ []Message // messages moved to the dead-letter queue during this receive
}

type SQSRepo interface {
	InsertQueue(ctx context.Context, q *Queue) error
	GetQueue(ctx context.Context, account, name string) (*Queue, error)
	GetQueueByID(ctx context.Context, id string) (*Queue, error)
	ListQueues(ctx context.Context, account string) ([]Queue, error)
	ListAllQueues(ctx context.Context) ([]Queue, error)
	UpdateQueue(ctx context.Context, q *Queue) error
	DeleteQueue(ctx context.Context, id string) error
	QueuesUsingDLQ(ctx context.Context, dlqID string) ([]string, error)
	Counts(ctx context.Context, queueID string, now time.Time) (QueueCounts, error)
	InsertMessage(ctx context.Context, m *Message) error
	// Receive atomically selects up to max visible messages, hides them for vt and rotates their
	// receipt handles. Messages whose receive count already reached maxReceive are moved to dlqID.
	Receive(ctx context.Context, q *Queue, max int, vt time.Duration, now time.Time, newHandle func() string) (*ReceiveResult, error)
	DeleteMessage(ctx context.Context, queueID, receiptHandle string) (bool, error)
	Purge(ctx context.Context, queueID string) (int64, error)
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

type sqsRepo struct{ d *db.DB }

func NewSQSRepo(d *db.DB) SQSRepo { return &sqsRepo{d} }

const queueCols = `q.queue_id, q.account_id, q.name, q.visibility_timeout, q.message_retention, q.delay_seconds,
 q.max_receive_count, COALESCE(q.dlq_queue_id,''), COALESCE(d.name,''), q.created_at, q.updated_at`

const queueFrom = ` FROM sqs_queues q LEFT JOIN sqs_queues d ON d.queue_id = q.dlq_queue_id`

func scanQueue(sc interface{ Scan(...any) error }) (*Queue, error) {
	var q Queue
	err := sc.Scan(&q.QueueID, &q.AccountID, &q.Name, &q.VisibilityTimeout, &q.MessageRetention, &q.DelaySeconds,
		&q.MaxReceiveCount, &q.DLQQueueID, &q.DeadLetterQueue, &q.CreatedAt, &q.UpdatedAt)
	return &q, err
}

func sqsNullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *sqsRepo) InsertQueue(ctx context.Context, q *Queue) error {
	_, err := r.d.ExecContext(ctx, `INSERT INTO sqs_queues(queue_id, account_id, name, visibility_timeout, message_retention,
 delay_seconds, max_receive_count, dlq_queue_id, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		q.QueueID, q.AccountID, q.Name, q.VisibilityTimeout, q.MessageRetention, q.DelaySeconds, q.MaxReceiveCount,
		sqsNullStr(q.DLQQueueID), q.CreatedAt, q.UpdatedAt)
	return err
}

func (r *sqsRepo) GetQueue(ctx context.Context, account, name string) (*Queue, error) {
	q, err := scanQueue(r.d.QueryRowContext(ctx, `SELECT `+queueCols+queueFrom+` WHERE q.account_id=? AND q.name=?`, account, name))
	return q, notFound(err)
}

func (r *sqsRepo) GetQueueByID(ctx context.Context, id string) (*Queue, error) {
	q, err := scanQueue(r.d.QueryRowContext(ctx, `SELECT `+queueCols+queueFrom+` WHERE q.queue_id=?`, id))
	return q, notFound(err)
}

func (r *sqsRepo) list(ctx context.Context, where string, args ...any) ([]Queue, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+queueCols+queueFrom+where+` ORDER BY q.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Queue{}
	for rows.Next() {
		q, err := scanQueue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *q)
	}
	return out, rows.Err()
}

func (r *sqsRepo) ListQueues(ctx context.Context, account string) ([]Queue, error) {
	return r.list(ctx, ` WHERE q.account_id=?`, account)
}

func (r *sqsRepo) ListAllQueues(ctx context.Context) ([]Queue, error) { return r.list(ctx, "") }

func (r *sqsRepo) UpdateQueue(ctx context.Context, q *Queue) error {
	_, err := r.d.ExecContext(ctx, `UPDATE sqs_queues SET visibility_timeout=?, message_retention=?, delay_seconds=?,
 max_receive_count=?, dlq_queue_id=?, updated_at=? WHERE queue_id=?`,
		q.VisibilityTimeout, q.MessageRetention, q.DelaySeconds, q.MaxReceiveCount, sqsNullStr(q.DLQQueueID), q.UpdatedAt, q.QueueID)
	return err
}

func (r *sqsRepo) DeleteQueue(ctx context.Context, id string) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM sqs_queues WHERE queue_id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *sqsRepo) QueuesUsingDLQ(ctx context.Context, dlqID string) ([]string, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT name FROM sqs_queues WHERE dlq_queue_id=? ORDER BY name`, dlqID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *sqsRepo) Counts(ctx context.Context, queueID string, now time.Time) (QueueCounts, error) {
	var c QueueCounts
	ts := db.Fmt(now)
	err := r.d.QueryRowContext(ctx, `SELECT
 COALESCE(SUM(CASE WHEN visible_at <= ? THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN visible_at > ? AND receive_count > 0 THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN visible_at > ? AND receive_count = 0 THEN 1 ELSE 0 END),0)
 FROM sqs_messages WHERE queue_id=? AND expires_at > ?`, ts, ts, ts, queueID, ts).Scan(&c.Visible, &c.NotVisible, &c.Delayed)
	return c, err
}

func (r *sqsRepo) InsertMessage(ctx context.Context, m *Message) error {
	_, err := r.d.ExecContext(ctx, `INSERT INTO sqs_messages(message_id, queue_id, body, md5_of_body, sent_at, visible_at, expires_at)
 VALUES(?,?,?,?,?,?,?)`, m.MessageID, m.QueueID, m.Body, m.MD5OfBody, m.SentAt, m.VisibleAt, m.ExpiresAt)
	return err
}

func (r *sqsRepo) Receive(ctx context.Context, q *Queue, max int, vt time.Duration, now time.Time, newHandle func() string) (*ReceiveResult, error) {
	out := &ReceiveResult{Messages: []Message{}}
	nowS, hideUntil := db.Fmt(now), db.Fmt(now.Add(vt))
	// The connection is opened with _txlock=immediate, so this transaction holds SQLite's write lock
	// from BEGIN: concurrent receivers are serialized and can never select the same visible message.
	err := r.d.Tx(ctx, func(tx *sql.Tx) error {
		for len(out.Messages) < max {
			rows, err := tx.QueryContext(ctx, `SELECT message_id, body, md5_of_body, sent_at, receive_count, COALESCE(first_received_at,'')
 FROM sqs_messages WHERE queue_id=? AND visible_at <= ? AND expires_at > ? ORDER BY sent_at, rowid LIMIT ?`,
				q.QueueID, nowS, nowS, max-len(out.Messages))
			if err != nil {
				return err
			}
			var batch []Message
			for rows.Next() {
				var m Message
				if err := rows.Scan(&m.MessageID, &m.Body, &m.MD5OfBody, &m.SentAt, &m.ReceiveCount, &m.FirstReceivedAt); err != nil {
					rows.Close()
					return err
				}
				batch = append(batch, m)
			}
			rows.Close()
			if len(batch) == 0 {
				break
			}
			for _, m := range batch {
				if q.DLQQueueID != "" && q.MaxReceiveCount > 0 && m.ReceiveCount >= q.MaxReceiveCount {
					// Redrive: the message exceeded maxReceiveCount; move it (visible) to the DLQ.
					if _, err := tx.ExecContext(ctx, `UPDATE sqs_messages SET queue_id=?, visible_at=?, receipt_handle=NULL WHERE message_id=?`,
						q.DLQQueueID, nowS, m.MessageID); err != nil {
						return err
					}
					out.MovedDLQ = append(out.MovedDLQ, m)
					continue
				}
				m.ReceiptHandle = newHandle()
				m.ReceiveCount++
				if m.FirstReceivedAt == "" {
					m.FirstReceivedAt = nowS
				}
				res, err := tx.ExecContext(ctx, `UPDATE sqs_messages SET visible_at=?, receipt_handle=?, receive_count=?, first_received_at=?
 WHERE message_id=? AND visible_at <= ?`, hideUntil, m.ReceiptHandle, m.ReceiveCount, m.FirstReceivedAt, m.MessageID, nowS)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 1 {
					out.Messages = append(out.Messages, m)
				}
			}
		}
		return nil
	})
	return out, err
}

func (r *sqsRepo) DeleteMessage(ctx context.Context, queueID, receiptHandle string) (bool, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM sqs_messages WHERE queue_id=? AND receipt_handle=?`, queueID, receiptHandle)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *sqsRepo) Purge(ctx context.Context, queueID string) (int64, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM sqs_messages WHERE queue_id=?`, queueID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *sqsRepo) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM sqs_messages WHERE expires_at <= ?`, db.Fmt(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
