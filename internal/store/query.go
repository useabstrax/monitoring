package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"math"
	"time"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/api"
	"github.com/useabstrax/abstrax/plugins/monitoring/internal/moniterr"
)

// HistoryQuery is one page request. Cursor, when set, continues a previous page.
type HistoryQuery struct {
	From   time.Time
	To     time.Time
	Max    int
	Cursor string
}

type histCursor struct {
	V     int   `json:"v"`
	From  int64 `json:"f"`
	To    int64 `json:"t"`
	Max   int   `json:"m"`
	Width int64 `json:"w"`
	After int64 `json:"a"`
}

// History returns one coherent host and filesystem page.
func (db *DB) History(ctx context.Context, q HistoryQuery) (api.History, error) {
	if err := validateWindow(q); err != nil {
		return api.History{}, err
	}
	oldest, newest, err := db.bounds(ctx, "host_samples", "")
	if err != nil {
		return api.History{}, err
	}
	pageFrom, pageTo, width, err := db.resolve(ctx, "host_samples", "", q, oldest)
	if err != nil {
		return api.History{}, err
	}
	mounts, err := db.countMounts(ctx)
	if err != nil {
		return api.History{}, err
	}
	if err := checkPayload(q.Max, mounts); err != nil {
		return api.History{}, err
	}
	hostRows, err := db.loadHost(ctx, pageFrom, pageTo)
	if err != nil {
		return api.History{}, err
	}
	fsRows, err := db.loadFS(ctx, pageFrom, pageTo)
	if err != nil {
		return api.History{}, err
	}
	out := api.History{
		SchemaVersion:     api.SchemaVersion,
		From:              formatUnix(pageFrom),
		To:                formatUnix(pageTo),
		ResolutionSeconds: int(width),
		Tier:              db.Tiers().TierName(width),
		MaxPoints:         q.Max,
		AvailableFrom:     formatUnix(oldest),
		AvailableTo:       formatUnix(newest),
		Host:              foldHost(hostRows, width),
		Filesystems:       foldFS(fsRows, width),
		NextCursor:        nil,
	}
	if out.Host == nil {
		out.Host = []api.HostPoint{}
	}
	if out.Filesystems == nil {
		out.Filesystems = []api.FilesystemSeries{}
	}
	next, err := db.nextTS(ctx, "host_samples", "", pageTo, q.To.Unix())
	if err != nil {
		return api.History{}, err
	}
	if next > 0 {
		cur, err := encodeCursor(histCursor{V: 1, From: q.From.Unix(), To: q.To.Unix(), Max: q.Max, Width: width, After: pageTo})
		if err != nil {
			return api.History{}, err
		}
		out.NextCursor = &cur
	}
	return out, nil
}

// ProcessHistory returns one process page using the same tier rules as host history.
func (db *DB) ProcessHistory(ctx context.Context, instanceID string, q HistoryQuery) (api.ProcessHistory, error) {
	if instanceID == "" {
		return api.ProcessHistory{}, moniterr.E("invalid_argument", "instance id is required")
	}
	if err := validateWindow(q); err != nil {
		return api.ProcessHistory{}, err
	}
	ok, err := db.instanceExists(ctx, instanceID)
	if err != nil {
		return api.ProcessHistory{}, err
	}
	if !ok {
		return api.ProcessHistory{}, moniterr.E("not_found", "process instance not found")
	}
	oldest, newest, err := db.bounds(ctx, "process_samples", instanceID)
	if err != nil {
		return api.ProcessHistory{}, err
	}
	pageFrom, pageTo, width, err := db.resolve(ctx, "process_samples", instanceID, q, oldest)
	if err != nil {
		return api.ProcessHistory{}, err
	}
	if err := checkPayload(q.Max, 0); err != nil {
		return api.ProcessHistory{}, err
	}
	rows, err := db.loadProc(ctx, instanceID, pageFrom, pageTo)
	if err != nil {
		return api.ProcessHistory{}, err
	}
	out := api.ProcessHistory{
		SchemaVersion:     api.SchemaVersion,
		InstanceID:        instanceID,
		From:              formatUnix(pageFrom),
		To:                formatUnix(pageTo),
		ResolutionSeconds: int(width),
		Tier:              db.Tiers().TierName(width),
		MaxPoints:         q.Max,
		AvailableFrom:     formatUnix(oldest),
		AvailableTo:       formatUnix(newest),
		Points:            foldProc(rows, width),
	}
	if out.Points == nil {
		out.Points = []api.ProcessPoint{}
	}
	next, err := db.nextTS(ctx, "process_samples", instanceID, pageTo, q.To.Unix())
	if err != nil {
		return api.ProcessHistory{}, err
	}
	if next > 0 {
		cur, err := encodeCursor(histCursor{V: 1, From: q.From.Unix(), To: q.To.Unix(), Max: q.Max, Width: width, After: pageTo})
		if err != nil {
			return api.ProcessHistory{}, err
		}
		out.NextCursor = &cur
	}
	return out, nil
}

func validateWindow(q HistoryQuery) error {
	if q.From.IsZero() || q.To.IsZero() {
		return moniterr.E("invalid_argument", "from and to are required")
	}
	if !q.To.After(q.From) {
		return moniterr.E("invalid_argument", "to must be after from")
	}
	if q.To.Sub(q.From) > 10*365*24*time.Hour {
		return moniterr.E("invalid_argument", "history range exceeds 10 years")
	}
	if q.Max <= 0 {
		return moniterr.E("invalid_argument", "max_points must be positive")
	}
	if q.Max > api.HardMaxPoints {
		return moniterr.E("invalid_argument", "max_points exceeds the limit of 500")
	}
	return nil
}

func checkPayload(maxPoints, mounts int) error {
	if mounts < 1 {
		mounts = 1
	}
	estimate := maxPoints * (900 + mounts*450)
	if estimate > api.MaxPayloadBytes {
		return moniterr.E("invalid_argument", "max_points would make the history page too large; request fewer points")
	}
	return nil
}

func (db *DB) resolve(ctx context.Context, table, instance string, q HistoryQuery, oldest int64) (int64, int64, int64, error) {
	from := q.From.Unix()
	to := q.To.Unix()
	if q.Cursor != "" {
		c, err := decodeCursor(q.Cursor)
		if err != nil || c.V != 1 || c.Width <= 0 || c.From != from || c.To != to || c.Max != q.Max {
			return 0, 0, 0, moniterr.E("cursor_invalid", "history cursor is invalid or does not match this query")
		}
		if oldest == 0 || c.After < oldest {
			return 0, 0, 0, moniterr.E("cursor_expired", "history cursor expired because that data is no longer retained")
		}
		maxRes, err := db.maxRes(ctx, table, instance, c.After, to)
		if err != nil {
			return 0, 0, 0, err
		}
		if maxRes > c.Width {
			return 0, 0, 0, moniterr.E("cursor_expired", "history cursor expired because that resolution has been rolled up")
		}
		pageTo := c.After + c.Width*int64(q.Max)
		if pageTo > to {
			pageTo = to
		}
		return c.After, pageTo, c.Width, nil
	}
	width, err := db.chooseWidth(ctx, table, instance, from, to, q.Max)
	if err != nil {
		return 0, 0, 0, err
	}
	span := width * int64(q.Max)
	pageFrom, pageTo := from, from+span
	if pageTo > to {
		pageTo = to
	}
	next, err := db.nextTS(ctx, table, instance, from, to)
	if err != nil {
		return 0, 0, 0, err
	}
	if next > 0 && next >= pageTo {
		pageFrom = alignDown(next, width)
		pageTo = pageFrom + span
		if pageTo > to {
			pageTo = to
		}
	}
	return pageFrom, pageTo, width, nil
}

func (db *DB) chooseWidth(ctx context.Context, table, instance string, from, to int64, max int) (int64, error) {
	tiers := db.Tiers()
	maxRes, err := db.maxRes(ctx, table, instance, from, to)
	if err != nil {
		return 0, err
	}
	minWidth := tiers.RawInterval
	if minWidth <= 0 {
		minWidth = 5
	}
	if maxRes > minWidth {
		minWidth = maxRes
	}
	window := to - from
	if window < 1 {
		window = 1
	}
	widths := append([]int64(nil), tiers.Widths()...)
	if len(widths) == 0 || widths[len(widths)-1] < minWidth {
		widths = append(widths, minWidth)
	}
	chosen := widths[len(widths)-1]
	for _, w := range widths {
		if w <= 0 || w < minWidth {
			continue
		}
		points := (window + w - 1) / w
		chosen = w
		if points <= int64(max) {
			return w, nil
		}
	}
	return chosen, nil
}

func (db *DB) maxRes(ctx context.Context, table, instance string, from, to int64) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	q := `SELECT MAX(res) FROM ` + table + ` WHERE ts >= ? AND ts < ?`
	args := []any{from, to}
	if instance != "" {
		q = `SELECT MAX(res) FROM ` + table + ` WHERE instance_id = ? AND ts >= ? AND ts < ?`
		args = []any{instance, from, to}
	}
	var n sql.NullInt64
	if err := db.sql.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	if !n.Valid {
		return 0, nil
	}
	return n.Int64, nil
}

func (db *DB) bounds(ctx context.Context, table, instance string) (int64, int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	q := `SELECT MIN(ts), MAX(ts) FROM ` + table
	var args []any
	if instance != "" {
		q += ` WHERE instance_id = ?`
		args = append(args, instance)
	}
	var min, max sql.NullInt64
	if err := db.sql.QueryRowContext(ctx, q, args...).Scan(&min, &max); err != nil {
		return 0, 0, err
	}
	return min.Int64, max.Int64, nil
}

func (db *DB) nextTS(ctx context.Context, table, instance string, from, to int64) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	q := `SELECT MIN(ts) FROM ` + table + ` WHERE ts >= ? AND ts < ?`
	args := []any{from, to}
	if instance != "" {
		q = `SELECT MIN(ts) FROM ` + table + ` WHERE instance_id = ? AND ts >= ? AND ts < ?`
		args = []any{instance, from, to}
	}
	var n sql.NullInt64
	if err := db.sql.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n.Int64, nil
}

func (db *DB) countMounts(ctx context.Context) (int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(DISTINCT mount) FROM filesystem_samples`).Scan(&n)
	return n, err
}

func (db *DB) instanceExists(ctx context.Context, id string) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM process_instances WHERE instance_id = ?`, id).Scan(&n)
	return n > 0, err
}

func encodeCursor(c histCursor) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeCursor(s string) (histCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return histCursor{}, err
	}
	var c histCursor
	err = json.Unmarshal(b, &c)
	return c, err
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func avgF(sum float64, n int64) float64 {
	if n <= 0 {
		return 0
	}
	return sum / float64(n)
}

func divRound(sum, n int64) int64 {
	if n <= 0 {
		return 0
	}
	if sum < 0 {
		return sum / n
	}
	return (sum + n/2) / n
}

func rateOf(bytes, ms int64) float64 {
	if ms <= 0 {
		return 0
	}
	return float64(bytes) / (float64(ms) / 1000)
}
