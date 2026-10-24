package memtable

import (
	"math"
	"testing"
	"time"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
)

func TestLooseEqual(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		a, b any
		want bool
	}{
		{nil, nil, true},
		{nil, "", false},
		{"1", 1.0, true},
		{1.0, "1", true},
		{"01", 1.0, true},
		{"a", 1.0, false},
		{true, 1.0, true},
		{false, 0.0, true},
		{true, "1", true},
		{"true", true, false},
		{"x", "x", true},
		{now, now, true},
		{now, float64(now.UnixMilli()), true},
		{now, "2026-01-01T00:00:00Z", true},
		{now, "bukan", false},
		{math.NaN(), math.NaN(), false},
	}
	for i, tt := range tests {
		if got := looseEqual(tt.a, tt.b, time.UTC); got != tt.want {
			t.Errorf("#%d looseEqual(%#v,%#v) = %v", i, tt.a, tt.b, got)
		}
	}
}

func TestCompareOp(t *testing.T) {
	tests := []struct {
		cell  any
		op    dt.Operator
		value any
		want  bool
	}{
		{"10", dt.OpGt, 9.0, true},  // sel di-cast ke angka bila nilai filter angka
		{"10", dt.OpGt, "9", false}, // string vs string leksikografis (seperti JS)
		{nil, dt.OpEq, "x", false},
		{nil, dt.OpNe, "x", true},
		{nil, dt.OpGt, 0.0, false},
		{nil, dt.OpContains, "", true},
		{"Budi", dt.OpContains, "UD", true},
		{12.0, dt.OpContains, "2", true},
		{"abc", dt.OpLte, 5.0, false},
		{5.0, dt.OpLte, 5.0, true},
		{5.0, dt.OpLt, 5.0, false},
		{5.0, dt.OpGte, 5.0, true},
		{true, dt.OpEq, true, true},
	}
	for i, tt := range tests {
		if got := compareOp(tt.cell, tt.op, tt.value, time.UTC); got != tt.want {
			t.Errorf("#%d compareOp(%#v %s %#v) = %v", i, tt.cell, tt.op, tt.value, got)
		}
	}
}

func TestParseDate(t *testing.T) {
	wita := time.FixedZone("WITA", 8*3600)
	tests := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"2026-01-05T02:00:00Z", time.Date(2026, 1, 5, 2, 0, 0, 0, time.UTC), true},
		{"2026-01-05T02:00:00.5+07:00", time.Date(2026, 1, 5, 2, 0, 0, 5e8, time.FixedZone("", 7*3600)), true},
		{"2026-01-05T02:00Z", time.Date(2026, 1, 5, 2, 0, 0, 0, time.UTC), true},
		{"2026-01-05", time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), true},
		{"2026-01", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"2026", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"2026-01-05 08:00", time.Date(2026, 1, 5, 8, 0, 0, 0, wita), true},
		{"2026-01-05T08:00:30", time.Date(2026, 1, 5, 8, 0, 30, 0, wita), true},
		{"05/01/2026", time.Time{}, false},
	}
	for _, tt := range tests {
		got, ok := parseDate(tt.in, wita)
		if ok != tt.ok || (ok && !got.Equal(tt.want)) {
			t.Errorf("parseDate(%q) = %s, %v", tt.in, got, ok)
		}
	}
}
