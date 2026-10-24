package datatable

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFilterDefaults(t *testing.T) {
	s := Select("s", "S", Field("status"))
	if s.Operator != OpEq || s.Type != FilterSelect {
		t.Errorf("select default: %+v", s)
	}
	m := Multi("m", "M", Field("kat"))
	if m.SearchThreshold == nil || *m.SearchThreshold != 5 || m.AllowExclude {
		t.Errorf("multi default: %+v", m)
	}
	if m2 := Multi("m", "M", Field("kat"), NoSearch()); m2.SearchThreshold != nil {
		t.Error("NoSearch harus nil")
	}
	if m3 := Multi("m", "M", Field("kat"), SearchThreshold(10), AllowExclude()); *m3.SearchThreshold != 10 || !m3.AllowExclude {
		t.Error("SearchThreshold/AllowExclude")
	}
	d := Date("d", "D", Field("tgl"))
	if d.Format != FormatDate || d.Operator != OpEq {
		t.Errorf("date default: %+v", d)
	}
	r := DateRange("r", "R", Field("tgl"))
	if r.Format != FormatDate {
		t.Errorf("dateRange default: %+v", r)
	}
	// opsi menimpa default
	if d2 := Date("d", "D", Field("tgl"), Format(FormatMonth), Op(OpGte)); d2.Format != FormatMonth || d2.Operator != OpGte {
		t.Errorf("override: %+v", d2)
	}
}

func TestFilterDefinitionErrors(t *testing.T) {
	tests := []struct {
		name    string
		typ     FilterType
		opts    []FilterOption
		wantMsg string
	}{
		{"tanpa target ditolak", FilterSelect, []FilterOption{Options()}, "field, where, atau match"},
		{"tipe tidak dikenal", FilterType("radio"), []FilterOption{Field("x")}, "tidak dikenal"},
		{"operator select tidak valid", FilterSelect, []FilterOption{Field("x"), Op("like")}, "operator"},
		{"contains tidak boleh untuk date", FilterDate, []FilterOption{Field("x"), Op(OpContains)}, "operator"},
		{"cast tidak valid", FilterMulti, []FilterOption{Field("x"), CastAs("int")}, "cast"},
		{"format tidak valid", FilterDateRange, []FilterOption{Field("x"), Format("week")}, "format"},
		{"WithWhere nil-func tetap tanpa target", FilterSelect, []FilterOption{WithWhere((func())(nil))}, "field, where, atau match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewFilter(tt.typ, "x", "X", tt.opts...)
			var fe *FilterDefinitionError
			if !errors.As(err, &fe) || !strings.Contains(fe.Msg, tt.wantMsg) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	// target lewat where atau match sudah cukup
	if _, err := NewFilter(FilterSelect, "x", "X", WithWhere(func() {})); err != nil {
		t.Error(err)
	}
	if _, err := NewFilter(FilterSelect, "x", "X", MatchFunc(func(any, Resolved) bool { return true })); err != nil {
		t.Error(err)
	}
	defer func() {
		r := recover()
		if _, ok := r.(*FilterDefinitionError); !ok {
			t.Fatalf("Select harus panic *FilterDefinitionError, dapat %v", r)
		}
	}()
	Select("x", "X", Options())
}

func TestEffectiveCast(t *testing.T) {
	type myInt int
	tests := []struct {
		name string
		f    Filter
		want Cast
	}{
		{"ditebak angka", Select("a", "A", Field("a"), Options(Opt(1, "x"))), CastNumber},
		{"ditebak angka tipe turunan", Select("a", "A", Field("a"), Options(Opt(myInt(1), "x"))), CastNumber},
		{"ditebak boolean", Multi("a", "A", Field("a"), Options(Opt(true, "x"))), CastBoolean},
		{"ditebak string", Select("a", "A", Field("a"), Options(Opt("x", "x"))), CastString},
		{"tanpa opsi", Select("a", "A", Field("a")), ""},
		{"opsi dinamis tidak ditebak", Select("a", "A", Field("a"), OptionsFrom(func(FilterContext) ([]Option, error) { return nil, nil })), ""},
		{"eksplisit menang", Select("a", "A", Field("a"), Options(Opt(1, "x")), CastAs(CastString)), CastString},
		{"date tidak di-cast", Date("a", "A", Field("a"), CastAs(CastNumber)), ""},
	}
	for _, tt := range tests {
		if got := tt.f.EffectiveCast(); got != tt.want {
			t.Errorf("%s: %q want %q", tt.name, got, tt.want)
		}
	}
}

func TestCastValue(t *testing.T) {
	tests := []struct {
		raw  string
		c    Cast
		want any
	}{
		{"12", CastNumber, 12.0},
		{" 1.5 ", CastNumber, 1.5},
		{"0x10", CastNumber, 16.0},
		{"1e3", CastNumber, 1000.0},
		{"abc", CastNumber, "abc"},
		{"Infinity", CastNumber, "Infinity"},
		{"inf", CastNumber, "inf"},
		{"true", CastBoolean, true},
		{"1", CastBoolean, true},
		{"yes", CastBoolean, false},
		{"x", CastString, "x"},
		{"x", "", "x"},
	}
	for _, tt := range tests {
		if got := CastValue(tt.raw, tt.c); got != tt.want {
			t.Errorf("CastValue(%q,%q) = %#v want %#v", tt.raw, tt.c, got, tt.want)
		}
	}
}

func TestResolveValue(t *testing.T) {
	sel := Select("s", "S", Field("s"), Options(Opt(1, "A")))
	multi := Multi("m", "M", Field("m"), Options(Opt(1, "A")))
	multiEx := Multi("m", "M", Field("m"), Options(Opt(1, "A")), AllowExclude())
	date := Date("d", "D", Field("d"))
	rng := DateRange("r", "R", Field("r"))
	tests := []struct {
		name   string
		f      Filter
		v      FilterValue
		want   Resolved
		wantOK bool
	}{
		{"kosong diabaikan", sel, StringValue(""), Resolved{}, false},
		{"nil diabaikan", sel, nil, Resolved{}, false},
		{"select di-cast angka", sel, StringValue("1"), Resolved{Scalar: 1.0}, true},
		{"select '0' tetap terisi", sel, StringValue("0"), Resolved{Scalar: 0.0}, true},
		{"select bentuk salah", sel, In("1"), Resolved{}, false},
		{"date tetap string", date, StringValue("2026-01-01"), Resolved{Scalar: "2026-01-01"}, true},
		{"multi di-cast", multi, In("1", "2"), Resolved{Values: []any{1.0, 2.0}}, true},
		{"exclude diabaikan tanpa allowExclude", multi, NotIn("1"), Resolved{Values: []any{1.0}}, true},
		{"exclude dengan allowExclude", multiEx, NotIn("1"), Resolved{Values: []any{1.0}, Exclude: true}, true},
		{"multi bentuk salah", multi, StringValue("1"), Resolved{}, false},
		{"rentang terbalik ditukar", rng, Range("2026-02-01", "2026-01-01"), Resolved{From: Str("2026-01-01"), To: Str("2026-02-01")}, true},
		{"rentang terbuka", rng, RangeValue{From: Str("2026-01-01"), To: Str("")}, Resolved{From: Str("2026-01-01")}, true},
		{"rentang dari string = tanpa batas (seperti JS)", rng, StringValue("x"), Resolved{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveValue(tt.f, tt.v)
			if ok != tt.wantOK || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v,%v want %#v,%v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestEffectiveOperator(t *testing.T) {
	fc := FilterContext{Values: map[string]FilterValue{"arah": StringValue("sesudah")}}
	dyn := Date("d", "D", Field("d"), OpFunc(func(c FilterContext) Operator {
		if c.String("arah") == "sesudah" {
			return OpGte
		}
		return OpLte
	}))
	tests := []struct {
		name string
		f    Filter
		fc   FilterContext
		want Operator
	}{
		{"dinamis sesudah", dyn, fc, OpGte},
		{"dinamis default", dyn, FilterContext{}, OpLte},
		{"dinamis tidak valid → =", Date("d", "D", Field("d"), OpFunc(func(FilterContext) Operator { return OpContains })), fc, OpEq},
		{"select contains", Select("s", "S", Field("s"), Op(OpContains)), fc, OpContains},
		{"multi selalu =", Multi("m", "M", Field("m")), fc, OpEq},
	}
	for _, tt := range tests {
		if got := tt.f.EffectiveOperator(tt.fc); got != tt.want {
			t.Errorf("%s: %q", tt.name, got)
		}
	}
}

type ctxKey struct{}

func TestDescribeFilters(t *testing.T) {
	var gotCtx any
	defs := []Filter{
		Select("periode", "Periode", Field("periode"), Options(Opt("2024", "2024"), Opt("2025", "2025"))),
		Date("tgl", "Tanggal", Field("tgl"), DependsOn("periode"),
			MinFunc(func(c FilterContext) string {
				if p := c.String("periode"); p != "" {
					return p + "-01-01"
				}
				return ""
			}),
			MaxFunc(func(c FilterContext) string {
				if p := c.String("periode"); p != "" {
					return p + "-12-31"
				}
				return ""
			}),
		),
		DateRange("rentang", "Rentang", Field("tgl"), Min("2020-01-01"), MaxFunc(func(FilterContext) string { return "2030-12-31" })),
		Select("kec", "Kecamatan", Field("kecId"), OptionsFrom(func(c FilterContext) ([]Option, error) {
			gotCtx = c.Context().Value(ctxKey{})
			return []Option{{1, "Murung Pudak"}}, nil
		})),
		Multi("b", "B", Field("kat"), DependsOn("periode"), VisibleWhen(func(c FilterContext) bool { return c.Value("periode") != nil }),
			OptionsFrom(func(FilterContext) ([]Option, error) { return []Option{{1, "x"}}, nil })),
		Select("h", "H", Field("h"), Hidden(), Options(Opt("a", "A"))),
	}

	ctx := context.WithValue(context.Background(), ctxKey{}, "req-1")
	s, err := DescribeFilters(FilterContext{Ctx: ctx, Values: map[string]FilterValue{"periode": StringValue("2024")}}, defs)
	if err != nil {
		t.Fatal(err)
	}
	f := s.Filters
	if f[1].Min != "2024-01-01" || f[1].Max != "2024-12-31" || !reflect.DeepEqual(f[1].DependsOn, []string{"periode"}) || f[1].Format != FormatDate {
		t.Errorf("tgl: %+v", f[1])
	}
	if f[2].Min != "2020-01-01" || f[2].Max != "2030-12-31" {
		t.Errorf("rentang: %+v", f[2])
	}
	if !reflect.DeepEqual(f[3].Options, []Option{{1, "Murung Pudak"}}) || gotCtx != "req-1" {
		t.Errorf("opsi dinamis: %+v ctx=%v", f[3], gotCtx)
	}
	if !f[4].Visible || len(f[4].Options) != 1 || f[4].SearchThreshold == nil || *f[4].SearchThreshold != 5 {
		t.Errorf("bertingkat tampil: %+v", f[4])
	}
	if f[5].Visible || len(f[5].Options) != 0 || f[5].Options == nil {
		t.Errorf("hidden: %+v", f[5])
	}

	kosong, err := DescribeFilters(FilterContext{}, defs)
	if err != nil {
		t.Fatal(err)
	}
	if kosong.Filters[1].Min != "" || kosong.Filters[1].Max != "" {
		t.Errorf("min/max harus kosong: %+v", kosong.Filters[1])
	}
	if kosong.Filters[4].Visible || len(kosong.Filters[4].Options) != 0 {
		t.Errorf("bertingkat tersembunyi: %+v", kosong.Filters[4])
	}

	boom := errors.New("db mati")
	_, err = DescribeFilters(FilterContext{}, []Filter{Select("x", "X", Field("x"), OptionsFrom(func(FilterContext) ([]Option, error) { return nil, boom }))})
	if !errors.Is(err, boom) {
		t.Fatalf("error loader harus diteruskan: %v", err)
	}
}

func TestFilterTypes(t *testing.T) {
	got := FilterTypes([]Filter{Select("a", "A", Field("a")), Multi("b", "B", Field("b"))})
	if !reflect.DeepEqual(got, map[string]FilterType{"a": FilterSelect, "b": FilterMulti}) {
		t.Fatal(got)
	}
}
