package datatable

import (
	"net/url"
	"reflect"
	"testing"
)

func TestQueryRoundTripWithPrefix(t *testing.T) {
	q := TableQuery{
		Page:   3,
		Limit:  25,
		Search: "budi",
		Sort:   []SortRule{{"nama", Asc}, {"createdAt", Desc}},
		Filters: map[string]FilterValue{
			"status":   StringValue("aktif"),
			"kategori": NotIn("1", "3"),
			"dibuat":   Range("2026-01-01", "2026-01-31"),
		},
	}
	params := FormatQuery(q, "u_")
	if got := params.Get("u_sort"); got != "nama:asc,createdAt:desc" {
		t.Fatalf("u_sort = %q", got)
	}
	got := ParseQuery(params, ParseOptions{Prefix: "u_"})
	if !reflect.DeepEqual(got, q) {
		t.Fatalf("round-trip:\n got %#v\nwant %#v", got, q)
	}
}

func TestParseQuery(t *testing.T) {
	multi := map[string]FilterType{"kat": FilterMulti}
	tests := []struct {
		name string
		raw  string
		opts ParseOptions
		want TableQuery
	}{
		{
			name: "multi dengan satu nilai tetap multi bila tipenya diketahui",
			raw:  "f.kat=2",
			opts: ParseOptions{FilterTypes: multi},
			want: TableQuery{Page: 1, Limit: 10, Sort: []SortRule{}, Filters: map[string]FilterValue{"kat": In("2")}},
		},
		{
			name: "membatasi limit dan memakai default",
			raw:  "limit=99999&page=-1",
			opts: ParseOptions{MaxLimit: 50},
			want: TableQuery{Page: 1, Limit: 50, Sort: []SortRule{}, Filters: map[string]FilterValue{}},
		},
		{
			name: "mengabaikan param milik tabel lain",
			raw:  "search=x&b_search=y",
			opts: ParseOptions{Prefix: "b_"},
			want: TableQuery{Page: 1, Limit: 10, Search: "y", Sort: []SortRule{}, Filters: map[string]FilterValue{}},
		},
		{
			name: "multi + .not dengan tipe (seperti engine server)",
			raw:  "f.kat=1&f.kat.not=1",
			opts: ParseOptions{FilterTypes: multi},
			want: TableQuery{Page: 1, Limit: 10, Sort: []SortRule{}, Filters: map[string]FilterValue{"kat": NotIn("1")}},
		},
		{
			name: ".not sebelum nilai di URL tetap digabung",
			raw:  "f.kat.not=true&f.kat=1&f.kat=2",
			want: TableQuery{Page: 1, Limit: 10, Sort: []SortRule{}, Filters: map[string]FilterValue{"kat": NotIn("1", "2")}},
		},
		{
			name: "multi tanpa tipe dengan dua nilai; nilai kosong dibuang",
			raw:  "f.kat=1&f.kat=&f.kat=3",
			want: TableQuery{Page: 1, Limit: 10, Sort: []SortRule{}, Filters: map[string]FilterValue{"kat": In("1", "3")}},
		},
		{
			name: "select tipe diketahui dengan banyak nilai memakai yang pertama",
			raw:  "f.s=a&f.s=b",
			opts: ParseOptions{FilterTypes: map[string]FilterType{"s": FilterSelect}},
			want: TableQuery{Page: 1, Limit: 10, Sort: []SortRule{}, Filters: map[string]FilterValue{"s": StringValue("a")}},
		},
		{
			name: "rentang terbuka & id dengan titik",
			raw:  "f.a.b.from=2026-01-01&f.x.to=2026-02-01",
			want: TableQuery{Page: 1, Limit: 10, Sort: []SortRule{}, Filters: map[string]FilterValue{
				"a.b": RangeValue{From: Str("2026-01-01")},
				"x":   RangeValue{To: Str("2026-02-01")},
			}},
		},
		{
			name: "parseInt gaya JS, search di-trim, sort default",
			raw:  "page=2abc&limit=%207&search=%20%20budi%20",
			opts: ParseOptions{DefaultSort: []SortRule{{"id", Desc}}},
			want: TableQuery{Page: 2, Limit: 7, Search: "budi", Sort: []SortRule{{"id", Desc}}, Filters: map[string]FilterValue{}},
		},
		{
			name: "limit 0 / tidak valid memakai default; defaultLimit kustom",
			raw:  "limit=0&page=abc",
			opts: ParseOptions{DefaultLimit: 25},
			want: TableQuery{Page: 1, Limit: 25, Sort: []SortRule{}, Filters: map[string]FilterValue{}},
		},
		{
			name: "page sangat besar dijenuhkan",
			raw:  "page=999999999999999999999999",
			want: TableQuery{Page: maxPage, Limit: 10, Sort: []SortRule{}, Filters: map[string]FilterValue{}},
		},
		{
			name: "nilai terakhir menang untuk kunci tunggal",
			raw:  "search=a&search=b&sort=x:desc",
			want: TableQuery{Page: 1, Limit: 10, Search: "b", Sort: []SortRule{{"x", Desc}}, Filters: map[string]FilterValue{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := url.ParseQuery(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			got := ParseQuery(v, tt.opts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("\n got %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestParseSort(t *testing.T) {
	tests := []struct {
		raw  string
		want []SortRule
	}{
		{"", []SortRule{}},
		{"nama", []SortRule{{"nama", Asc}}},
		{"nama:desc, createdAt:asc", []SortRule{{"nama", Desc}, {"createdAt", Asc}}},
		{"nama:DESC", []SortRule{{"nama", Asc}}},
		{",,:desc,a:desc:x", []SortRule{{"a", Desc}}},
	}
	for _, tt := range tests {
		if got := ParseSort(tt.raw); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseSort(%q) = %#v, want %#v", tt.raw, got, tt.want)
		}
	}
	if got := FormatSort([]SortRule{{"a", Asc}, {"b", Desc}}); got != "a:asc,b:desc" {
		t.Errorf("FormatSort = %q", got)
	}
}

func TestFormatQuery(t *testing.T) {
	got := FormatQuery(TableQuery{
		Page: 1,
		Filters: map[string]FilterValue{
			"kosong": StringValue(""),
			"nol":    StringValue("0"),
			"m":      In(),
			"r":      RangeValue{From: Str(""), To: Str("")},
			"inc":    In("a"),
		},
	}, "")
	want := url.Values{"f.nol": {"0"}, "f.inc": {"a"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestIsTableParam(t *testing.T) {
	tests := []struct {
		key, prefix string
		want        bool
	}{
		{"search", "", true},
		{"page", "", true},
		{"limit", "", true},
		{"sort", "", true},
		{"f.status", "", true},
		{"utm_source", "", false},
		{"u_search", "u_", true},
		{"search", "u_", false},
		{"u_f.x.from", "u_", true},
	}
	for _, tt := range tests {
		if got := IsTableParam(tt.key, tt.prefix); got != tt.want {
			t.Errorf("IsTableParam(%q,%q) = %v", tt.key, tt.prefix, got)
		}
	}
}

func TestIsEmpty(t *testing.T) {
	tests := []struct {
		v    FilterValue
		want bool
	}{
		{nil, true},
		{StringValue(""), true},
		{StringValue("0"), false},
		{In(), true},
		{In("0"), false},
		{RangeValue{}, true},
		{RangeValue{From: Str("")}, true},
		{RangeValue{To: Str("2026")}, false},
	}
	for i, tt := range tests {
		if got := IsEmpty(tt.v); got != tt.want {
			t.Errorf("#%d IsEmpty(%#v) = %v", i, tt.v, got)
		}
	}
}

func TestNewMeta(t *testing.T) {
	tests := []struct {
		total       int64
		page, limit int
		want        PageMeta
	}{
		{3, 2, 2, PageMeta{3, 2, 2, 2, false}},
		{3, 1, 2, PageMeta{3, 1, 2, 2, true}},
		{0, 1, 10, PageMeta{0, 1, 10, 1, false}},
		{92, 1, 10, PageMeta{92, 1, 10, 10, true}},
	}
	for _, tt := range tests {
		if got := NewMeta(tt.total, tt.page, tt.limit); got != tt.want {
			t.Errorf("NewMeta(%d,%d,%d) = %+v", tt.total, tt.page, tt.limit, got)
		}
	}
}
