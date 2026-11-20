package services

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
)

// CloudWatchService implements metrics (collection, query, custom data), alarms and logs.
type CloudWatchService struct {
	*Deps
	Repo      repositories.CloudWatchRepo
	Retention time.Duration

	// ExtraCollectors contribute samples on every collection (e.g. Auto Scaling group metrics).
	// They receive the snapshot samples keyed by instance id.
	ExtraCollectors []func(ctx context.Context, samples map[string]simulator.Sample) []repositories.MetricDatum
	// PolicyExists / PolicyExecutor resolve and run Auto Scaling policy ARNs used as alarm actions.
	PolicyExists   func(ctx context.Context, account, arn string) bool
	PolicyExecutor func(ctx context.Context, account, arn, cause string) error
	// Notifier publishes to an SNS topic ARN (optional).
	Notifier func(ctx context.Context, account, topicARN, subject, message string) error

	alarmMu sync.Mutex
}

func NewCloudWatchService(d *Deps, retention time.Duration) *CloudWatchService {
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	return &CloudWatchService{Deps: d, Repo: repositories.NewCloudWatchRepo(d.Store.DB), Retention: retention}
}

const (
	NSEC2 = "CloudSim/EC2"
	NSASG = "CloudSim/AutoScaling"
)

// ---- Collection

// Collect reads the engine metrics snapshot and stores per-instance samples in one transaction.
func (s *CloudWatchService) Collect(ctx context.Context) error {
	snap, err := s.Engine.MetricsSnapshot(ctx)
	if err != nil {
		return EngineErr(err)
	}
	ts := time.Now().UTC()
	if t, err := time.Parse(time.RFC3339Nano, snap.TS); err == nil {
		ts = t
	}
	tss := db.Fmt(ts)
	var data []repositories.MetricDatum
	byOwner := map[string][]any{}
	byID := map[string]simulator.Sample{}
	for _, sm := range snap.Samples {
		if sm.Owner == "" || sm.ResourceID == "" {
			continue
		}
		dk := repositories.CanonicalDims(map[string]string{"InstanceId": sm.ResourceID})
		for _, m := range []struct {
			name, unit string
			v          float64
		}{
			{"CPUUtilization", "Percent", sm.CPUPercent}, {"MemoryUtilization", "Percent", sm.MemoryPercent},
			{"NetworkIn", "Bytes", sm.NetworkInBytes}, {"NetworkOut", "Bytes", sm.NetworkOutBytes},
			{"DiskReadBytes", "Bytes", sm.DiskReadBytes}, {"DiskWriteBytes", "Bytes", sm.DiskWriteBytes},
		} {
			data = append(data, repositories.MetricDatum{AccountID: sm.Owner, Namespace: NSEC2, MetricName: m.name, DimKey: dk, TS: tss, Value: m.v, Unit: m.unit})
		}
		byOwner[sm.Owner] = append(byOwner[sm.Owner], sm)
		byID[sm.ResourceID] = sm
	}
	for _, f := range s.ExtraCollectors {
		for _, d := range f(ctx, byID) {
			if d.TS == "" {
				d.TS = tss
			}
			data = append(data, d)
		}
	}
	if err := s.Repo.InsertMetrics(ctx, data); err != nil {
		return err
	}
	for owner, samples := range byOwner {
		s.Pub.Publish(owner, map[string]any{"type": "metrics", "ts": tss, "samples": samples})
	}
	return nil
}

// Prune applies metric and log retention.
func (s *CloudWatchService) Prune(ctx context.Context) {
	if _, err := s.Repo.PruneMetrics(ctx, db.Fmt(time.Now().Add(-s.Retention))); err != nil {
		s.Log.Error("prune metrics", "err", err)
	}
	if _, err := s.Repo.PruneLogs(ctx, db.Now()); err != nil {
		s.Log.Error("prune logs", "err", err)
	}
}

// ---- Metrics query

func (s *CloudWatchService) ListMetrics(ctx context.Context, account, namespace, metricName string) ([]repositories.MetricDescriptor, error) {
	return s.Repo.ListMetrics(ctx, account, namespace, metricName)
}

var validStats = map[string]bool{"Average": true, "Maximum": true, "Minimum": true, "Sum": true, "SampleCount": true}

type MetricDataInput struct {
	Namespace  string
	MetricName string
	Dimensions map[string]string
	Start, End time.Time
	Period     int
	Stat       string
}

// ParseDimensionParams parses repeated "Name:Value" (or "Name=Value") query values.
func ParseDimensionParams(vals []string) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vals {
		for _, part := range strings.Split(v, ",") {
			if part == "" {
				continue
			}
			k, val, ok := strings.Cut(part, ":")
			if !ok {
				k, val, ok = strings.Cut(part, "=")
			}
			if !ok || k == "" {
				return nil, apierr.Validation("dimension must be Name:Value")
			}
			out[k] = val
		}
	}
	return out, nil
}

func (s *CloudWatchService) GetMetricData(ctx context.Context, account string, in MetricDataInput) (map[string]any, error) {
	if in.Namespace == "" || in.MetricName == "" {
		return nil, apierr.Validation("namespace and metric_name are required")
	}
	if in.Stat == "" {
		in.Stat = "Average"
	}
	if !validStats[in.Stat] {
		return nil, apierr.Validation("stat must be Average, Maximum, Minimum, Sum or SampleCount")
	}
	if in.Period == 0 {
		in.Period = 60
	}
	if in.Period < 1 || in.Period > 86400 {
		return nil, apierr.Validation("period must be between 1 and 86400 seconds")
	}
	if in.End.IsZero() {
		in.End = time.Now()
	}
	if in.Start.IsZero() {
		in.Start = in.End.Add(-time.Hour)
	}
	if !in.Start.Before(in.End) {
		return nil, apierr.Validation("start must be before end")
	}
	if in.End.Sub(in.Start)/time.Duration(in.Period)/time.Second > 1440 {
		return nil, apierr.Validation("too many datapoints requested (max 1440); increase period or shorten the range")
	}
	origin := in.Start.Unix() / int64(in.Period) * int64(in.Period)
	bs, err := s.Repo.Aggregate(ctx, repositories.MetricQuery{AccountID: account, Namespace: in.Namespace, MetricName: in.MetricName,
		DimKey: repositories.CanonicalDims(in.Dimensions), Start: db.Fmt(in.Start), End: db.Fmt(in.End), Origin: origin, Period: in.Period, Stat: in.Stat})
	if err != nil {
		return nil, err
	}
	dps := make([]map[string]any, 0, len(bs))
	for _, b := range bs {
		dps = append(dps, map[string]any{"ts": time.Unix(b.Start, 0).UTC().Format(time.RFC3339), "value": round(b.Value, 4), "sample_count": b.Count})
	}
	return map[string]any{"label": in.MetricName, "namespace": in.Namespace, "dimensions": in.Dimensions, "stat": in.Stat, "period": in.Period, "datapoints": dps}, nil
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

var (
	cwNamespaceRe = regexp.MustCompile(`^[A-Za-z0-9.\-_/#:]{1,255}$`)
	cwUnits       = map[string]bool{"Seconds": true, "Microseconds": true, "Milliseconds": true, "Bytes": true, "Kilobytes": true, "Megabytes": true,
		"Gigabytes": true, "Terabytes": true, "Bits": true, "Kilobits": true, "Megabits": true, "Gigabits": true, "Terabits": true, "Percent": true,
		"Count": true, "Bytes/Second": true, "Kilobytes/Second": true, "Megabytes/Second": true, "Bits/Second": true, "Count/Second": true, "None": true}
)

type MetricDatumInput struct {
	MetricName string            `json:"metric_name"`
	Dimensions map[string]string `json:"dimensions"`
	Value      *float64          `json:"value"`
	Unit       string            `json:"unit"`
	TS         string            `json:"ts"`
}

type PutMetricDataInput struct {
	Namespace  string             `json:"namespace"`
	MetricData []MetricDatumInput `json:"metric_data"`
}

func (s *CloudWatchService) PutMetricData(ctx context.Context, account string, in PutMetricDataInput) (int, error) {
	if !cwNamespaceRe.MatchString(in.Namespace) {
		return 0, apierr.Validation("namespace must be 1-255 characters (letters, digits, . - _ / # :)")
	}
	low := strings.ToLower(in.Namespace)
	if strings.HasPrefix(low, "cloudsim/") || strings.HasPrefix(low, "aws/") {
		return 0, apierr.Validation("namespaces starting with 'CloudSim/' or 'AWS/' are reserved for built-in metrics")
	}
	if len(in.MetricData) == 0 || len(in.MetricData) > 1000 {
		return 0, apierr.Validation("metric_data must contain 1-1000 items")
	}
	now := time.Now()
	var data []repositories.MetricDatum
	for i, m := range in.MetricData {
		if m.MetricName == "" || len(m.MetricName) > 255 {
			return 0, apierr.Validation("metric_data[%d].metric_name must be 1-255 characters", i)
		}
		if m.Value == nil || math.IsNaN(*m.Value) || math.IsInf(*m.Value, 0) {
			return 0, apierr.Validation("metric_data[%d].value must be a finite number", i)
		}
		if len(m.Dimensions) > 30 {
			return 0, apierr.Validation("metric_data[%d] has more than 30 dimensions", i)
		}
		for k, v := range m.Dimensions {
			if k == "" || len(k) > 255 || v == "" || len(v) > 1024 {
				return 0, apierr.Validation("metric_data[%d] dimension names must be 1-255 and values 1-1024 characters", i)
			}
		}
		if m.Unit == "" {
			m.Unit = "None"
		}
		if !cwUnits[m.Unit] {
			return 0, apierr.Validation("metric_data[%d].unit %q is not a valid CloudWatch unit", i, m.Unit)
		}
		ts := now
		if m.TS != "" {
			t, err := time.Parse(time.RFC3339Nano, m.TS)
			if err != nil {
				return 0, apierr.Validation("metric_data[%d].ts must be RFC 3339", i)
			}
			if t.Before(now.Add(-s.Retention)) || t.After(now.Add(2*time.Hour)) {
				return 0, apierr.Validation("metric_data[%d].ts is outside the accepted range (retention to +2h)", i)
			}
			ts = t
		}
		data = append(data, repositories.MetricDatum{AccountID: account, Namespace: in.Namespace, MetricName: m.MetricName,
			DimKey: repositories.CanonicalDims(m.Dimensions), TS: db.Fmt(ts), Value: *m.Value, Unit: m.Unit})
	}
	if err := s.Repo.InsertMetrics(ctx, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

// AverageCPU returns the average CPUUtilization per instance since `since`.
func (s *CloudWatchService) AverageCPU(ctx context.Context, account string, since time.Time) map[string]float64 {
	m, err := s.Repo.SeriesAverages(ctx, account, NSEC2, "CPUUtilization", db.Fmt(since))
	out := map[string]float64{}
	if err != nil {
		return out
	}
	for k, v := range m {
		d := repositories.ParseDims(k)
		if id := d["InstanceId"]; id != "" && len(d) == 1 {
			out[id] = v
		}
	}
	return out
}

// ---- Alarms

var (
	alarmNameRe = regexp.MustCompile(`^[^\x00-\x1f]{1,255}$`)
	cmpOps      = map[string]func(v, t float64) bool{
		"GreaterThanThreshold":          func(v, t float64) bool { return v > t },
		"GreaterThanOrEqualToThreshold": func(v, t float64) bool { return v >= t },
		"LessThanThreshold":             func(v, t float64) bool { return v < t },
		"LessThanOrEqualToThreshold":    func(v, t float64) bool { return v <= t },
	}
	cmpWords = map[string]string{"GreaterThanThreshold": "greater than", "GreaterThanOrEqualToThreshold": "greater than or equal to",
		"LessThanThreshold": "less than", "LessThanOrEqualToThreshold": "less than or equal to"}
)

type PutAlarmInput struct {
	AlarmName          string            `json:"alarm_name"`
	Namespace          string            `json:"namespace"`
	MetricName         string            `json:"metric_name"`
	Dimensions         map[string]string `json:"dimensions"`
	Statistic          string            `json:"statistic"`
	Period             int               `json:"period"`
	EvaluationPeriods  int               `json:"evaluation_periods"`
	Threshold          *float64          `json:"threshold"`
	ComparisonOperator string            `json:"comparison_operator"`
	AlarmActions       []string          `json:"alarm_actions"`
}

func (s *CloudWatchService) alarmARN(account, name string) string {
	return auth.ARN("cloudwatch", account, "alarm:"+name)
}

// PutAlarm creates or updates an alarm and evaluates it immediately.
func (s *CloudWatchService) PutAlarm(ctx context.Context, account string, in PutAlarmInput) (*repositories.Alarm, bool, error) {
	if !alarmNameRe.MatchString(in.AlarmName) {
		return nil, false, apierr.Validation("alarm_name must be 1-255 printable characters")
	}
	if !cwNamespaceRe.MatchString(in.Namespace) || in.MetricName == "" || len(in.MetricName) > 255 {
		return nil, false, apierr.Validation("namespace and metric_name are required")
	}
	if in.Statistic == "" {
		in.Statistic = "Average"
	}
	if !validStats[in.Statistic] {
		return nil, false, apierr.Validation("statistic must be Average, Maximum, Minimum, Sum or SampleCount")
	}
	if in.Period == 0 {
		in.Period = 60
	}
	if in.EvaluationPeriods == 0 {
		in.EvaluationPeriods = 1
	}
	if in.Period < 1 || in.Period > 86400 || in.EvaluationPeriods < 1 || in.Period*in.EvaluationPeriods > 86400 {
		return nil, false, apierr.Validation("period must be 1-86400 s, evaluation_periods ≥ 1, and period × evaluation_periods ≤ 86400")
	}
	if in.Threshold == nil || math.IsNaN(*in.Threshold) || math.IsInf(*in.Threshold, 0) {
		return nil, false, apierr.Validation("threshold is required")
	}
	if _, ok := cmpOps[in.ComparisonOperator]; !ok {
		return nil, false, apierr.Validation("comparison_operator must be GreaterThanThreshold, GreaterThanOrEqualToThreshold, LessThanThreshold or LessThanOrEqualToThreshold")
	}
	if len(in.AlarmActions) > 5 {
		return nil, false, apierr.Validation("at most 5 alarm actions")
	}
	for _, a := range in.AlarmActions {
		switch {
		case strings.HasPrefix(a, "arn:cloudsim:autoscaling:"):
			if !strings.Contains(a, ":"+account+":") || s.PolicyExists == nil || !s.PolicyExists(ctx, account, a) {
				return nil, false, apierr.Validation("alarm action %s does not reference an existing scaling policy in this account", a)
			}
		case strings.HasPrefix(a, "arn:cloudsim:sns:"):
			if !strings.Contains(a, ":"+account+":") {
				return nil, false, apierr.Validation("alarm action %s must reference a topic in this account", a)
			}
		default:
			return nil, false, apierr.Validation("unsupported alarm action %q (use a scaling policy or SNS topic ARN)", a)
		}
	}
	if in.AlarmActions == nil {
		in.AlarmActions = []string{}
	}
	a := &repositories.Alarm{AlarmID: ids.New("alarm"), AccountID: account, AlarmName: in.AlarmName, Namespace: in.Namespace, MetricName: in.MetricName,
		DimKey: repositories.CanonicalDims(in.Dimensions), Statistic: in.Statistic, Period: in.Period, EvaluationPeriods: in.EvaluationPeriods,
		Threshold: *in.Threshold, ComparisonOperator: in.ComparisonOperator, AlarmActions: in.AlarmActions, State: "INSUFFICIENT_DATA",
		StateReason: "Unchecked: Initial alarm creation"}
	created, err := s.Repo.UpsertAlarm(ctx, a)
	if err != nil {
		return nil, false, RepoErr(err, "alarm "+in.AlarmName)
	}
	verb := "updated"
	if created {
		verb = "created"
		_ = s.Repo.InsertAlarmHistory(ctx, &repositories.AlarmHistory{AlarmID: a.AlarmID, Type: "ConfigurationUpdate", NewState: "INSUFFICIENT_DATA", Summary: "Alarm created"})
	} else {
		_ = s.Repo.InsertAlarmHistory(ctx, &repositories.AlarmHistory{AlarmID: a.AlarmID, Type: "ConfigurationUpdate", OldState: a.State, NewState: a.State, Summary: "Alarm updated"})
	}
	s.RecordEvent(ctx, account, in.AlarmName, "alarm", verb, "Alarm "+in.AlarmName+" "+verb, nil)
	got, err := s.Repo.GetAlarm(ctx, account, in.AlarmName)
	if err != nil {
		return nil, false, err
	}
	s.evaluate(ctx, got)
	got, err = s.GetAlarm(ctx, account, in.AlarmName)
	return got, created, err
}

func (s *CloudWatchService) GetAlarm(ctx context.Context, account, name string) (*repositories.Alarm, error) {
	a, err := s.Repo.GetAlarm(ctx, account, name)
	if err != nil {
		return nil, RepoErr(err, "alarm "+name)
	}
	a.ARN = s.alarmARN(account, a.AlarmName)
	return a, nil
}

func (s *CloudWatchService) ListAlarms(ctx context.Context, account string) ([]repositories.Alarm, error) {
	l, err := s.Repo.ListAlarms(ctx, account)
	for i := range l {
		l[i].ARN = s.alarmARN(account, l[i].AlarmName)
	}
	return l, err
}

func (s *CloudWatchService) DeleteAlarm(ctx context.Context, account, name string) error {
	ok, err := s.Repo.DeleteAlarm(ctx, account, name)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.NotFound("alarm %s not found", name)
	}
	s.RecordEvent(ctx, account, name, "alarm", "deleted", "Alarm "+name+" deleted", nil)
	return nil
}

func (s *CloudWatchService) AlarmHistory(ctx context.Context, account, name string) ([]repositories.AlarmHistory, error) {
	a, err := s.GetAlarm(ctx, account, name)
	if err != nil {
		return nil, err
	}
	return s.Repo.ListAlarmHistory(ctx, a.AlarmID, 200)
}

// EvaluateAlarms evaluates every alarm (all accounts).
func (s *CloudWatchService) EvaluateAlarms(ctx context.Context) {
	l, err := s.Repo.ListAlarms(ctx, "")
	if err != nil {
		s.Log.Error("list alarms", "err", err)
		return
	}
	for i := range l {
		if ctx.Err() != nil {
			return
		}
		s.evaluate(ctx, &l[i])
	}
}

func (s *CloudWatchService) evaluate(ctx context.Context, a *repositories.Alarm) {
	s.alarmMu.Lock()
	defer s.alarmMu.Unlock()
	end := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	span := time.Duration(a.Period*a.EvaluationPeriods) * time.Second
	start := end.Add(-span)
	bs, err := s.Repo.Aggregate(ctx, repositories.MetricQuery{AccountID: a.AccountID, Namespace: a.Namespace, MetricName: a.MetricName, DimKey: a.DimKey,
		Start: db.Fmt(start), End: db.Fmt(end), Origin: start.Unix(), Period: a.Period, Stat: a.Statistic})
	if err != nil {
		s.Log.Error("evaluate alarm", "alarm", a.AlarmName, "err", err)
		return
	}
	cmp := cmpOps[a.ComparisonOperator]
	state, reason := "", ""
	var vals []string
	breaching := 0
	for _, b := range bs {
		vals = append(vals, fmt.Sprintf("%g (%s)", round(b.Value, 2), time.Unix(b.Start, 0).UTC().Format("15:04:05")))
		if cmp(b.Value, a.Threshold) {
			breaching++
		}
	}
	switch {
	case len(bs) < a.EvaluationPeriods:
		state = "INSUFFICIENT_DATA"
		reason = fmt.Sprintf("Insufficient Data: %d of %d datapoints were available in the last %s", len(bs), a.EvaluationPeriods, span)
	case breaching == len(bs):
		state = "ALARM"
		reason = fmt.Sprintf("Threshold Crossed: %d datapoint(s) [%s] were %s the threshold (%g).", len(bs), strings.Join(vals, ", "), cmpWords[a.ComparisonOperator], a.Threshold)
	default:
		state = "OK"
		reason = fmt.Sprintf("Threshold Crossed: %d of %d datapoint(s) [%s] were not %s the threshold (%g).", len(bs)-breaching, len(bs), strings.Join(vals, ", "), cmpWords[a.ComparisonOperator], a.Threshold)
	}
	if state == a.State {
		return
	}
	old := a.State
	if err := s.Repo.SetAlarmState(ctx, a.AlarmID, state, reason); err != nil {
		s.Log.Error("set alarm state", "err", err)
		return
	}
	_ = s.Repo.InsertAlarmHistory(ctx, &repositories.AlarmHistory{AlarmID: a.AlarmID, Type: "StateUpdate", OldState: old, NewState: state,
		Summary: fmt.Sprintf("Alarm updated from %s to %s: %s", old, state, reason)})
	s.RecordEvent(ctx, a.AccountID, a.AlarmName, "alarm", "alarm_state_changed", fmt.Sprintf("Alarm %s: %s → %s", a.AlarmName, old, state),
		map[string]string{"from": old, "to": state, "reason": reason})
	a.State = state
	if state == "ALARM" {
		s.runActions(ctx, a, reason)
	}
}

func (s *CloudWatchService) runActions(ctx context.Context, a *repositories.Alarm, reason string) {
	for _, act := range a.AlarmActions {
		var err error
		cause := fmt.Sprintf("CloudWatch alarm %s changed state to ALARM", a.AlarmName)
		switch {
		case strings.HasPrefix(act, "arn:cloudsim:autoscaling:"):
			if s.PolicyExecutor == nil {
				err = fmt.Errorf("auto scaling is not available")
			} else {
				err = s.PolicyExecutor(ctx, a.AccountID, act, cause)
			}
		case strings.HasPrefix(act, "arn:cloudsim:sns:"):
			if s.Notifier == nil {
				err = fmt.Errorf("SNS is not available")
			} else {
				err = s.Notifier(ctx, a.AccountID, act, "ALARM: \""+a.AlarmName+"\"", reason)
			}
		}
		h := &repositories.AlarmHistory{AlarmID: a.AlarmID, Type: "Action", NewState: "ALARM", Summary: "Successfully executed action " + act}
		if err != nil {
			h.Summary = "Failed to execute action " + act + ": " + err.Error()
		}
		_ = s.Repo.InsertAlarmHistory(ctx, h)
	}
}

// ---- Logs

var (
	logGroupRe     = regexp.MustCompile(`^[.\-_/#A-Za-z0-9]{1,512}$`)
	logStreamRe    = regexp.MustCompile(`^[^:*\x00-\x1f]{1,512}$`)
	retentionDays  = map[int]bool{1: true, 3: true, 5: true, 7: true, 14: true, 30: true, 60: true, 90: true, 120: true, 150: true, 180: true, 365: true, 400: true, 545: true, 731: true, 1096: true, 1827: true, 2192: true, 2557: true, 2922: true, 3288: true, 3653: true}
	maxLogMessage  = 256 * 1024
	maxLogEventsIn = 10000
)

func (s *CloudWatchService) logARN(account, name string) string {
	return auth.ARN("logs", account, "log-group:"+name)
}

func (s *CloudWatchService) CreateLogGroup(ctx context.Context, account, name string, retention *int) (*repositories.LogGroup, error) {
	if !logGroupRe.MatchString(name) {
		return nil, apierr.Validation("log group name must be 1-512 characters of letters, digits, '.', '-', '_', '/', '#'")
	}
	if retention != nil && !retentionDays[*retention] {
		return nil, apierr.Validation("retention_days must be one of 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653")
	}
	g := &repositories.LogGroup{AccountID: account, Name: name, RetentionDays: retention}
	if err := s.Repo.CreateLogGroup(ctx, g); err != nil {
		if e := RepoErr(err, "log group "+name); e != err {
			return nil, apierr.New(409, "ResourceAlreadyExists", "log group %s already exists", name)
		}
		return nil, err
	}
	s.RecordEvent(ctx, account, name, "log_group", "created", "Log group "+name+" created", nil)
	return s.GetLogGroup(ctx, account, name)
}

func (s *CloudWatchService) GetLogGroup(ctx context.Context, account, name string) (*repositories.LogGroup, error) {
	g, err := s.Repo.GetLogGroup(ctx, account, name)
	if err != nil {
		return nil, RepoErr(err, "log group "+name)
	}
	g.ARN = s.logARN(account, name)
	return g, nil
}

func (s *CloudWatchService) ListLogGroups(ctx context.Context, account string) ([]repositories.LogGroup, error) {
	l, err := s.Repo.ListLogGroups(ctx, account)
	for i := range l {
		l[i].ARN = s.logARN(account, l[i].Name)
	}
	return l, err
}

func (s *CloudWatchService) DeleteLogGroup(ctx context.Context, account, name string) error {
	ok, err := s.Repo.DeleteLogGroup(ctx, account, name)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.NotFound("log group %s not found", name)
	}
	s.RecordEvent(ctx, account, name, "log_group", "deleted", "Log group "+name+" deleted", nil)
	return nil
}

func (s *CloudWatchService) ListLogStreams(ctx context.Context, account, group string) ([]repositories.LogStream, error) {
	g, err := s.GetLogGroup(ctx, account, group)
	if err != nil {
		return nil, err
	}
	return s.Repo.ListLogStreams(ctx, g.ID)
}

type LogEventIn struct {
	TS      any    `json:"ts"`
	Message string `json:"message"`
}

func parseLogTS(v any) (time.Time, error) {
	switch t := v.(type) {
	case nil:
		return time.Now(), nil
	case float64:
		return time.UnixMilli(int64(t)), nil
	case string:
		if t == "" {
			return time.Now(), nil
		}
		if ms, err := strconv.ParseInt(t, 10, 64); err == nil {
			return time.UnixMilli(ms), nil
		}
		return time.Parse(time.RFC3339Nano, t)
	}
	return time.Time{}, fmt.Errorf("bad ts")
}

func (s *CloudWatchService) PutLogEvents(ctx context.Context, account, group, stream string, events []LogEventIn) (int, error) {
	g, err := s.GetLogGroup(ctx, account, group)
	if err != nil {
		return 0, err
	}
	if !logStreamRe.MatchString(stream) {
		return 0, apierr.Validation("log stream name must be 1-512 characters and may not contain ':' or '*'")
	}
	if len(events) == 0 || len(events) > maxLogEventsIn {
		return 0, apierr.Validation("events must contain 1-%d entries", maxLogEventsIn)
	}
	in := make([]repositories.LogEventInput, 0, len(events))
	for i, e := range events {
		if e.Message == "" || len(e.Message) > maxLogMessage {
			return 0, apierr.Validation("events[%d].message must be 1-%d bytes", i, maxLogMessage)
		}
		t, err := parseLogTS(e.TS)
		if err != nil {
			return 0, apierr.Validation("events[%d].ts must be RFC 3339 or epoch milliseconds", i)
		}
		in = append(in, repositories.LogEventInput{TS: db.Fmt(t), Message: e.Message})
	}
	sort.SliceStable(in, func(i, j int) bool { return in[i].TS < in[j].TS })
	if err := s.Repo.PutLogEvents(ctx, g.ID, stream, in); err != nil {
		return 0, err
	}
	return len(in), nil
}

// Append implements LogSink (platform logs such as EC2 console output).
func (s *CloudWatchService) Append(ctx context.Context, account, group, stream string, lines []string) {
	if account == "" || len(lines) == 0 {
		return
	}
	gid, err := s.Repo.EnsureLogGroup(ctx, account, group)
	if err != nil {
		s.Log.Warn("log sink: ensure group", "group", group, "err", err)
		return
	}
	now := db.Now()
	in := make([]repositories.LogEventInput, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			in = append(in, repositories.LogEventInput{TS: now, Message: l})
		}
	}
	if err := s.Repo.PutLogEvents(ctx, gid, stream, in); err != nil {
		s.Log.Warn("log sink: put events", "group", group, "err", err)
	}
}

// LogFilter is a parsed filter pattern: plain terms (all must match), "quoted phrases",
// -term exclusions and ?term any-of alternatives. Matching is case-sensitive like CloudWatch Logs.
type LogFilter struct {
	All, None, Any []string
}

func ParseFilterPattern(p string) (LogFilter, error) {
	var f LogFilter
	rs := []rune(strings.TrimSpace(p))
	for i := 0; i < len(rs); {
		if rs[i] == ' ' || rs[i] == '\t' {
			i++
			continue
		}
		mod := rune(0)
		if rs[i] == '-' || rs[i] == '?' {
			mod = rs[i]
			i++
		}
		var term string
		if i < len(rs) && rs[i] == '"' {
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				j++
			}
			if j >= len(rs) {
				return f, apierr.Validation("filter_pattern has an unterminated quoted phrase")
			}
			term = string(rs[i+1 : j])
			i = j + 1
		} else {
			j := i
			for j < len(rs) && rs[j] != ' ' && rs[j] != '\t' {
				j++
			}
			term = string(rs[i:j])
			i = j
		}
		if term == "" {
			if mod != 0 {
				return f, apierr.Validation("filter_pattern operator %q must be followed by a term", string(mod))
			}
			continue
		}
		switch mod {
		case '-':
			f.None = append(f.None, term)
		case '?':
			f.Any = append(f.Any, term)
		default:
			f.All = append(f.All, term)
		}
	}
	return f, nil
}

func (f LogFilter) Empty() bool { return len(f.All)+len(f.None)+len(f.Any) == 0 }

func (f LogFilter) Match(msg string) bool {
	for _, t := range f.All {
		if !strings.Contains(msg, t) {
			return false
		}
	}
	for _, t := range f.None {
		if strings.Contains(msg, t) {
			return false
		}
	}
	if len(f.Any) == 0 {
		return true
	}
	for _, t := range f.Any {
		if strings.Contains(msg, t) {
			return true
		}
	}
	return false
}

type FilterLogInput struct {
	Pattern    string
	Stream     string
	Start, End string
	Limit      int
}

func parseTimeParam(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
		return db.Fmt(time.UnixMilli(ms)), nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return "", apierr.Validation("time %q must be RFC 3339 or epoch milliseconds", v)
	}
	return db.Fmt(t), nil
}

func (s *CloudWatchService) FilterLogEvents(ctx context.Context, account, group string, in FilterLogInput) (map[string]any, error) {
	g, err := s.GetLogGroup(ctx, account, group)
	if err != nil {
		return nil, err
	}
	f, err := ParseFilterPattern(in.Pattern)
	if err != nil {
		return nil, err
	}
	if in.Limit == 0 {
		in.Limit = 100
	}
	if in.Limit < 1 || in.Limit > 10000 {
		return nil, apierr.Validation("limit must be between 1 and 10000")
	}
	start, err := parseTimeParam(in.Start)
	if err != nil {
		return nil, err
	}
	end, err := parseTimeParam(in.End)
	if err != nil {
		return nil, err
	}
	scan := in.Limit
	if !f.Empty() {
		scan = 10000
	}
	evs, err := s.Repo.QueryLogEvents(ctx, repositories.LogQuery{GroupID: g.ID, Stream: in.Stream, Start: start, End: end, Limit: scan})
	if err != nil {
		return nil, err
	}
	out := make([]repositories.LogEvent, 0, min(len(evs), in.Limit))
	for _, e := range evs {
		if f.Match(e.Message) {
			out = append(out, e)
			if len(out) == in.Limit {
				break
			}
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return map[string]any{"log_group": group, "events": out, "searched": len(evs)}, nil
}

// ---- Dashboard

func (s *CloudWatchService) Dashboard(ctx context.Context, account string, out map[string]any) {
	alarms, _ := s.Repo.ListAlarms(ctx, account)
	counts := map[string]int{"ok": 0, "alarm": 0, "insufficient_data": 0}
	for _, a := range alarms {
		counts[strings.ToLower(a.State)]++
	}
	out["alarms"] = counts
	now := time.Now().UTC()
	instLike := `{"InstanceId":%`
	agg := func(metric, stat string, from time.Time, period int, origin int64) []repositories.Bucket {
		b, err := s.Repo.Aggregate(ctx, repositories.MetricQuery{AccountID: account, Namespace: NSEC2, MetricName: metric, DimLike: instLike,
			Start: db.Fmt(from), End: db.Fmt(now.Add(time.Second)), Origin: origin, Period: period, Stat: stat})
		if err != nil {
			return nil
		}
		return b
	}
	from5 := now.Add(-5 * time.Minute)
	total := func(bs []repositories.Bucket, avg bool) float64 {
		sum, n := 0.0, 0
		for _, b := range bs {
			if avg {
				sum += b.Value * float64(b.Count)
				n += b.Count
			} else {
				sum += b.Value
			}
		}
		if avg {
			if n == 0 {
				return 0
			}
			return sum / float64(n)
		}
		return sum
	}
	out["avg_cpu_percent"] = round(total(agg("CPUUtilization", "Average", from5, 300, from5.Unix()), true), 1)
	out["network_in_bytes_5m"] = int64(total(agg("NetworkIn", "Sum", from5, 300, from5.Unix()), false))
	out["network_out_bytes_5m"] = int64(total(agg("NetworkOut", "Sum", from5, 300, from5.Unix()), false))
	fromH := now.Add(-time.Hour).Truncate(time.Minute)
	series := []map[string]any{}
	for _, b := range agg("CPUUtilization", "Average", fromH, 60, fromH.Unix()) {
		series = append(series, map[string]any{"ts": time.Unix(b.Start, 0).UTC().Format(time.RFC3339), "value": round(b.Value, 2)})
	}
	out["cpu_series"] = series
}
