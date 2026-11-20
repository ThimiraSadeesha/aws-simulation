package services

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
)

var (
	topicNameRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	logGroupNameRe = regexp.MustCompile(`^[A-Za-z0-9_\-/.#]{1,512}$`)
)

// SNSService implements SNS-style topics with sqs and log subscriptions.
type SNSService struct {
	*Deps
	Repo repositories.SNSRepo
	SQS  *SQSService
}

func NewSNSService(d *Deps, sqs *SQSService) *SNSService {
	return &SNSService{Deps: d, Repo: repositories.NewSNSRepo(d.Store.DB), SQS: sqs}
}

func TopicARN(account, name string) string { return auth.ARN("sns", account, name) }

type TopicView struct {
	repositories.Topic
	ARN           string                      `json:"arn"`
	State         string                      `json:"state"`
	Subscriptions []repositories.Subscription `json:"subscriptions"`
}

func (s *SNSService) view(ctx context.Context, t *repositories.Topic) (*TopicView, error) {
	subs, err := s.Repo.ListSubscriptions(ctx, t.TopicID)
	if err != nil {
		return nil, err
	}
	return &TopicView{Topic: *t, ARN: TopicARN(t.AccountID, t.Name), State: "available", Subscriptions: subs}, nil
}

type CreateTopicInput struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

func (s *SNSService) Create(ctx context.Context, account string, in CreateTopicInput) (*TopicView, error) {
	if strings.HasSuffix(in.Name, ".fifo") {
		return nil, apierr.Validation("FIFO topics are not supported by CloudSim")
	}
	if !topicNameRe.MatchString(in.Name) {
		return nil, apierr.Validation("topic name must be 1-256 characters: letters, digits, hyphens and underscores")
	}
	if len(in.DisplayName) > 100 {
		return nil, apierr.Validation("display_name must be at most 100 characters")
	}
	if _, err := s.Repo.GetTopic(ctx, account, in.Name); err == nil {
		return nil, apierr.Conflict("topic %s already exists", in.Name)
	}
	t := &repositories.Topic{TopicID: ids.New("topic"), AccountID: account, Name: in.Name, DisplayName: in.DisplayName, CreatedAt: db.Now()}
	if err := s.registerSimple(ctx, t.TopicID, "topic", account, nil, map[string]any{"name": t.Name}); err != nil {
		return nil, err
	}
	if err := s.Repo.InsertTopic(ctx, t); err != nil {
		_ = s.Engine.Deregister(ctx, t.TopicID)
		return nil, RepoErr(err, "topic "+in.Name)
	}
	s.RecordEvent(ctx, account, t.TopicID, "topic", "created", "Topic "+t.Name+" created", nil)
	return s.view(ctx, t)
}

func (s *SNSService) get(ctx context.Context, account, name string) (*repositories.Topic, error) {
	t, err := s.Repo.GetTopic(ctx, account, name)
	if err != nil {
		return nil, RepoErr(err, "topic "+name)
	}
	return t, nil
}

func (s *SNSService) Get(ctx context.Context, account, name string) (*TopicView, error) {
	t, err := s.get(ctx, account, name)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, t)
}

func (s *SNSService) List(ctx context.Context, account string) ([]TopicView, error) {
	l, err := s.Repo.ListTopics(ctx, account)
	if err != nil {
		return nil, err
	}
	out := make([]TopicView, 0, len(l))
	for i := range l {
		v, err := s.view(ctx, &l[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

func (s *SNSService) Delete(ctx context.Context, account, name string) error {
	t, err := s.get(ctx, account, name)
	if err != nil {
		return err
	}
	if err := s.deregister(ctx, t.TopicID); err != nil {
		return err
	}
	if err := s.Repo.DeleteTopic(ctx, t.TopicID); err != nil {
		return RepoErr(err, "topic "+name)
	}
	s.RecordEvent(ctx, account, t.TopicID, "topic", "deleted", "Topic "+name+" deleted (subscriptions removed)", nil)
	return nil
}

type SubscribeInput struct {
	Protocol           string `json:"protocol"`
	Endpoint           string `json:"endpoint"`
	RawMessageDelivery bool   `json:"raw_message_delivery"`
}

// queueNameFromEndpoint accepts a queue name or a CloudSim SQS ARN of the same account.
func queueNameFromEndpoint(account, ep string) (string, error) {
	if strings.HasPrefix(ep, "arn:") {
		parts := strings.Split(ep, ":")
		if len(parts) != 6 || parts[2] != "sqs" {
			return "", apierr.Validation("endpoint must be a queue name or an SQS queue ARN")
		}
		if parts[4] != account {
			return "", apierr.Validation("cross-account SQS subscriptions are not supported")
		}
		return parts[5], nil
	}
	return ep, nil
}

func (s *SNSService) Subscribe(ctx context.Context, account, topic string, in SubscribeInput) (*repositories.Subscription, error) {
	t, err := s.get(ctx, account, topic)
	if err != nil {
		return nil, err
	}
	ep := strings.TrimSpace(in.Endpoint)
	switch in.Protocol {
	case "sqs":
		name, err := queueNameFromEndpoint(account, ep)
		if err != nil {
			return nil, err
		}
		if _, err := s.SQS.get(ctx, account, name); err != nil {
			return nil, err
		}
		ep = name
	case "log":
		if !logGroupNameRe.MatchString(ep) {
			return nil, apierr.Validation("endpoint must be a CloudWatch log group name (1-512 chars of [A-Za-z0-9_-/.#])")
		}
	default:
		return nil, apierr.Validation("protocol must be one of: sqs, log (http/email/sms/lambda are not simulated)")
	}
	sub := &repositories.Subscription{SubscriptionID: ids.New("sub"), TopicID: t.TopicID, AccountID: account, Protocol: in.Protocol,
		Endpoint: ep, RawMessageDelivery: in.RawMessageDelivery, CreatedAt: db.Now()}
	if err := s.Repo.InsertSubscription(ctx, sub); err != nil {
		return nil, RepoErr(err, "subscription "+in.Protocol+":"+ep)
	}
	s.RecordEvent(ctx, account, t.TopicID, "topic", "subscribed", fmt.Sprintf("Subscribed %s:%s to topic %s", in.Protocol, ep, topic), nil)
	return sub, nil
}

func (s *SNSService) ListSubscriptions(ctx context.Context, account, topic string) ([]repositories.Subscription, error) {
	t, err := s.get(ctx, account, topic)
	if err != nil {
		return nil, err
	}
	return s.Repo.ListSubscriptions(ctx, t.TopicID)
}

func (s *SNSService) Unsubscribe(ctx context.Context, account, topic, id string) error {
	t, err := s.get(ctx, account, topic)
	if err != nil {
		return err
	}
	return RepoErr(s.Repo.DeleteSubscription(ctx, t.TopicID, id), "subscription "+id)
}

type PublishInput struct {
	Message string `json:"message"`
	Subject string `json:"subject"`
}

type PublishResult struct {
	MessageID  string                  `json:"message_id"`
	Deliveries []repositories.Delivery `json:"deliveries"`
}

func (s *SNSService) Publish(ctx context.Context, account, topic string, in PublishInput) (*PublishResult, error) {
	t, err := s.get(ctx, account, topic)
	if err != nil {
		return nil, err
	}
	if in.Message == "" {
		return nil, apierr.Validation("message must not be empty")
	}
	if len(in.Message) > sqsMaxBody || !utf8.ValidString(in.Message) {
		return nil, apierr.Validation("message must be valid UTF-8 and at most %d bytes", sqsMaxBody)
	}
	if len(in.Subject) > 100 || strings.ContainsAny(in.Subject, "\r\n") {
		return nil, apierr.Validation("subject must be at most 100 characters on a single line")
	}
	subs, err := s.Repo.ListSubscriptions(ctx, t.TopicID)
	if err != nil {
		return nil, err
	}
	msgID := ids.Token()[:32]
	now := db.Now()
	envelope, _ := json.Marshal(map[string]any{"Type": "Notification", "MessageId": msgID, "TopicArn": TopicARN(account, t.Name),
		"Subject": in.Subject, "Message": in.Message, "Timestamp": now})
	res := &PublishResult{MessageID: msgID, Deliveries: []repositories.Delivery{}}
	for _, sub := range subs {
		payload := string(envelope)
		if sub.RawMessageDelivery {
			payload = in.Message
		}
		d := repositories.Delivery{TopicID: t.TopicID, MessageID: msgID, SubscriptionID: sub.SubscriptionID, Protocol: sub.Protocol,
			Endpoint: sub.Endpoint, Status: "delivered", TS: now}
		switch sub.Protocol {
		case "sqs":
			if _, err := s.SQS.SendByName(ctx, account, sub.Endpoint, payload); err != nil {
				d.Status, d.Error = "failed", apierr.As(err).Message
			}
		case "log":
			s.Logs.Append(ctx, account, sub.Endpoint, "sns/"+t.Name, []string{payload})
		}
		if err := s.Repo.InsertDelivery(ctx, &d); err != nil {
			return nil, err
		}
		res.Deliveries = append(res.Deliveries, d)
	}
	failed := 0
	for _, d := range res.Deliveries {
		if d.Status == "failed" {
			failed++
		}
	}
	s.RecordEvent(ctx, account, t.TopicID, "topic", "published",
		fmt.Sprintf("Message %s published to %s: %d deliveries, %d failed", msgID, t.Name, len(res.Deliveries), failed),
		map[string]any{"message_id": msgID, "deliveries": len(res.Deliveries), "failed": failed})
	return res, nil
}

// PublishARN publishes to a topic identified by its CloudSim ARN (used by CloudWatch alarm actions).
func (s *SNSService) PublishARN(ctx context.Context, account, arn, subject, message string) error {
	i := strings.LastIndex(arn, ":")
	if !strings.HasPrefix(arn, "arn:cloudsim:sns:") || i < 0 {
		return apierr.Validation("not an SNS topic ARN: %s", arn)
	}
	_, err := s.Publish(ctx, account, arn[i+1:], PublishInput{Message: message, Subject: subject})
	return err
}

func (s *SNSService) Deliveries(ctx context.Context, account, topic string, limit int) ([]repositories.Delivery, error) {
	t, err := s.get(ctx, account, topic)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.Repo.ListDeliveries(ctx, t.TopicID, limit)
}

func (s *SNSService) Resync(ctx context.Context) error {
	ts, err := s.Repo.ListAllTopics(ctx)
	if err != nil {
		return err
	}
	for _, t := range ts {
		if err := s.resyncSimple(ctx, t.TopicID, "topic", t.AccountID, nil); err != nil {
			return err
		}
	}
	return nil
}
