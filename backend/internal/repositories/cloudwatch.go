package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"cloudsim/backend/internal/db"
)

// MetricDatum is one stored sample.
type MetricDatum struct {
	AccountID  string
	Namespace  string
	MetricName string
	DimKey     string // canonical JSON (see CanonicalDims)
	TS         string // db.TimeFormat
	Value      float64
	Unit       string
}

// CanonicalDims renders dimensions as JSON with sorted keys ("{}" when empty).
func CanonicalDims(d map[string]string) string {
	if len(d) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b := []byte{'{'}
	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(d[k])
		b = append(b, kb...)
		b = append(b, ':')
		b = append(b, vb...)
	}
	return string(append(b, '}'))
}

// ParseDims decodes a dim_key.
func ParseDims(key string) map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal([]byte(key), &m)
	return m
}

type MetricDescriptor struct {
	Namespace  string            `json:"namespace"`
	MetricName string            `json:"metric_name"`
	Dimensions map[string]string `json:"dimensions"`
}

// MetricQuery selects one series (or, with DimLike, every series whose dim_key matches a LIKE pattern).
type MetricQuery struct {
	AccountID  string
	Namespace  string
	MetricName string
	DimKey     string
	DimLike    string
	Start, End string // db.TimeFormat, [Start, End)
	Origin     int64  // epoch seconds bucket 0 starts at
	Period     int    // seconds
	Stat       string // Average|Maximum|Minimum|Sum|SampleCount
}

type Bucket struct {
	Start int64 // epoch seconds
	Value float64
	Count int
}

type Alarm struct {
	AlarmID            string            `json:"alarm_id"`
	AccountID          string            `json:"-"`
	AlarmName          string            `json:"alarm_name"`
	ARN                string            `json:"arn"`
	Namespace          string            `json:"namespace"`
	MetricName         string            `json:"metric_name"`
	DimKey             string            `json:"-"`
	Dimensions         map[string]string `json:"dimensions"`
	Statistic          string            `json:"statistic"`
	Period             int               `json:"period"`
	EvaluationPeriods  int               `json:"evaluation_periods"`
	Threshold          float64           `json:"threshold"`
	ComparisonOperator string            `json:"comparison_operator"`
	AlarmActions       []string          `json:"alarm_actions"`
	State              string            `json:"state"`
	StateReason        string            `json:"state_reason"`
	StateUpdatedAt     string            `json:"state_updated_at"`
	CreatedAt          string            `json:"created_at"`
}

type AlarmHistory struct {
	ID       int64  `json:"id"`
	AlarmID  string `json:"-"`
	TS       string `json:"ts"`
	Type     string `json:"type"`
	OldState string `json:"old_state"`
	NewState string `json:"new_state"`
	Summary  string `json:"summary"`
}

type LogGroup struct {
	ID            int64  `json:"-"`
	AccountID     string `json:"-"`
	Name          string `json:"name"`
	ARN           string `json:"arn"`
	RetentionDays *int   `json:"retention_days"`
	StoredBytes   int64  `json:"stored_bytes"`
	CreatedAt     string `json:"created_at"`
}

type LogStream struct {
	ID          int64   `json:"-"`
	Name        string  `json:"name"`
	CreatedAt   string  `json:"created_at"`
	LastEventTS *string `json:"last_event_ts"`
	StoredBytes int64   `json:"stored_bytes"`
}

type LogEvent struct {
	ID      int64  `json:"id"`
	TS      string `json:"ts"`
	Stream  string `json:"stream"`
	Message string `json:"message"`
}

type LogEventInput struct {
	TS      string
	Message string
}

type LogQuery struct {
	GroupID    int64
	Stream     string
	Start, End string
	Limit      int
}

// CloudWatchRepo persists metrics, alarms and logs.
type CloudWatchRepo interface {
	InsertMetrics(ctx context.Context, data []MetricDatum) error
	ListMetrics(ctx context.Context, account, namespace, metricName string) ([]MetricDescriptor, error)
	Aggregate(ctx context.Context, q MetricQuery) ([]Bucket, error)
	SeriesAverages(ctx context.Context, account, namespace, metricName, since string) (map[string]float64, error)
	PruneMetrics(ctx context.Context, before string) (int64, error)

	UpsertAlarm(ctx context.Context, a *Alarm) (created bool, err error)
	GetAlarm(ctx context.Context, account, name string) (*Alarm, error)
	ListAlarms(ctx context.Context, account string) ([]Alarm, error) // "" = all
	DeleteAlarm(ctx context.Context, account, name string) (bool, error)
	SetAlarmState(ctx context.Context, alarmID, state, reason string) error
	InsertAlarmHistory(ctx context.Context, h *AlarmHistory) error
	ListAlarmHistory(ctx context.Context, alarmID string, limit int) ([]AlarmHistory, error)

	CreateLogGroup(ctx context.Context, g *LogGroup) error
	EnsureLogGroup(ctx context.Context, account, name string) (int64, error)
	GetLogGroup(ctx context.Context, account, name string) (*LogGroup, error)
	ListLogGroups(ctx context.Context, account string) ([]LogGroup, error)
	DeleteLogGroup(ctx context.Context, account, name string) (bool, error)
	ListLogStreams(ctx context.Context, groupID int64) ([]LogStream, error)
	PutLogEvents(ctx context.Context, groupID int64, stream string, events []LogEventInput) error
	QueryLogEvents(ctx context.Context, q LogQuery) ([]LogEvent, error)
	PruneLogs(ctx context.Context, now string) (int64, error)
}

func NewCloudWatchRepo(d *db.DB) CloudWatchRepo { return &cwRepo{d} }

type cwRepo struct{ d *db.DB }

func (r *cwRepo) InsertMetrics(ctx context.Context, data []MetricDatum) error {
	if len(data) == 0 {
		return nil
	}
	return r.d.Tx(ctx, func(tx *sql.Tx) error {
		st, err := tx.PrepareContext(ctx, `INSERT INTO cw_metric_data(account_id,namespace,metric_name,dim_key,ts,value,unit) VALUES(?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, m := range data {
			if m.DimKey == "" {
				m.DimKey = "{}"
			}
			if m.Unit == "" {
				m.Unit = "None"
			}
			if _, err := st.ExecContext(ctx, m.AccountID, m.Namespace, m.MetricName, m.DimKey, m.TS, m.Value, m.Unit); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *cwRepo) ListMetrics(ctx context.Context, account, namespace, metricName string) ([]MetricDescriptor, error) {
	q := `SELECT DISTINCT namespace, metric_name, dim_key FROM cw_metric_data WHERE account_id=?`
	args := []any{account}
	if namespace != "" {
		q += ` AND namespace=?`
		args = append(args, namespace)
	}
	if metricName != "" {
		q += ` AND metric_name=?`
		args = append(args, metricName)
	}
	rows, err := r.d.QueryContext(ctx, q+` ORDER BY namespace, metric_name, dim_key`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricDescriptor{}
	for rows.Next() {
		var d MetricDescriptor
		var key string
		if err := rows.Scan(&d.Namespace, &d.MetricName, &key); err != nil {
			return nil, err
		}
		d.Dimensions = ParseDims(key)
		out = append(out, d)
	}
	return out, rows.Err()
}

var statSQL = map[string]string{"Average": "AVG(value)", "Maximum": "MAX(value)", "Minimum": "MIN(value)", "Sum": "SUM(value)", "SampleCount": "COUNT(*)"}

// Aggregate buckets samples by period and applies the statistic in SQL.
func (r *cwRepo) Aggregate(ctx context.Context, q MetricQuery) ([]Bucket, error) {
	agg, ok := statSQL[q.Stat]
	if !ok || q.Period <= 0 {
		return nil, fmt.Errorf("invalid statistic or period")
	}
	where := `account_id=? AND namespace=? AND metric_name=? AND ts>=? AND ts<?`
	args := []any{q.Origin, q.Period, q.AccountID, q.Namespace, q.MetricName, q.Start, q.End}
	if q.DimLike != "" {
		where += ` AND dim_key LIKE ?`
		args = append(args, q.DimLike)
	} else {
		where += ` AND dim_key=?`
		args = append(args, q.DimKey)
	}
	rows, err := r.d.QueryContext(ctx, `SELECT (CAST(strftime('%s', substr(ts,1,19)) AS INTEGER) - ?) / ? AS b, `+agg+`, COUNT(*)
		FROM cw_metric_data WHERE `+where+` GROUP BY b ORDER BY b`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bucket{}
	for rows.Next() {
		var b int64
		var v float64
		var n int
		if err := rows.Scan(&b, &v, &n); err != nil {
			return nil, err
		}
		out = append(out, Bucket{Start: q.Origin + b*int64(q.Period), Value: v, Count: n})
	}
	return out, rows.Err()
}

func (r *cwRepo) SeriesAverages(ctx context.Context, account, namespace, metricName, since string) (map[string]float64, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT dim_key, AVG(value) FROM cw_metric_data WHERE account_id=? AND namespace=? AND metric_name=? AND ts>=? GROUP BY dim_key`,
		account, namespace, metricName, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var k string
		var v float64
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (r *cwRepo) PruneMetrics(ctx context.Context, before string) (int64, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM cw_metric_data WHERE ts < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const alarmCols = `alarm_id,account_id,alarm_name,namespace,metric_name,dim_key,statistic,period,evaluation_periods,threshold,comparison_operator,alarm_actions,state,state_reason,state_updated_at,created_at`

func scanAlarm(row interface{ Scan(...any) error }) (*Alarm, error) {
	var a Alarm
	var actions string
	if err := row.Scan(&a.AlarmID, &a.AccountID, &a.AlarmName, &a.Namespace, &a.MetricName, &a.DimKey, &a.Statistic, &a.Period, &a.EvaluationPeriods,
		&a.Threshold, &a.ComparisonOperator, &actions, &a.State, &a.StateReason, &a.StateUpdatedAt, &a.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	a.Dimensions = ParseDims(a.DimKey)
	_ = json.Unmarshal([]byte(actions), &a.AlarmActions)
	if a.AlarmActions == nil {
		a.AlarmActions = []string{}
	}
	return &a, nil
}

func (r *cwRepo) UpsertAlarm(ctx context.Context, a *Alarm) (bool, error) {
	actions, _ := json.Marshal(a.AlarmActions)
	existing, err := r.GetAlarm(ctx, a.AccountID, a.AlarmName)
	now := db.Now()
	if err == nil {
		a.AlarmID, a.CreatedAt, a.State, a.StateReason, a.StateUpdatedAt = existing.AlarmID, existing.CreatedAt, existing.State, existing.StateReason, existing.StateUpdatedAt
		_, err = r.d.ExecContext(ctx, `UPDATE cw_alarms SET namespace=?,metric_name=?,dim_key=?,statistic=?,period=?,evaluation_periods=?,threshold=?,comparison_operator=?,alarm_actions=? WHERE alarm_id=?`,
			a.Namespace, a.MetricName, a.DimKey, a.Statistic, a.Period, a.EvaluationPeriods, a.Threshold, a.ComparisonOperator, string(actions), a.AlarmID)
		return false, err
	}
	if err != ErrNotFound {
		return false, err
	}
	a.CreatedAt, a.StateUpdatedAt = now, now
	_, err = r.d.ExecContext(ctx, `INSERT INTO cw_alarms(`+alarmCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.AlarmID, a.AccountID, a.AlarmName, a.Namespace, a.MetricName, a.DimKey, a.Statistic, a.Period, a.EvaluationPeriods, a.Threshold,
		a.ComparisonOperator, string(actions), a.State, a.StateReason, now, now)
	return true, err
}

func (r *cwRepo) GetAlarm(ctx context.Context, account, name string) (*Alarm, error) {
	return scanAlarm(r.d.QueryRowContext(ctx, `SELECT `+alarmCols+` FROM cw_alarms WHERE account_id=? AND alarm_name=?`, account, name))
}

func (r *cwRepo) ListAlarms(ctx context.Context, account string) ([]Alarm, error) {
	q, args := `SELECT `+alarmCols+` FROM cw_alarms`, []any{}
	if account != "" {
		q += ` WHERE account_id=?`
		args = append(args, account)
	}
	rows, err := r.d.QueryContext(ctx, q+` ORDER BY alarm_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alarm{}
	for rows.Next() {
		a, err := scanAlarm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *cwRepo) DeleteAlarm(ctx context.Context, account, name string) (bool, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM cw_alarms WHERE account_id=? AND alarm_name=?`, account, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *cwRepo) SetAlarmState(ctx context.Context, alarmID, state, reason string) error {
	_, err := r.d.ExecContext(ctx, `UPDATE cw_alarms SET state=?, state_reason=?, state_updated_at=? WHERE alarm_id=?`, state, reason, db.Now(), alarmID)
	return err
}

func (r *cwRepo) InsertAlarmHistory(ctx context.Context, h *AlarmHistory) error {
	h.TS = db.Now()
	res, err := r.d.ExecContext(ctx, `INSERT INTO cw_alarm_history(alarm_id,ts,type,old_state,new_state,summary) VALUES(?,?,?,?,?,?)`,
		h.AlarmID, h.TS, h.Type, h.OldState, h.NewState, h.Summary)
	if err != nil {
		return err
	}
	h.ID, _ = res.LastInsertId()
	return nil
}

func (r *cwRepo) ListAlarmHistory(ctx context.Context, alarmID string, limit int) ([]AlarmHistory, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.d.QueryContext(ctx, `SELECT id,alarm_id,ts,type,old_state,new_state,summary FROM cw_alarm_history WHERE alarm_id=? ORDER BY id DESC LIMIT ?`, alarmID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlarmHistory{}
	for rows.Next() {
		var h AlarmHistory
		if err := rows.Scan(&h.ID, &h.AlarmID, &h.TS, &h.Type, &h.OldState, &h.NewState, &h.Summary); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *cwRepo) CreateLogGroup(ctx context.Context, g *LogGroup) error {
	g.CreatedAt = db.Now()
	res, err := r.d.ExecContext(ctx, `INSERT INTO cw_log_groups(account_id,name,retention_days,created_at) VALUES(?,?,?,?)`, g.AccountID, g.Name, g.RetentionDays, g.CreatedAt)
	if err != nil {
		return err
	}
	g.ID, _ = res.LastInsertId()
	return nil
}

func (r *cwRepo) EnsureLogGroup(ctx context.Context, account, name string) (int64, error) {
	if _, err := r.d.ExecContext(ctx, `INSERT INTO cw_log_groups(account_id,name,created_at) VALUES(?,?,?) ON CONFLICT(account_id,name) DO NOTHING`, account, name, db.Now()); err != nil {
		return 0, err
	}
	var id int64
	err := r.d.QueryRowContext(ctx, `SELECT log_group_id FROM cw_log_groups WHERE account_id=? AND name=?`, account, name).Scan(&id)
	return id, err
}

const lgSelect = `SELECT g.log_group_id,g.account_id,g.name,g.retention_days,g.created_at,
 COALESCE((SELECT SUM(LENGTH(message)) FROM cw_log_events e WHERE e.log_group_id=g.log_group_id),0) FROM cw_log_groups g`

func scanLG(row interface{ Scan(...any) error }) (*LogGroup, error) {
	var g LogGroup
	var ret sql.NullInt64
	if err := row.Scan(&g.ID, &g.AccountID, &g.Name, &ret, &g.CreatedAt, &g.StoredBytes); err != nil {
		return nil, notFound(err)
	}
	if ret.Valid {
		n := int(ret.Int64)
		g.RetentionDays = &n
	}
	return &g, nil
}

func (r *cwRepo) GetLogGroup(ctx context.Context, account, name string) (*LogGroup, error) {
	return scanLG(r.d.QueryRowContext(ctx, lgSelect+` WHERE g.account_id=? AND g.name=?`, account, name))
}

func (r *cwRepo) ListLogGroups(ctx context.Context, account string) ([]LogGroup, error) {
	rows, err := r.d.QueryContext(ctx, lgSelect+` WHERE g.account_id=? ORDER BY g.name`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogGroup{}
	for rows.Next() {
		g, err := scanLG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (r *cwRepo) DeleteLogGroup(ctx context.Context, account, name string) (bool, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM cw_log_groups WHERE account_id=? AND name=?`, account, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *cwRepo) ListLogStreams(ctx context.Context, groupID int64) ([]LogStream, error) {
	rows, err := r.d.QueryContext(ctx, `SELECT s.log_stream_id,s.name,s.created_at,s.last_event_ts,
		COALESCE((SELECT SUM(LENGTH(message)) FROM cw_log_events e WHERE e.log_stream_id=s.log_stream_id),0)
		FROM cw_log_streams s WHERE s.log_group_id=? ORDER BY COALESCE(s.last_event_ts, s.created_at) DESC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogStream{}
	for rows.Next() {
		var s LogStream
		var last sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.CreatedAt, &last, &s.StoredBytes); err != nil {
			return nil, err
		}
		if last.Valid {
			s.LastEventTS = &last.String
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *cwRepo) PutLogEvents(ctx context.Context, groupID int64, stream string, events []LogEventInput) error {
	if len(events) == 0 {
		return nil
	}
	return r.d.Tx(ctx, func(tx *sql.Tx) error {
		now := db.Now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO cw_log_streams(log_group_id,name,created_at) VALUES(?,?,?) ON CONFLICT(log_group_id,name) DO NOTHING`, groupID, stream, now); err != nil {
			return err
		}
		var sid int64
		if err := tx.QueryRowContext(ctx, `SELECT log_stream_id FROM cw_log_streams WHERE log_group_id=? AND name=?`, groupID, stream).Scan(&sid); err != nil {
			return err
		}
		st, err := tx.PrepareContext(ctx, `INSERT INTO cw_log_events(log_group_id,log_stream_id,ts,message,ingested_at) VALUES(?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		last := ""
		for _, e := range events {
			if _, err := st.ExecContext(ctx, groupID, sid, e.TS, e.Message, now); err != nil {
				return err
			}
			if e.TS > last {
				last = e.TS
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE cw_log_streams SET last_event_ts=MAX(COALESCE(last_event_ts,''), ?) WHERE log_stream_id=?`, last, sid)
		return err
	})
}

// QueryLogEvents returns the newest events matching the coarse filters (newest first).
func (r *cwRepo) QueryLogEvents(ctx context.Context, q LogQuery) ([]LogEvent, error) {
	where := `e.log_group_id=?`
	args := []any{q.GroupID}
	if q.Stream != "" {
		where += ` AND s.name=?`
		args = append(args, q.Stream)
	}
	if q.Start != "" {
		where += ` AND e.ts>=?`
		args = append(args, q.Start)
	}
	if q.End != "" {
		where += ` AND e.ts<=?`
		args = append(args, q.End)
	}
	if q.Limit <= 0 {
		q.Limit = 10000
	}
	rows, err := r.d.QueryContext(ctx, `SELECT e.id,e.ts,s.name,e.message FROM cw_log_events e JOIN cw_log_streams s ON s.log_stream_id=e.log_stream_id
		WHERE `+where+` ORDER BY e.ts DESC, e.id DESC LIMIT ?`, append(args, q.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEvent{}
	for rows.Next() {
		var e LogEvent
		if err := rows.Scan(&e.ID, &e.TS, &e.Stream, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneLogs deletes events older than each group's retention (now is db.TimeFormat).
func (r *cwRepo) PruneLogs(ctx context.Context, now string) (int64, error) {
	res, err := r.d.ExecContext(ctx, `DELETE FROM cw_log_events WHERE id IN (
		SELECT e.id FROM cw_log_events e JOIN cw_log_groups g ON g.log_group_id=e.log_group_id
		WHERE g.retention_days IS NOT NULL AND e.ts < strftime('%Y-%m-%dT%H:%M:%fZ', ?, '-' || g.retention_days || ' days'))`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
