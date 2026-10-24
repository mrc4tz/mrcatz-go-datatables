package datatable

import (
	"errors"
	"testing"
	"time"
)

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestPeriodOf(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		format     DateFormat
		off        UTCOffset
		start, end string
	}{
		{"bulan dengan offset WITA", "2026-10", FormatMonth, "+08:00", "2026-09-30T16:00:00Z", "2026-10-31T16:00:00Z"},
		{"hari UTC", "2026-10-19", FormatDate, "Z", "2026-10-19T00:00:00Z", "2026-10-20T00:00:00Z"},
		{"hari dengan sisa jam diabaikan", "2026-10-19T08:30", FormatDate, "+07:00", "2026-10-18T17:00:00Z", "2026-10-19T17:00:00Z"},
		{"tahun", "2026", FormatYear, "+0800", "2025-12-31T16:00:00Z", "2026-12-31T16:00:00Z"},
		{"datetime satu menit", "2026-10-19T08:30", FormatDatetime, "Z", "2026-10-19T08:30:00Z", "2026-10-19T08:31:00Z"},
		{"datetime dengan spasi", "2026-10-19 23:59", FormatDatetime, "-05:00", "2026-10-20T04:59:00Z", "2026-10-20T05:00:00Z"},
		{"desember → januari", "2026-12", FormatMonth, "Z", "2026-12-01T00:00:00Z", "2027-01-01T00:00:00Z"},
		{"tanggal di luar kalender dinormalisasi", "2026-02-31", FormatDate, "Z", "2026-03-03T00:00:00Z", "2026-03-04T00:00:00Z"},
		{"spasi di tepi di-trim", " 2026-10-19 ", FormatDate, "Z", "2026-10-19T00:00:00Z", "2026-10-20T00:00:00Z"},
		{"time = satu detik", "08:30:15", FormatTime, "", "1970-01-01T08:30:15Z", "1970-01-01T08:30:16Z"},
		{"time tanpa detik = satu menit", "08:30", FormatTime, "", "1970-01-01T08:30:00Z", "1970-01-01T08:31:00Z"},
		{"timeHm selalu satu menit", "08:30:59", FormatTimeHm, "", "1970-01-01T08:30:00Z", "1970-01-01T08:31:00Z"},
		{"offset tidak berpengaruh pada jam murni", "08:30", FormatTimeHm, "+08:00", "1970-01-01T08:30:00Z", "1970-01-01T08:31:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := PeriodOf(tt.value, tt.format, tt.off)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Start.Equal(mustTime(tt.start)) || !p.End.Equal(mustTime(tt.end)) {
				t.Fatalf("got [%s, %s)", p.Start, p.End)
			}
		})
	}
}

func TestPeriodOfLocal(t *testing.T) {
	p, err := PeriodOf("2026-10-19", FormatDate, "")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)
	if !p.Start.Equal(want) {
		t.Fatalf("lokal: %s", p.Start)
	}
}

func TestPeriodOfErrors(t *testing.T) {
	tests := []struct {
		value  string
		format DateFormat
		off    UTCOffset
	}{
		{"2026/10/01", FormatDate, ""},
		{"kemarin", FormatDate, "Z"},
		{"2026-13", FormatMonth, "Z"},
		{"2026-00", FormatMonth, "Z"},
		{"26", FormatYear, "Z"},
		{"2026-10-19", FormatDatetime, "Z"},
		{"24:00", FormatTimeHm, ""},
		{"2026-10-19", FormatDate, "WITA"},
		{"2026-10-19", DateFormat("week"), "Z"},
	}
	for _, tt := range tests {
		_, err := PeriodOf(tt.value, tt.format, tt.off)
		var qe *QueryError
		if !errors.As(err, &qe) || !errors.Is(err, ErrInvalidDate) || qe.StatusCode() != 400 {
			t.Errorf("PeriodOf(%q,%q,%q): err=%v", tt.value, tt.format, tt.off, err)
		}
	}
}

func TestParseTimeOfDay(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"00:00", 0, true},
		{"14:30", 52200, true},
		{"14:30:15", 52215, true},
		{" 23:59:59 ", 86399, true},
		{"24:00", 0, false},
		{"9:30", 0, false},
		{"12:60", 0, false},
		{"12:00:60", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		got, err := ParseTimeOfDay(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("ParseTimeOfDay(%q) = %d, %v", tt.in, got, err)
		}
		if err != nil && !errors.Is(err, ErrInvalidDate) {
			t.Errorf("harus ErrInvalidDate: %v", err)
		}
	}
	if !IsTimeFormat(FormatTimeHm) || !IsTimeFormat(FormatTime) || IsTimeFormat(FormatDate) {
		t.Error("IsTimeFormat")
	}
}

func TestTimeOfDay(t *testing.T) {
	tests := []struct {
		in   string
		off  UTCOffset
		want string
	}{
		{"2026-10-19T01:30:00Z", "+08:00", "1970-01-01T09:30:00Z"},
		{"2026-10-19T23:30:00Z", "+08:00", "1970-01-01T07:30:00Z"},
		{"2026-10-19T23:30:00.250Z", "Z", "1970-01-01T23:30:00.25Z"},
	}
	for _, tt := range tests {
		got, err := TimeOfDay(mustTime(tt.in), tt.off)
		if err != nil || !got.Equal(mustTime(tt.want)) {
			t.Errorf("TimeOfDay(%s,%s) = %s, %v", tt.in, tt.off, got, err)
		}
	}
	if _, err := TimeOfDay(time.Now(), "x"); err == nil {
		t.Error("offset tidak valid harus gagal")
	}
}

func ptrEq(a *time.Time, b string) bool {
	if b == "" {
		return a == nil
	}
	return a != nil && a.Equal(mustTime(b))
}

func TestCompareBounds(t *testing.T) {
	p, _ := PeriodOf("2026-10-19", FormatDate, "Z")
	tests := []struct {
		op      Operator
		gte, lt string
		not     bool
	}{
		{OpEq, "2026-10-19T00:00:00Z", "2026-10-20T00:00:00Z", false},
		{OpNe, "", "", true},
		{OpGt, "2026-10-20T00:00:00Z", "", false},
		{OpGte, "2026-10-19T00:00:00Z", "", false},
		{OpLt, "", "2026-10-19T00:00:00Z", false},
		{OpLte, "", "2026-10-20T00:00:00Z", false},
		{OpContains, "2026-10-19T00:00:00Z", "2026-10-20T00:00:00Z", false},
	}
	for _, tt := range tests {
		b := CompareBounds(p, tt.op)
		if !ptrEq(b.GTE, tt.gte) || !ptrEq(b.LT, tt.lt) || (b.Not != nil) != tt.not {
			t.Errorf("op %s: %+v", tt.op, b)
		}
		if tt.not && *b.Not != p {
			t.Errorf("not period: %+v", b.Not)
		}
	}
	hm, _ := PeriodOf("12:00", FormatTimeHm, "")
	if b := CompareBounds(hm, OpGte); !ptrEq(b.GTE, "1970-01-01T12:00:00Z") || b.LT != nil {
		t.Errorf("timeHm >=: %+v", b)
	}
}

func TestBoundsContains(t *testing.T) {
	p, _ := PeriodOf("2026-10-19", FormatDate, "Z")
	in := mustTime("2026-10-19T12:00:00Z")
	start := mustTime("2026-10-19T00:00:00Z")
	end := mustTime("2026-10-20T00:00:00Z")
	eq, ne := CompareBounds(p, OpEq), CompareBounds(p, OpNe)
	cases := []struct {
		b    Bounds
		t    time.Time
		want bool
	}{
		{eq, in, true}, {eq, start, true}, {eq, end, false},
		{ne, in, false}, {ne, end, true}, {ne, start.Add(-time.Nanosecond), true},
		{Bounds{}, in, true},
	}
	for i, c := range cases {
		if got := c.b.Contains(c.t); got != c.want {
			t.Errorf("#%d Contains(%s) = %v", i, c.t, got)
		}
	}
	if !(Bounds{}).IsZero() || eq.IsZero() {
		t.Error("IsZero")
	}
}

func TestRangeBounds(t *testing.T) {
	b, err := RangeBounds(Str("08:00"), Str("17:00"), FormatTimeHm, "")
	if err != nil || !ptrEq(b.GTE, "1970-01-01T08:00:00Z") || !ptrEq(b.LT, "1970-01-01T17:01:00Z") {
		t.Fatalf("timeHm: %+v %v", b, err)
	}
	b, err = RangeBounds(Str("2026-01-01"), nil, FormatDate, "+08:00")
	if err != nil || !ptrEq(b.GTE, "2025-12-31T16:00:00Z") || b.LT != nil {
		t.Fatalf("terbuka: %+v %v", b, err)
	}
	b, err = RangeBounds(Str(""), Str("2026-01"), FormatMonth, "Z")
	if err != nil || b.GTE != nil || !ptrEq(b.LT, "2026-02-01T00:00:00Z") {
		t.Fatalf("to saja: %+v %v", b, err)
	}
	if _, err := RangeBounds(nil, Str("x"), FormatDate, "Z"); !errors.Is(err, ErrInvalidDate) {
		t.Fatalf("to tidak valid: %v", err)
	}
	if _, err := RangeBounds(Str("x"), nil, FormatDate, "Z"); !errors.Is(err, ErrInvalidDate) {
		t.Fatalf("from tidak valid: %v", err)
	}
}

func TestFilterBounds(t *testing.T) {
	d := Date("d", "D", Field("d"), Format(FormatMonth), OpFunc(func(c FilterContext) Operator {
		if c.String("arah") == "sesudah" {
			return OpGte
		}
		return OpLte
	}))
	b, err := FilterBounds(d, Resolved{Scalar: "2026-02"}, FilterContext{Values: map[string]FilterValue{"arah": StringValue("sesudah")}}, "Z")
	if err != nil || !ptrEq(b.GTE, "2026-02-01T00:00:00Z") || b.LT != nil {
		t.Fatalf("date: %+v %v", b, err)
	}
	r := DateRange("r", "R", Field("r"))
	b, err = FilterBounds(r, Resolved{From: Str("2026-01-01"), To: Str("2026-01-31")}, FilterContext{}, "Z")
	if err != nil || !ptrEq(b.GTE, "2026-01-01T00:00:00Z") || !ptrEq(b.LT, "2026-02-01T00:00:00Z") {
		t.Fatalf("range: %+v %v", b, err)
	}
	if b, err := FilterBounds(Select("s", "S", Field("s")), Resolved{Scalar: "x"}, FilterContext{}, "Z"); err != nil || !b.IsZero() {
		t.Fatalf("select: %+v %v", b, err)
	}
}

func TestUTCOffsetLocation(t *testing.T) {
	tests := []struct {
		off  UTCOffset
		secs int
		ok   bool
	}{
		{"Z", 0, true}, {"+00:00", 0, true}, {"+08:00", 8 * 3600, true}, {"-0530", -(5*3600 + 30*60), true},
		{"08:00", 0, false}, {"+8", 0, false}, {"UTC", 0, false},
	}
	for _, tt := range tests {
		loc, err := tt.off.Location()
		if (err == nil) != tt.ok {
			t.Errorf("%q: %v", tt.off, err)
			continue
		}
		if err == nil {
			if _, secs := time.Date(2026, 1, 1, 0, 0, 0, 0, loc).Zone(); secs != tt.secs {
				t.Errorf("%q: offset %d", tt.off, secs)
			}
		}
	}
	if loc, _ := UTCOffset("").Location(); loc != time.Local {
		t.Error("\"\" harus time.Local")
	}
}
