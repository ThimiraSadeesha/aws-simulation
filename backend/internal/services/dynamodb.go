package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
)

const (
	ddbMaxItemBytes = 400 * 1024
	ddbMaxPKBytes   = 2048
	ddbMaxSKBytes   = 1024
)

var ddbTableNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,255}$`)

// DynamoDBService implements DynamoDB-style key/value tables over SQLite.
type DynamoDBService struct {
	*Deps
	Repo repositories.DynamoDBRepo
}

func NewDynamoDBService(d *Deps) *DynamoDBService {
	return &DynamoDBService{Deps: d, Repo: repositories.NewDynamoDBRepo(d.Store.DB)}
}

type KeySchema struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type TableView struct {
	TableName      string     `json:"table_name"`
	TableID        string     `json:"table_id"`
	ARN            string     `json:"arn"`
	PartitionKey   KeySchema  `json:"partition_key"`
	SortKey        *KeySchema `json:"sort_key"`
	Status         string     `json:"status"`
	ItemCount      int64      `json:"item_count"`
	TableSizeBytes int64      `json:"table_size_bytes"`
	CreatedAt      string     `json:"created_at"`
}

func TableARN(account, name string) string { return auth.ARN("dynamodb", account, "table/"+name) }

func (s *DynamoDBService) view(ctx context.Context, t *repositories.DDBTable) (*TableView, error) {
	n, size, err := s.Repo.Stats(ctx, t.TableID)
	if err != nil {
		return nil, err
	}
	v := &TableView{TableName: t.Name, TableID: t.TableID, ARN: TableARN(t.AccountID, t.Name), PartitionKey: KeySchema{t.PKName, t.PKType},
		Status: "ACTIVE", ItemCount: n, TableSizeBytes: size, CreatedAt: t.CreatedAt}
	if t.SKName != "" {
		v.SortKey = &KeySchema{t.SKName, t.SKType}
	}
	return v, nil
}

type CreateTableInput struct {
	TableName    string     `json:"table_name"`
	PartitionKey *KeySchema `json:"partition_key"`
	SortKey      *KeySchema `json:"sort_key"`
}

func validKeySchema(role string, k *KeySchema) error {
	if k.Name == "" || len(k.Name) > 255 {
		return apierr.Validation("%s name must be 1-255 characters", role)
	}
	if k.Type != "S" && k.Type != "N" {
		return apierr.Validation("%s type must be S or N (B is not supported)", role)
	}
	return nil
}

func (s *DynamoDBService) Create(ctx context.Context, account string, in CreateTableInput) (*TableView, error) {
	if !ddbTableNameRe.MatchString(in.TableName) {
		return nil, apierr.Validation("table_name must be 3-255 characters of letters, digits, '_', '-' and '.'")
	}
	if in.PartitionKey == nil {
		return nil, apierr.Validation("partition_key is required")
	}
	if err := validKeySchema("partition_key", in.PartitionKey); err != nil {
		return nil, err
	}
	t := &repositories.DDBTable{TableID: ids.New("ddb"), AccountID: account, Name: in.TableName, PKName: in.PartitionKey.Name,
		PKType: in.PartitionKey.Type, CreatedAt: db.Now()}
	if in.SortKey != nil {
		if err := validKeySchema("sort_key", in.SortKey); err != nil {
			return nil, err
		}
		if in.SortKey.Name == in.PartitionKey.Name {
			return nil, apierr.Validation("sort_key must differ from partition_key")
		}
		t.SKName, t.SKType = in.SortKey.Name, in.SortKey.Type
	}
	if _, err := s.Repo.GetTable(ctx, account, in.TableName); err == nil {
		return nil, apierr.Conflict("table %s already exists", in.TableName)
	}
	if err := s.registerSimple(ctx, t.TableID, "dynamodb_table", account, nil, map[string]any{"name": t.Name}); err != nil {
		return nil, err
	}
	if err := s.Repo.InsertTable(ctx, t); err != nil {
		_ = s.Engine.Deregister(ctx, t.TableID)
		return nil, RepoErr(err, "table "+in.TableName)
	}
	s.RecordEvent(ctx, account, t.TableID, "dynamodb_table", "created", "Table "+t.Name+" created", nil)
	return s.view(ctx, t)
}

func (s *DynamoDBService) table(ctx context.Context, account, name string) (*repositories.DDBTable, error) {
	t, err := s.Repo.GetTable(ctx, account, name)
	if err != nil {
		return nil, RepoErr(err, "table "+name)
	}
	return t, nil
}

func (s *DynamoDBService) Get(ctx context.Context, account, name string) (*TableView, error) {
	t, err := s.table(ctx, account, name)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, t)
}

func (s *DynamoDBService) List(ctx context.Context, account string) ([]TableView, error) {
	l, err := s.Repo.ListTables(ctx, account)
	if err != nil {
		return nil, err
	}
	out := make([]TableView, 0, len(l))
	for i := range l {
		v, err := s.view(ctx, &l[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

func (s *DynamoDBService) Delete(ctx context.Context, account, name string) error {
	t, err := s.table(ctx, account, name)
	if err != nil {
		return err
	}
	if err := s.deregister(ctx, t.TableID); err != nil {
		return err
	}
	if err := s.Repo.DeleteTable(ctx, t.TableID); err != nil {
		return RepoErr(err, "table "+name)
	}
	s.RecordEvent(ctx, account, t.TableID, "dynamodb_table", "deleted", "Table "+name+" deleted", nil)
	return nil
}

// canonKey validates a key value of type typ and returns its canonical string (and numeric value for N).
// strict requires the JSON type to match (item attributes); otherwise strings are parsed (query params).
func canonKey(attr, typ string, v any, strict bool, maxBytes int) (string, *float64, error) {
	switch typ {
	case "S":
		str, ok := v.(string)
		if !ok {
			return "", nil, apierr.Validation("key attribute %s must be a string (type S)", attr)
		}
		if str == "" || len(str) > maxBytes {
			return "", nil, apierr.Validation("key attribute %s must be 1-%d bytes", attr, maxBytes)
		}
		return str, nil, nil
	default: // N
		var raw string
		switch x := v.(type) {
		case json.Number:
			raw = x.String()
		case float64:
			raw = strconv.FormatFloat(x, 'g', -1, 64)
		case string:
			if strict {
				return "", nil, apierr.Validation("key attribute %s must be a number (type N)", attr)
			}
			raw = x
		default:
			return "", nil, apierr.Validation("key attribute %s must be a number (type N)", attr)
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return "", nil, apierr.Validation("key attribute %s must be a valid number, got %q", attr, raw)
		}
		return strconv.FormatFloat(f, 'g', -1, 64), &f, nil
	}
}

func decodeJSONNumber(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return dec.Decode(v)
}

// PutItem validates and stores an item (plain JSON object) — full replacement like DynamoDB PutItem.
func (s *DynamoDBService) PutItem(ctx context.Context, account, name string, raw json.RawMessage) (map[string]any, error) {
	t, err := s.table(ctx, account, name)
	if err != nil {
		return nil, err
	}
	var item map[string]any
	if err := decodeJSONNumber(raw, &item); err != nil || item == nil {
		return nil, apierr.Validation("item must be a JSON object")
	}
	if len(item) == 0 {
		return nil, apierr.Validation("item must not be empty")
	}
	for k := range item {
		if k == "" || len(k) > 255 {
			return nil, apierr.Validation("attribute names must be 1-255 characters")
		}
	}
	pv, ok := item[t.PKName]
	if !ok {
		return nil, apierr.Validation("item is missing partition key attribute %s", t.PKName)
	}
	it := &repositories.DDBItem{}
	if it.PK, _, err = canonKey(t.PKName, t.PKType, pv, true, ddbMaxPKBytes); err != nil {
		return nil, err
	}
	if t.SKName != "" {
		sv, ok := item[t.SKName]
		if !ok {
			return nil, apierr.Validation("item is missing sort key attribute %s", t.SKName)
		}
		if it.SK, it.SKNum, err = canonKey(t.SKName, t.SKType, sv, true, ddbMaxSKBytes); err != nil {
			return nil, err
		}
	}
	b, _ := json.Marshal(item)
	if len(b) > ddbMaxItemBytes {
		return nil, apierr.Validation("item size %d bytes exceeds the 400 KB limit", len(b))
	}
	it.Item, it.SizeBytes = string(b), len(b)
	if err := s.Repo.PutItem(ctx, t.TableID, it, db.Now()); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *DynamoDBService) keyArgs(t *repositories.DDBTable, pk, sk string, skSet bool) (string, string, error) {
	if pk == "" {
		return "", "", apierr.Validation("pk is required")
	}
	p, _, err := canonKey(t.PKName, t.PKType, pk, false, ddbMaxPKBytes)
	if err != nil {
		return "", "", err
	}
	if t.SKName == "" {
		if skSet && sk != "" {
			return "", "", apierr.Validation("table %s has no sort key", t.Name)
		}
		return p, "", nil
	}
	if sk == "" {
		return "", "", apierr.Validation("sk is required: table %s has sort key %s", t.Name, t.SKName)
	}
	sc, _, err := canonKey(t.SKName, t.SKType, sk, false, ddbMaxSKBytes)
	return p, sc, err
}

func itemMap(it *repositories.DDBItem) map[string]any {
	m := map[string]any{}
	_ = decodeJSONNumber([]byte(it.Item), &m)
	return m
}

func (s *DynamoDBService) GetItem(ctx context.Context, account, name, pk, sk string) (map[string]any, error) {
	t, err := s.table(ctx, account, name)
	if err != nil {
		return nil, err
	}
	p, sc, err := s.keyArgs(t, pk, sk, true)
	if err != nil {
		return nil, err
	}
	it, err := s.Repo.GetItem(ctx, t.TableID, p, sc)
	if err != nil {
		return nil, RepoErr(err, "item")
	}
	return itemMap(it), nil
}

func (s *DynamoDBService) DeleteItem(ctx context.Context, account, name, pk, sk string) error {
	t, err := s.table(ctx, account, name)
	if err != nil {
		return err
	}
	p, sc, err := s.keyArgs(t, pk, sk, true)
	if err != nil {
		return err
	}
	_, err = s.Repo.DeleteItem(ctx, t.TableID, p, sc) // idempotent, like DynamoDB DeleteItem
	return err
}

type QueryInput struct {
	PK                any    `json:"pk"`
	SKBeginsWith      string `json:"sk_begins_with"`
	ScanForward       *bool  `json:"scan_forward"`
	Limit             int    `json:"limit"`
	ExclusiveStartKey string `json:"exclusive_start_key"`
}

type ItemsPage struct {
	Items            []map[string]any `json:"items"`
	Count            int              `json:"count"`
	LastEvaluatedKey *string          `json:"last_evaluated_key"`
}

func encodeToken(parts ...string) string {
	b, _ := json.Marshal(parts)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeToken(tok string, n int) ([]string, error) {
	b, err := base64.RawURLEncoding.DecodeString(tok)
	var parts []string
	if err != nil || json.Unmarshal(b, &parts) != nil || len(parts) != n {
		return nil, apierr.Validation("invalid exclusive_start_key")
	}
	return parts, nil
}

func pageLimit(l, def, max int) (int, error) {
	if l == 0 {
		return def, nil
	}
	if l < 1 || l > max {
		return 0, apierr.Validation("limit must be between 1 and %d", max)
	}
	return l, nil
}

// Query returns the items of one partition ordered by sort key (numerically for N sort keys).
func (s *DynamoDBService) Query(ctx context.Context, account, name string, in QueryInput) (*ItemsPage, error) {
	t, err := s.table(ctx, account, name)
	if err != nil {
		return nil, err
	}
	if in.PK == nil || in.PK == "" {
		return nil, apierr.Validation("pk is required")
	}
	pk, _, err := canonKey(t.PKName, t.PKType, in.PK, false, ddbMaxPKBytes)
	if err != nil {
		return nil, err
	}
	limit, err := pageLimit(in.Limit, 100, 1000)
	if err != nil {
		return nil, err
	}
	q := repositories.DDBQuery{TableID: t.TableID, PK: pk, NumericSK: t.SKType == "N", Limit: limit + 1}
	if in.ScanForward != nil && !*in.ScanForward {
		q.Descending = true
	}
	if in.SKBeginsWith != "" {
		if t.SKName == "" {
			return nil, apierr.Validation("table %s has no sort key; sk_begins_with is not applicable", t.Name)
		}
		if t.SKType != "S" {
			return nil, apierr.Validation("begins_with is only supported on string (S) sort keys")
		}
		q.SKPrefix = in.SKBeginsWith
	}
	if in.ExclusiveStartKey != "" {
		parts, err := decodeToken(in.ExclusiveStartKey, 1)
		if err != nil {
			return nil, err
		}
		q.AfterSK = &parts[0]
		if q.NumericSK {
			f, err := strconv.ParseFloat(parts[0], 64)
			if err != nil {
				return nil, apierr.Validation("invalid exclusive_start_key")
			}
			q.AfterSKNum = &f
		}
	}
	items, err := s.Repo.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return s.page(items, limit, func(it repositories.DDBItem) string { return encodeToken(it.SK) }), nil
}

func (s *DynamoDBService) page(items []repositories.DDBItem, limit int, tok func(repositories.DDBItem) string) *ItemsPage {
	p := &ItemsPage{Items: []map[string]any{}}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	for i := range items {
		p.Items = append(p.Items, itemMap(&items[i]))
	}
	p.Count = len(p.Items)
	if more && len(items) > 0 {
		t := tok(items[len(items)-1])
		p.LastEvaluatedKey = &t
	}
	return p
}

// Scan returns all items of a table in key order, paginated with limit/exclusive_start_key.
func (s *DynamoDBService) Scan(ctx context.Context, account, name string, limit int, start string) (*ItemsPage, error) {
	t, err := s.table(ctx, account, name)
	if err != nil {
		return nil, err
	}
	limit, err = pageLimit(limit, 100, 1000)
	if err != nil {
		return nil, err
	}
	var after bool
	var apk, ask string
	if start != "" {
		parts, err := decodeToken(start, 2)
		if err != nil {
			return nil, err
		}
		after, apk, ask = true, parts[0], parts[1]
	}
	items, err := s.Repo.Scan(ctx, t.TableID, limit+1, after, apk, ask)
	if err != nil {
		return nil, err
	}
	return s.page(items, limit, func(it repositories.DDBItem) string { return encodeToken(it.PK, it.SK) }), nil
}

func (s *DynamoDBService) Resync(ctx context.Context) error {
	ts, err := s.Repo.ListAllTables(ctx)
	if err != nil {
		return err
	}
	for _, t := range ts {
		if err := s.resyncSimple(ctx, t.TableID, "dynamodb_table", t.AccountID, nil); err != nil {
			return fmt.Errorf("dynamodb resync %s: %w", t.TableID, err)
		}
	}
	return nil
}
