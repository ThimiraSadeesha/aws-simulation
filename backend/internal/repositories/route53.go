package repositories

import (
	"context"
	"database/sql"
	"encoding/json"

	"cloudsim/backend/internal/db"
)

// HostedZone is a Route 53-style hosted zone.
type HostedZone struct {
	ZoneID      string `json:"zone_id"`
	AccountID   string `json:"-"`
	Name        string `json:"name"`
	Private     bool   `json:"private"`
	VPCID       string `json:"vpc_id"`
	Comment     string `json:"comment"`
	CreatedAt   string `json:"created_at"`
	RecordCount int    `json:"record_count"`
}

// RecordSet is a resource record set (name+type unique within a zone).
type RecordSet struct {
	ZoneID    string   `json:"zone_id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	TTL       int      `json:"ttl"`
	Values    []string `json:"values"`
	UpdatedAt string   `json:"updated_at"`
}

type Route53Repo interface {
	// InsertZone creates the zone together with its default NS/SOA records.
	InsertZone(ctx context.Context, z *HostedZone, defaults []RecordSet) error
	GetZone(ctx context.Context, account, id string) (*HostedZone, error)
	ListZones(ctx context.Context, account string) ([]HostedZone, error)
	ListAllZones(ctx context.Context) ([]HostedZone, error)
	DeleteZone(ctx context.Context, id string) error
	ListRecords(ctx context.Context, zoneID string) ([]RecordSet, error)
	RecordsByName(ctx context.Context, zoneID, name string) ([]RecordSet, error)
	UpsertRecord(ctx context.Context, r *RecordSet) error
	DeleteRecord(ctx context.Context, zoneID, name, typ string) (bool, error)
}

type route53Repo struct{ d *db.DB }

func NewRoute53Repo(d *db.DB) Route53Repo { return &route53Repo{d} }

const zoneCols = `z.zone_id, z.account_id, z.name, z.private, z.vpc_id, z.comment, z.created_at,
 (SELECT COUNT(*) FROM r53_records r WHERE r.zone_id = z.zone_id)`

func scanZone(sc interface{ Scan(...any) error }) (*HostedZone, error) {
	var z HostedZone
	err := sc.Scan(&z.ZoneID, &z.AccountID, &z.Name, &z.Private, &z.VPCID, &z.Comment, &z.CreatedAt, &z.RecordCount)
	return &z, err
}

func insertRecord(ctx context.Context, q Querier, r *RecordSet) error {
	vals, _ := json.Marshal(r.Values)
	_, err := q.ExecContext(ctx, `INSERT INTO r53_records(zone_id, name, type, ttl, value_json, updated_at) VALUES(?,?,?,?,?,?)
 ON CONFLICT(zone_id, name, type) DO UPDATE SET ttl=excluded.ttl, value_json=excluded.value_json, updated_at=excluded.updated_at`,
		r.ZoneID, r.Name, r.Type, r.TTL, string(vals), r.UpdatedAt)
	return err
}

func (r *route53Repo) InsertZone(ctx context.Context, z *HostedZone, defaults []RecordSet) error {
	return r.d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO r53_zones(zone_id, account_id, name, private, vpc_id, comment, created_at) VALUES(?,?,?,?,?,?,?)`,
			z.ZoneID, z.AccountID, z.Name, b2i(z.Private), z.VPCID, z.Comment, z.CreatedAt); err != nil {
			return err
		}
		for i := range defaults {
			if err := insertRecord(ctx, tx, &defaults[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *route53Repo) GetZone(ctx context.Context, account, id string) (*HostedZone, error) {
	z, err := scanZone(r.d.QueryRowContext(ctx, `SELECT `+zoneCols+` FROM r53_zones z WHERE z.account_id=? AND z.zone_id=?`, account, id))
	return z, notFound(err)
}

func (r *route53Repo) listZones(ctx context.Context, where string, args ...any) ([]HostedZone, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT `+zoneCols+` FROM r53_zones z`+where+` ORDER BY z.name, z.zone_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HostedZone{}
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *z)
	}
	return out, rows.Err()
}

func (r *route53Repo) ListZones(ctx context.Context, account string) ([]HostedZone, error) {
	return r.listZones(ctx, ` WHERE z.account_id=?`, account)
}

func (r *route53Repo) ListAllZones(ctx context.Context) ([]HostedZone, error) {
	return r.listZones(ctx, "")
}

func (r *route53Repo) DeleteZone(ctx context.Context, id string) error {
	res, err := r.d.ExecContext(ctx, `DELETE FROM r53_zones WHERE zone_id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *route53Repo) records(ctx context.Context, where string, args ...any) ([]RecordSet, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT zone_id, name, type, ttl, value_json, updated_at FROM r53_records`+where+` ORDER BY name, type`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecordSet{}
	for rows.Next() {
		var rs RecordSet
		var vals string
		if err := rows.Scan(&rs.ZoneID, &rs.Name, &rs.Type, &rs.TTL, &vals, &rs.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(vals), &rs.Values)
		if rs.Values == nil {
			rs.Values = []string{}
		}
		out = append(out, rs)
	}
	return out, rows.Err()
}

func (r *route53Repo) ListRecords(ctx context.Context, zoneID string) ([]RecordSet, error) {
	return r.records(ctx, ` WHERE zone_id=?`, zoneID)
}

func (r *route53Repo) RecordsByName(ctx context.Context, zoneID, name string) ([]RecordSet, error) {
	return r.records(ctx, ` WHERE zone_id=? AND name=?`, zoneID, name)
}

func (r *route53Repo) UpsertRecord(ctx context.Context, rs *RecordSet) error {
	return insertRecord(ctx, r.d, rs)
}

func (r *route53Repo) DeleteRecord(ctx context.Context, zoneID, name, typ string) (bool, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM r53_records WHERE zone_id=? AND name=? AND type=?`, zoneID, name, typ)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
