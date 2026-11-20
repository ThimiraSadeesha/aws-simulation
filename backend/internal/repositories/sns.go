package repositories

import (
	"context"

	"cloudsim/backend/internal/db"
)

// Topic is an SNS-style topic.
type Topic struct {
	TopicID     string `json:"topic_id"`
	AccountID   string `json:"-"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// Subscription delivers a topic's messages to an endpoint.
type Subscription struct {
	SubscriptionID     string `json:"subscription_id"`
	TopicID            string `json:"-"`
	AccountID          string `json:"-"`
	Protocol           string `json:"protocol"`
	Endpoint           string `json:"endpoint"`
	RawMessageDelivery bool   `json:"raw_message_delivery"`
	CreatedAt          string `json:"created_at"`
}

// Delivery is one fan-out attempt.
type Delivery struct {
	ID             int64  `json:"id"`
	TopicID        string `json:"-"`
	MessageID      string `json:"message_id"`
	SubscriptionID string `json:"subscription_id"`
	Protocol       string `json:"protocol"`
	Endpoint       string `json:"endpoint"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	TS             string `json:"ts"`
}

type SNSRepo interface {
	InsertTopic(ctx context.Context, t *Topic) error
	GetTopic(ctx context.Context, account, name string) (*Topic, error)
	ListTopics(ctx context.Context, account string) ([]Topic, error)
	ListAllTopics(ctx context.Context) ([]Topic, error)
	DeleteTopic(ctx context.Context, id string) error
	InsertSubscription(ctx context.Context, s *Subscription) error
	ListSubscriptions(ctx context.Context, topicID string) ([]Subscription, error)
	DeleteSubscription(ctx context.Context, topicID, id string) error
	InsertDelivery(ctx context.Context, d *Delivery) error
	ListDeliveries(ctx context.Context, topicID string, limit int) ([]Delivery, error)
}

type snsRepo struct{ d *db.DB }

func NewSNSRepo(d *db.DB) SNSRepo { return &snsRepo{d} }

func (r *snsRepo) InsertTopic(ctx context.Context, t *Topic) error {
	_, err := r.d.ExecContext(ctx, `INSERT INTO sns_topics(topic_id, account_id, name, display_name, created_at) VALUES(?,?,?,?,?)`,
		t.TopicID, t.AccountID, t.Name, t.DisplayName, t.CreatedAt)
	return err
}

func (r *snsRepo) GetTopic(ctx context.Context, account, name string) (*Topic, error) {
	var t Topic
	err := r.d.QueryRowContext(ctx, `SELECT topic_id, account_id, name, display_name, created_at FROM sns_topics WHERE account_id=? AND name=?`,
		account, name).Scan(&t.TopicID, &t.AccountID, &t.Name, &t.DisplayName, &t.CreatedAt)
	return &t, notFound(err)
}

func (r *snsRepo) listTopics(ctx context.Context, where string, args ...any) ([]Topic, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT topic_id, account_id, name, display_name, created_at FROM sns_topics`+where+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Topic{}
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.TopicID, &t.AccountID, &t.Name, &t.DisplayName, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *snsRepo) ListTopics(ctx context.Context, account string) ([]Topic, error) {
	return r.listTopics(ctx, ` WHERE account_id=?`, account)
}

func (r *snsRepo) ListAllTopics(ctx context.Context) ([]Topic, error) { return r.listTopics(ctx, "") }

func (r *snsRepo) DeleteTopic(ctx context.Context, id string) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM sns_topics WHERE topic_id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *snsRepo) InsertSubscription(ctx context.Context, s *Subscription) error {
	_, err := r.d.ExecContext(ctx, `INSERT INTO sns_subscriptions(subscription_id, topic_id, account_id, protocol, endpoint, raw_delivery, created_at)
 VALUES(?,?,?,?,?,?,?)`, s.SubscriptionID, s.TopicID, s.AccountID, s.Protocol, s.Endpoint, b2i(s.RawMessageDelivery), s.CreatedAt)
	return err
}

func (r *snsRepo) ListSubscriptions(ctx context.Context, topicID string) ([]Subscription, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT subscription_id, topic_id, account_id, protocol, endpoint, raw_delivery, created_at
 FROM sns_subscriptions WHERE topic_id=? ORDER BY created_at, subscription_id`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.SubscriptionID, &s.TopicID, &s.AccountID, &s.Protocol, &s.Endpoint, &s.RawMessageDelivery, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *snsRepo) DeleteSubscription(ctx context.Context, topicID, id string) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM sns_subscriptions WHERE topic_id=? AND subscription_id=?`, topicID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *snsRepo) InsertDelivery(ctx context.Context, d *Delivery) error {
	res, err := r.d.ExecContext(ctx, `INSERT INTO sns_deliveries(topic_id, message_id, subscription_id, protocol, endpoint, status, error, ts)
 VALUES(?,?,?,?,?,?,?,?)`, d.TopicID, d.MessageID, d.SubscriptionID, d.Protocol, d.Endpoint, d.Status, d.Error, d.TS)
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	return nil
}

func (r *snsRepo) ListDeliveries(ctx context.Context, topicID string, limit int) ([]Delivery, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT id, topic_id, message_id, subscription_id, protocol, endpoint, status, error, ts
 FROM sns_deliveries WHERE topic_id=? ORDER BY id DESC LIMIT ?`, topicID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.TopicID, &d.MessageID, &d.SubscriptionID, &d.Protocol, &d.Endpoint, &d.Status, &d.Error, &d.TS); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
