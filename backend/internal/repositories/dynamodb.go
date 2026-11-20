package repositories

import (
	"context"
	"database/sql"

	"cloudsim/backend/internal/db"
)

// DDBTable is a DynamoDB-style table definition.
type DDBTable struct {
	TableID   string `json:"table_id"`
	AccountID string `json:"-"`
	Name      string `json:"table_name"`
	PKName    string `json:"-"`
	PKType    string `json:"-"`
	SKName    string `json:"-"`
	SKType    string `json:"-"`
	CreatedAt string `json:"created_at"`
}

// DDBItem is a stored item; PK/SK are canonical key strings.
type DDBItem struct {
	PK        string
	SK        string
	SKNum     *float64
	Item      string
	SizeBytes int
}

// DDBQuery describes a key-condition query.
type DDBQuery struct {
	TableID    string
	PK         string
	SKPrefix   string
	NumericSK  bool
	Descending bool
	Limit      int
	AfterSK    *string // exclusive start sort key (canonical)
	AfterSKNum *float64
}

type DynamoDBRepo interface {
	InsertTable(ctx context.Context, t *DDBTable) error
	GetTable(ctx context.Context, account, name string) (*DDBTable, error)
	ListTables(ctx context.Context, account string) ([]DDBTable, error)
	ListAllTables(ctx context.Context) ([]DDBTable, error)
	DeleteTable(ctx context.Context, id string) error
	Stats(ctx context.Context, tableID string) (count int64, size int64, err error)
	PutItem(ctx context.Context, tableID string, it *DDBItem, now string) error
	GetItem(ctx context.Context, tableID, pk, sk string) (*DDBItem, error)
	DeleteItem(ctx context.Context, tableID, pk, sk string) (bool, error)
	Query(ctx context.Context, q DDBQuery) ([]DDBItem, error)
	// Scan returns up to limit items ordered by (pk, sk) strictly after (afterPK, afterSK) when after is set.
	Scan(ctx context.Context, tableID string, limit int, after bool, afterPK, afterSK string) ([]DDBItem, error)
}

type dynamoRepo struct{ d *db.DB }

func NewDynamoDBRepo(d *db.DB) DynamoDBRepo { return &dynamoRepo{d} }

const ddbTableCols = `table_id, account_id, name, pk_name, pk_type, sk_name, sk_type, created_at`

func scanDDBTable(sc interface{ Scan(...any) error }) (*DDBTable, error) {
	var t DDBTable
	err := sc.Scan(&t.TableID, &t.AccountID, &t.Name, &t.PKName, &t.PKType, &t.SKName, &t.SKType, &t.CreatedAt)
	return &t, err
}

func (r *dynamoRepo) InsertTable(ctx context.Context, t *DDBTable) error {
	_, err := r.d.ExecContext(ctx, `INSERT INTO ddb_tables(`+ddbTableCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		t.TableID, t.AccountID, t.Name, t.PKName, t.PKType, t.SKName, t.SKType, t.CreatedAt)
	return err
}

func (r *dynamoRepo) GetTable(ctx context.Context, account, name string) (*DDBTable, error) {
	t, err := scanDDBTable(r.d.QueryRowContext(ctx, `SELECT `+ddbTableCols+` FROM ddb_tables WHERE account_id=? AND name=?`, account, name))
	return t, notFound(err)
}

func (r *dynamoRepo) listTables(ctx context.Context, where string, args ...any) ([]DDBTable, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+ddbTableCols+` FROM ddb_tables`+where+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DDBTable{}
	for rows.Next() {
		t, err := scanDDBTable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *dynamoRepo) ListTables(ctx context.Context, account string) ([]DDBTable, error) {
	return r.listTables(ctx, ` WHERE account_id=?`, account)
}

func (r *dynamoRepo) ListAllTables(ctx context.Context) ([]DDBTable, error) {
	return r.listTables(ctx, "")
}

func (r *dynamoRepo) DeleteTable(ctx context.Context, id string) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM ddb_tables WHERE table_id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *dynamoRepo) Stats(ctx context.Context, tableID string) (int64, int64, error) {
	var n, size int64
	err := r.d.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes),0) FROM ddb_items WHERE table_id=?`, tableID).Scan(&n, &size)
	return n, size, err
}

func (r *dynamoRepo) PutItem(ctx context.Context, tableID string, it *DDBItem, now string) error {
	_, err := r.d.ExecContext(ctx, `INSERT INTO ddb_items(table_id, pk, sk, sk_num, item, size_bytes, updated_at) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(table_id, pk, sk) DO UPDATE SET sk_num=excluded.sk_num, item=excluded.item, size_bytes=excluded.size_bytes, updated_at=excluded.updated_at`,
		tableID, it.PK, it.SK, it.SKNum, it.Item, it.SizeBytes, now)
	return err
}

func scanDDBItem(sc interface{ Scan(...any) error }) (*DDBItem, error) {
	var it DDBItem
	var num sql.NullFloat64
	err := sc.Scan(&it.PK, &it.SK, &num, &it.Item, &it.SizeBytes)
	if num.Valid {
		it.SKNum = &num.Float64
	}
	return &it, err
}

func (r *dynamoRepo) GetItem(ctx context.Context, tableID, pk, sk string) (*DDBItem, error) {
	it, err := scanDDBItem(r.d.QueryRowContext(ctx, `SELECT pk, sk, sk_num, item, size_bytes FROM ddb_items WHERE table_id=? AND pk=? AND sk=?`, tableID, pk, sk))
	return it, notFound(err)
}

func (r *dynamoRepo) DeleteItem(ctx context.Context, tableID, pk, sk string) (bool, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM ddb_items WHERE table_id=? AND pk=? AND sk=?`, tableID, pk, sk)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *dynamoRepo) items(ctx context.Context, query string, args ...any) ([]DDBItem, error) {
	rows, err := r.d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DDBItem{}
	for rows.Next() {
		it, err := scanDDBItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

func (r *dynamoRepo) Query(ctx context.Context, q DDBQuery) ([]DDBItem, error) {
	where := ` WHERE table_id=? AND pk=?`
	args := []any{q.TableID, q.PK}
	if q.SKPrefix != "" {
		// substr comparison avoids LIKE wildcard/escaping pitfalls and is case-sensitive like DynamoDB.
		where += ` AND substr(sk, 1, ?) = ?`
		args = append(args, len(q.SKPrefix), q.SKPrefix)
	}
	cmp, dir := ">", "ASC"
	if q.Descending {
		cmp, dir = "<", "DESC"
	}
	order := ` ORDER BY sk ` + dir
	if q.NumericSK {
		order = ` ORDER BY sk_num ` + dir + `, sk ` + dir
		if q.AfterSKNum != nil {
			where += ` AND (sk_num ` + cmp + ` ? OR (sk_num = ? AND sk ` + cmp + ` ?))`
			args = append(args, *q.AfterSKNum, *q.AfterSKNum, *q.AfterSK)
		}
	} else if q.AfterSK != nil {
		where += ` AND sk ` + cmp + ` ?`
		args = append(args, *q.AfterSK)
	}
	args = append(args, q.Limit)
	return r.items(ctx, `SELECT pk, sk, sk_num, item, size_bytes FROM ddb_items`+where+order+` LIMIT ?`, args...)
}

func (r *dynamoRepo) Scan(ctx context.Context, tableID string, limit int, after bool, afterPK, afterSK string) ([]DDBItem, error) {
	if after {
		return r.items(ctx, `SELECT pk, sk, sk_num, item, size_bytes FROM ddb_items WHERE table_id=? AND (pk > ? OR (pk = ? AND sk > ?))
 ORDER BY pk, sk LIMIT ?`, tableID, afterPK, afterPK, afterSK, limit)
	}
	return r.items(ctx, `SELECT pk, sk, sk_num, item, size_bytes FROM ddb_items WHERE table_id=? ORDER BY pk, sk LIMIT ?`, tableID, limit)
}
