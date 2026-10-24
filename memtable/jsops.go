package memtable

import (
	"math"
	"strings"
	"time"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
)

// Operasi bergaya JavaScript atas nilai primitif (hasil dt.Primitive):
// nil, string, float64, bool, time.Time.

func ms(t time.Time) float64 { return float64(t.UnixMilli()) }

// toNumber meniru Number(v); NaN bila tidak bisa diubah.
func toNumber(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		if n, ok := dt.ParseNumber(x); ok {
			return n
		}
	case time.Time:
		return ms(x)
	}
	return math.NaN()
}

// looseEqual meniru `a == b` JS untuk primitif. time.Time dibandingkan
// sebagai instan (dengan angka: milidetik; dengan string: string di-parse
// sebagai tanggal, tanpa zona → loc).
func looseEqual(a, b any, loc *time.Location) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if ta, ok := a.(time.Time); ok {
		return timeEqual(ta, b, loc)
	}
	if tb, ok := b.(time.Time); ok {
		return timeEqual(tb, a, loc)
	}
	switch x := a.(type) {
	case string:
		if y, ok := b.(string); ok {
			return x == y
		}
	case bool:
		if y, ok := b.(bool); ok {
			return x == y
		}
	case float64:
		if y, ok := b.(float64); ok {
			return x == y
		}
	}
	// Tipe berbeda: bandingkan sebagai angka (aturan ToNumber JS).
	x, y := toNumber(a), toNumber(b)
	return !math.IsNaN(x) && !math.IsNaN(y) && x == y
}

func timeEqual(t time.Time, other any, loc *time.Location) bool {
	switch o := other.(type) {
	case time.Time:
		return t.Equal(o)
	case float64:
		return ms(t) == o
	case string:
		if p, ok := parseDate(o, loc); ok {
			return t.Equal(p)
		}
	}
	return false
}

// compareOrdered meniru <, >, dst. JS: string vs string leksikografis, selain
// itu numerik (time.Time → milidetik; dengan string tanggal → instan). ok=false bila tidak bisa dibandingkan (nil atau NaN) — baris
// semacam itu tidak cocok, sama seperti NULL di SQL.
func compareOrdered(a, b any, loc *time.Location) (int, bool) {
	if a == nil || b == nil {
		return 0, false
	}
	if x, ok := a.(string); ok {
		if y, ok := b.(string); ok {
			return strings.Compare(x, y), true
		}
	}
	// Tambahan versi Go: waktu vs string tanggal dibandingkan sebagai instan.
	if ta, ok := a.(time.Time); ok {
		if s, ok := b.(string); ok {
			if tb, ok := parseDate(s, loc); ok {
				return ta.Compare(tb), true
			}
		}
	}
	if tb, ok := b.(time.Time); ok {
		if s, ok := a.(string); ok {
			if ta, ok := parseDate(s, loc); ok {
				return ta.Compare(tb), true
			}
		}
	}
	x, y := toNumber(a), toNumber(b)
	if math.IsNaN(x) || math.IsNaN(y) {
		return 0, false
	}
	switch {
	case x < y:
		return -1, true
	case x > y:
		return 1, true
	}
	return 0, true
}

// compareOp menerapkan operator filter select. Seperti versi JS, bila nilai
// filter berupa angka maka sel juga diubah ke angka ('1' cocok dengan 1).
// Sel nil hanya cocok dengan "!=".
func compareOp(cell any, op dt.Operator, value any, loc *time.Location) bool {
	if op == dt.OpContains {
		return strings.Contains(strings.ToLower(dt.FormatValue(cell)), strings.ToLower(dt.FormatValue(value)))
	}
	x := cell
	if _, isNum := value.(float64); isNum && cell != nil {
		x = toNumber(cell)
	}
	switch op {
	case dt.OpNe:
		return !looseEqual(x, value, loc)
	case dt.OpGt, dt.OpGte, dt.OpLt, dt.OpLte:
		c, ok := compareOrdered(x, value, loc)
		if !ok {
			return false
		}
		switch op {
		case dt.OpGt:
			return c > 0
		case dt.OpGte:
			return c >= 0
		case dt.OpLt:
			return c < 0
		default:
			return c <= 0
		}
	}
	return looseEqual(x, value, loc)
}

// sortValue meniru kunci sort versi JS: Date → milidetik, angka → angka,
// bool → 0/1, selain itu string huruf kecil (nil → "").
func sortValue(v any) any {
	switch x := v.(type) {
	case time.Time:
		return ms(x)
	case float64:
		return x
	case bool:
		if x {
			return 1.0
		}
		return 0.0
	}
	return strings.ToLower(dt.FormatValue(v))
}

// compareSort membandingkan dua kunci sort; pasangan yang tak terbandingkan
// (NaN) dianggap sama.
func compareSort(a, b any) int {
	if x, ok := a.(string); ok {
		if y, ok := b.(string); ok {
			return strings.Compare(x, y)
		}
	}
	c, _ := compareOrdered(a, b, time.UTC)
	return c
}

var dateLayoutsZoned = []string{
	time.RFC3339,
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04:05Z0700",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04Z07:00",
}

var dateLayoutsUTC = []string{"2006-01-02", "2006-01", "2006"}

var dateLayoutsLocal = []string{
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
}

// parseDate meniru `new Date(string)` untuk format ISO 8601: dengan zona →
// apa adanya; tanggal saja → UTC; tanggal+jam tanpa zona → loc.
func parseDate(s string, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range dateLayoutsZoned {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	for _, l := range dateLayoutsUTC {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	for _, l := range dateLayoutsLocal {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
