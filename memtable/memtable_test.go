package memtable

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"testing"
	"time"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
)

var ctx = context.Background()

func base() dt.TableQuery {
	return dt.TableQuery{Page: 1, Limit: 10, Sort: []dt.SortRule{}, Filters: map[string]dt.FilterValue{}}
}

type orang struct {
	ID     int    `json:"id"`
	Nama   string `json:"nama"`
	Status string `json:"status"`
	Kat    int    `json:"kat"`
	At     string `json:"at"`
}

var orangRows = []orang{
	{1, "Budi Santoso", "aktif", 1, "2026-01-05T02:00:00Z"},
	{2, "Siti Aminah", "nonaktif", 2, "2026-02-10T02:00:00Z"},
	{3, "Budi Hartono", "aktif", 3, "2026-03-15T02:00:00Z"},
}

func ids[T any](t *testing.T, p *dt.Page[T], err error, id func(T) int) []int {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	out := []int{}
	for _, r := range p.Data {
		out = append(out, id(r))
	}
	return out
}

func orangID(o orang) int { return o.ID }

func orangTable() *Table[orang] {
	return New(orangRows, Options{
		Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama").W(2)}},
		Filters: []dt.Filter{
			dt.Select("status", "Status", dt.Field("status"), dt.Options(dt.Opt("aktif", "Aktif"))),
			dt.Multi("kat", "Kategori", dt.Field("kat"), dt.Options(dt.Opt(1, "A")), dt.AllowExclude()),
			dt.DateRange("at", "Tanggal", dt.Field("at")),
		},
		UTCOffset: "Z",
	})
}

func TestMemoryTable(t *testing.T) {
	table := orangTable()
	with := func(mod func(*dt.TableQuery)) dt.TableQuery { q := base(); mod(&q); return q }
	tests := []struct {
		name string
		q    dt.TableQuery
		want []int
	}{
		{"search semua kata harus cocok", with(func(q *dt.TableQuery) { q.Search = "budi hartono" }), []int{3}},
		{"filter multi exclude dengan cast angka", with(func(q *dt.TableQuery) { q.Filters["kat"] = dt.NotIn("1") }), []int{2, 3}},
		{"filter multi include", with(func(q *dt.TableQuery) { q.Filters["kat"] = dt.In("1", "3") }), []int{1, 3}},
		{"filter rentang terbuka", with(func(q *dt.TableQuery) { q.Filters["at"] = dt.RangeValue{From: dt.Str("2026-02-01")} }), []int{2, 3}},
		{"filter rentang tertutup inklusif", with(func(q *dt.TableQuery) { q.Filters["at"] = dt.Range("2026-01-01", "2026-02-10") }), []int{1, 2}},
		{"filter select", with(func(q *dt.TableQuery) { q.Filters["status"] = dt.StringValue("aktif") }), []int{1, 3}},
		{"filter kosong diabaikan", with(func(q *dt.TableQuery) { q.Filters["status"] = dt.StringValue("") }), []int{1, 2, 3}},
		{"search tanpa hasil", with(func(q *dt.TableQuery) { q.Search = "joko" }), []int{}},
		{"sort desc", with(func(q *dt.TableQuery) { q.Sort = []dt.SortRule{{Key: "nama", Dir: dt.Desc}} }), []int{2, 1, 3}},
		{"sort multi", with(func(q *dt.TableQuery) {
			q.Sort = []dt.SortRule{{Key: "status", Dir: dt.Asc}, {Key: "id", Dir: dt.Desc}}
		}), []int{3, 1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := table.QueryParsed(ctx, tt.q)
			if got := ids(t, p, err, orangID); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestPaginationMeta(t *testing.T) {
	q := base()
	q.Limit, q.Page = 2, 2
	p, err := orangTable().QueryParsed(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if p.Meta != (dt.PageMeta{Total: 3, Page: 2, Limit: 2, TotalPages: 2, HasMore: false}) || len(p.Data) != 1 {
		t.Fatalf("meta %+v data %d", p.Meta, len(p.Data))
	}
	q.Page = 1
	p, _ = orangTable().QueryParsed(ctx, q)
	if !p.Meta.HasMore || len(p.Data) != 2 {
		t.Fatalf("page 1: %+v", p.Meta)
	}
	q.Page = 9
	p, _ = orangTable().QueryParsed(ctx, q)
	if len(p.Data) != 0 || p.Meta.HasMore {
		t.Fatalf("page di luar jangkauan: %+v", p.Meta)
	}
	// normalisasi page/limit
	p, _ = orangTable().QueryParsed(ctx, dt.TableQuery{Page: -3, Limit: 0})
	if p.Meta.Page != 1 || p.Meta.Limit != 10 || len(p.Data) != 3 {
		t.Fatalf("normalisasi: %+v", p.Meta)
	}
	p, _ = New(orangRows, Options{MaxLimit: 2}).QueryParsed(ctx, dt.TableQuery{Page: 1, Limit: 50})
	if p.Meta.Limit != 2 {
		t.Fatalf("maxLimit: %+v", p.Meta)
	}
}

func TestTypoTolerance(t *testing.T) {
	fuzzy := New(orangRows, Options{Search: &dt.SearchConfig{
		Columns:       []dt.SearchColumn{dt.Col("nama")},
		TypoTolerance: &dt.TypoTolerance{},
	}})
	q := base()
	q.Search = "santosa"
	p, err := fuzzy.QueryParsed(ctx, q)
	if err != nil || len(p.Data) == 0 || p.Data[0].ID != 1 {
		t.Fatalf("typo: %+v %v", p, err)
	}
	// kata < minWordLength tidak memakai trigram
	q.Search = "bdx"
	if p, _ := fuzzy.QueryParsed(ctx, q); len(p.Data) != 0 {
		t.Fatalf("kata pendek: %v", p.Data)
	}
	// tanpa typo tolerance, salah ketik tidak ditemukan
	q.Search = "santosa"
	if p, _ := orangTable().QueryParsed(ctx, q); len(p.Data) != 0 {
		t.Fatalf("tanpa typo: %v", p.Data)
	}
}

func TestDescribeCascading(t *testing.T) {
	table := New(orangRows, Options{Filters: []dt.Filter{
		dt.Select("a", "A", dt.Field("status"), dt.Options()),
		dt.Select("b", "B", dt.Field("kat"),
			dt.OptionsFrom(func(dt.FilterContext) ([]dt.Option, error) { return []dt.Option{dt.Opt(1, "x")}, nil }),
			dt.DependsOn("a"),
			dt.VisibleWhen(func(c dt.FilterContext) bool { return !dt.IsEmpty(c.Value("a")) })),
	}})
	hidden, err := table.DescribeWith(dt.FilterContext{})
	if err != nil {
		t.Fatal(err)
	}
	if f := hidden.Filters[1]; f.Visible || len(f.Options) != 0 {
		t.Fatalf("hidden: %+v", f)
	}
	shown, err := table.Describe(ctx, url.Values{"f.a": {"aktif"}})
	if err != nil {
		t.Fatal(err)
	}
	if f := shown.Filters[1]; !f.Visible || !reflect.DeepEqual(f.DependsOn, []string{"a"}) || len(f.Options) != 1 {
		t.Fatalf("shown: %+v", f)
	}
	b, _ := json.Marshal(shown)
	want := `{"filters":[{"id":"a","label":"A","type":"select","visible":true,"options":[]},{"id":"b","label":"B","type":"select","visible":true,"dependsOn":["a"],"options":[{"value":1,"label":"x"}]}]}`
	if string(b) != want {
		t.Fatalf("json %s", b)
	}
}

type jadwal struct {
	ID  int       `json:"id"`
	At  time.Time `json:"at"`
	Jam string    `json:"jam"`
}

func TestTimeFilters(t *testing.T) {
	rows := []jadwal{
		{1, time.Date(2026, 10, 19, 0, 15, 0, 0, time.UTC), "08:15:00"}, // 08:15 WITA
		{2, time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC), "12:00:30"},  // 12:00 WITA
		{3, time.Date(2026, 10, 20, 9, 45, 0, 0, time.UTC), "17:45:00"}, // 17:45 WITA
	}
	table := New(rows, Options{
		UTCOffset: "+08:00",
		Filters: []dt.Filter{
			dt.Date("jamMulai", "Jam", dt.Field("at"), dt.Format(dt.FormatTimeHm), dt.Op(dt.OpGte)),
			dt.DateRange("jamKerja", "Jam kerja", dt.Field("at"), dt.Format(dt.FormatTimeHm)),
			dt.Date("jamTeks", "Jam (teks)", dt.Field("jam"), dt.Format(dt.FormatTime)),
		},
	})
	tests := []struct {
		name    string
		filters map[string]dt.FilterValue
		want    []int
	}{
		{"jam >= 12:00", map[string]dt.FilterValue{"jamMulai": dt.StringValue("12:00")}, []int{2, 3}},
		{"rentang jam kerja", map[string]dt.FilterValue{"jamKerja": dt.Range("08:00", "12:00")}, []int{1, 2}},
		{"jam teks dengan detik", map[string]dt.FilterValue{"jamTeks": dt.StringValue("12:00:30")}, []int{2}},
		{"jam teks tanpa detik = satu menit", map[string]dt.FilterValue{"jamTeks": dt.StringValue("12:00")}, []int{2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := base()
			q.Filters = tt.filters
			p, err := table.QueryParsed(ctx, q)
			if got := ids(t, p, err, func(j jadwal) int { return j.ID }); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

type tglRow struct {
	ID  int    `json:"id"`
	Tgl string `json:"tgl"`
}

func TestDynamicOperatorAndBounds(t *testing.T) {
	defs := []dt.Filter{
		dt.Select("periode", "Periode", dt.Field("periode"), dt.Options(dt.Opt("2024", "2024"), dt.Opt("2025", "2025"))),
		dt.Date("tgl", "Tanggal", dt.Field("tgl"), dt.DependsOn("periode"),
			dt.MinFunc(func(c dt.FilterContext) string {
				if p := c.String("periode"); p != "" {
					return p + "-01-01"
				}
				return ""
			}),
			dt.MaxFunc(func(c dt.FilterContext) string {
				if p := c.String("periode"); p != "" {
					return p + "-12-31"
				}
				return ""
			}),
			dt.OpFunc(func(c dt.FilterContext) dt.Operator {
				if c.String("arah") == "sesudah" {
					return dt.OpGte
				}
				return dt.OpLte
			})),
		dt.DateRange("rentang", "Rentang", dt.Field("tgl"), dt.Min("2020-01-01"), dt.MaxFunc(func(dt.FilterContext) string { return "2030-12-31" })),
	}
	rows := []tglRow{{1, "2026-01-10T00:00:00Z"}, {2, "2026-03-10T00:00:00Z"}}
	table := New(rows, Options{Filters: defs, UTCOffset: "Z"})

	s, err := table.DescribeWith(dt.FilterContext{Values: map[string]dt.FilterValue{"periode": dt.StringValue("2024")}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Filters[1].Min != "2024-01-01" || s.Filters[1].Max != "2024-12-31" || s.Filters[2].Min != "2020-01-01" || s.Filters[2].Max != "2030-12-31" {
		t.Fatalf("min/max: %+v", s.Filters)
	}

	run := func(f map[string]dt.FilterValue) []int {
		q := base()
		q.Filters = f
		p, err := table.QueryParsed(ctx, q)
		return ids(t, p, err, func(r tglRow) int { return r.ID })
	}
	if got := run(map[string]dt.FilterValue{"tgl": dt.StringValue("2026-02-01")}); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("<=: %v", got)
	}
	if got := run(map[string]dt.FilterValue{"tgl": dt.StringValue("2026-02-01"), "arah": dt.StringValue("sesudah")}); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf(">=: %v", got)
	}
}

func TestInvalidDateIsQueryError(t *testing.T) {
	table := New([]tglRow{}, Options{Filters: []dt.Filter{dt.Date("tgl", "Tgl", dt.Field("tgl"))}})
	_, err := table.Query(ctx, url.Values{"f.tgl": {"kemarin"}})
	var qe *dt.QueryError
	if !errors.As(err, &qe) || qe.StatusCode() != 400 || !errors.Is(err, dt.ErrInvalidDate) {
		t.Fatalf("err = %v", err)
	}
	_, err = table.ExportRows(ctx, url.Values{"f.tgl": {"2026-13-45x"}})
	if err != nil {
		t.Fatalf("tanggal dinormalisasi seperti JS, bukan error: %v", err)
	}
}
