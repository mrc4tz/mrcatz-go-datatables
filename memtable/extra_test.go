package memtable

import (
	"database/sql"
	"errors"
	"net/url"
	"reflect"
	"testing"
	"time"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
)

type kategori struct {
	Nama string `json:"nama"`
}

type Audit struct {
	CreatedAt time.Time `json:"createdAt"`
}

type barang struct {
	Audit
	ID       int            `json:"id"`
	Nama     string         `json:"nama"`
	Kode     string         `json:"kode"`
	Harga    float64        `json:"harga"`
	Stok     *int           `json:"stok"`
	Aktif    bool           `json:"aktif"`
	Kategori *kategori      `json:"kategori"`
	Tags     []string       `json:"tags"`
	Meta     map[string]any `json:"meta"`
	Catatan  sql.NullString `json:"catatan"`
	Rahasia  string         `json:"-"`
	TanpaTag string
	attrs    map[string]string //nolint:unused
}

func intp(n int) *int { return &n }

var barangRows = []barang{
	{ID: 1, Nama: "Kopi Arabika", Kode: "KP-01", Harga: 25000, Stok: intp(10), Aktif: true, Kategori: &kategori{"Minuman"}, Tags: []string{"panas"}, Meta: map[string]any{"asal": "Toraja"}, Catatan: sql.NullString{String: "favorit", Valid: true}, TanpaTag: "a", Audit: Audit{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}},
	{ID: 2, Nama: "Teh Hijau", Kode: "TH-02", Harga: 12000, Stok: nil, Aktif: false, Kategori: &kategori{"Minuman"}, Tags: []string{"dingin", "panas"}, Audit: Audit{time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}},
	{ID: 3, Nama: "Roti Kopi", Kode: "RK-03", Harga: 8000, Stok: intp(0), Aktif: true, Kategori: nil, Audit: Audit{time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}},
}

func TestLookup(t *testing.T) {
	b := barangRows[0]
	tests := []struct {
		path string
		want any
	}{
		{"id", 1},
		{"nama", "Kopi Arabika"},
		{"kategori.nama", "Minuman"},
		{"createdAt", b.CreatedAt},
		{"stok", 10},
		{"tags.0", "panas"},
		{"tags.5", nil},
		{"meta.asal", "Toraja"},
		{"meta.tidakada", nil},
		{"Rahasia", nil},
		{"TanpaTag", "a"},
		{"Nama", "Kopi Arabika"},
		{"attrs", nil},
		{"tidakAda", nil},
		{"nama.x", nil},
	}
	for _, tt := range tests {
		if got := Lookup(b, tt.path); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Lookup(%q) = %#v want %#v", tt.path, got, tt.want)
		}
		if got := Lookup(&b, tt.path); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Lookup(&b, %q) = %#v", tt.path, got)
		}
	}
	if got := Lookup(barangRows[2], "kategori.nama"); got != nil {
		t.Errorf("melewati pointer nil: %#v", got)
	}
	if got := Lookup(barangRows[1], "stok"); got != nil {
		t.Errorf("pointer nil: %#v", got)
	}
	if got := Lookup(map[string]any{"a": map[string]any{"b": 2}}, "a.b"); got != 2 {
		t.Errorf("map bersarang: %#v", got)
	}
	if got := Lookup(nil, "a"); got != nil {
		t.Errorf("nil row: %#v", got)
	}
}

func barangIDs(t *testing.T, table *Table[barang], q dt.TableQuery) []int {
	t.Helper()
	p, err := table.QueryParsed(ctx, q)
	return ids(t, p, err, func(b barang) int { return b.ID })
}

func TestSelectOperators(t *testing.T) {
	table := New(barangRows, Options{Filters: []dt.Filter{
		dt.Select("hargaMin", "Harga min", dt.Field("harga"), dt.Op(dt.OpGte), dt.CastAs(dt.CastNumber)),
		dt.Select("hargaLt", "Harga <", dt.Field("harga"), dt.Op(dt.OpLt), dt.CastAs(dt.CastNumber)),
		dt.Select("hargaGt", "Harga >", dt.Field("harga"), dt.Op(dt.OpGt), dt.CastAs(dt.CastNumber)),
		dt.Select("hargaLte", "Harga <=", dt.Field("harga"), dt.Op(dt.OpLte), dt.CastAs(dt.CastNumber)),
		dt.Select("bukanKode", "Bukan kode", dt.Field("kode"), dt.Op(dt.OpNe)),
		dt.Select("namaMirip", "Nama", dt.Field("nama"), dt.Op(dt.OpContains)),
		dt.Select("aktif", "Aktif", dt.Field("aktif"), dt.Options(dt.Opt(true, "Ya"), dt.Opt(false, "Tidak"))),
		dt.Select("kat", "Kategori", dt.Field("kategori.nama")),
		dt.Select("stok", "Stok", dt.Field("stok"), dt.Options(dt.Opt(0, "Habis"))),
		dt.Select("stokNe", "Stok !=", dt.Field("stok"), dt.Op(dt.OpNe), dt.Options(dt.Opt(0, "Habis"))),
		dt.Select("idStr", "Id (string)", dt.Field("id")),
		dt.Select("tanggal", "Tanggal", dt.Field("createdAt"), dt.Op(dt.OpGte)),
	}})
	tests := []struct {
		id    string
		value string
		want  []int
	}{
		{"hargaMin", "12000", []int{1, 2}},
		{"hargaLt", "12000", []int{3}},
		{"hargaGt", "12000", []int{1}},
		{"hargaLte", "12000", []int{2, 3}},
		{"hargaMin", "abc", []int{}},
		{"bukanKode", "TH-02", []int{1, 3}},
		{"namaMirip", "KOPI", []int{1, 3}},
		{"aktif", "true", []int{1, 3}},
		{"aktif", "0", []int{2}},
		{"kat", "Minuman", []int{1, 2}},
		{"stok", "0", []int{3}},
		{"stokNe", "0", []int{1, 2}},
		{"idStr", "2", []int{2}},
		{"tanggal", "2026-02-01T00:00:00Z", []int{2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.id+"="+tt.value, func(t *testing.T) {
			q := base()
			q.Filters[tt.id] = dt.StringValue(tt.value)
			if got := barangIDs(t, table, q); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestMultiAndCustomFilters(t *testing.T) {
	table := New(barangRows, Options{Filters: []dt.Filter{
		dt.Multi("kode", "Kode", dt.Field("kode")),
		dt.Multi("id", "Id", dt.Field("id"), dt.Options(dt.Opt(1, "a"))),
		dt.Multi("tag", "Tag", dt.MatchFunc(func(row any, v dt.Resolved) bool {
			for _, tag := range row.(barang).Tags {
				for _, want := range v.Values {
					if tag == want {
						return !v.Exclude
					}
				}
			}
			return v.Exclude
		}), dt.AllowExclude()),
		dt.Select("sql", "Hanya SQL", dt.WithWhere("engine-specific")),
		dt.DateRange("dibuat", "Dibuat", dt.Field("createdAt"), dt.Format(dt.FormatMonth)),
		dt.Select("catatan", "Catatan", dt.Field("catatan")),
	}, UTCOffset: "Z"})
	tests := []struct {
		name    string
		filters map[string]dt.FilterValue
		want    []int
	}{
		{"multi string", map[string]dt.FilterValue{"kode": dt.In("KP-01", "RK-03")}, []int{1, 3}},
		{"multi exclude tanpa allowExclude = include", map[string]dt.FilterValue{"kode": dt.NotIn("KP-01")}, []int{1}},
		{"multi angka", map[string]dt.FilterValue{"id": dt.In("2")}, []int{2}},
		{"match kustom", map[string]dt.FilterValue{"tag": dt.In("dingin")}, []int{2}},
		{"match kustom exclude", map[string]dt.FilterValue{"tag": dt.NotIn("dingin")}, []int{1, 3}},
		{"filter where-only diabaikan memtable", map[string]dt.FilterValue{"sql": dt.StringValue("x")}, []int{1, 2, 3}},
		{"rentang bulan", map[string]dt.FilterValue{"dibuat": dt.Range("2026-02", "2026-03")}, []int{2, 3}},
		{"driver.Valuer dibaca", map[string]dt.FilterValue{"catatan": dt.StringValue("favorit")}, []int{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := base()
			q.Filters = tt.filters
			if got := barangIDs(t, table, q); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestSearchModes(t *testing.T) {
	tests := []struct {
		name   string
		cfg    dt.SearchConfig
		search string
		want   []int
	}{
		{"contains lintas kolom relasi", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama"), dt.Col("kategori.nama")}}, "minuman", []int{1, 2}},
		{"equals", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("kode").Equals()}}, "kp-01", []int{1}},
		{"equals tidak cocok sebagian", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("kode").Equals()}}, "kp", []int{}},
		{"startsWith", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama").StartsWith()}}, "roti", []int{3}},
		{"startsWith bukan contains", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama").StartsWith()}}, "kopi", []int{1}},
		{"kolom angka hanya untuk kata angka", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("harga").Numeric()}}, "8000", []int{3}},
		{"kolom angka tidak cocok sebagian", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("harga").Numeric()}}, "800", []int{}},
		{"kolom angka mengabaikan teks", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("harga").Numeric()}}, "abc", []int{}},
		{"kolom angka nil tidak cocok 0", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("stok").Numeric()}}, "0", []int{3}},
		{"match all", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}}, "kopi teh", []int{}},
		{"match any", dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}, Match: dt.MatchAny}, "kopi teh", []int{1, 2, 3}},
		{"tanpa kolom pencarian, search diabaikan", dt.SearchConfig{}, "apapun", []int{1, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			table := New(barangRows, Options{Search: &cfg})
			q := base()
			q.Search = tt.search
			if got := barangIDs(t, table, q); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

type artikel struct {
	ID    int    `json:"id"`
	Judul string `json:"judul"`
	Isi   string `json:"isi"`
}

func TestRelevanceOrdering(t *testing.T) {
	rows := []artikel{
		{1, "Resep kue", "cara membuat kopi susu"},
		{2, "Kopi nusantara", "sejarah"},
		{3, "Kopi kopi", "kopi"},
		{4, "Teh", "tanpa"},
	}
	table := New(rows, Options{
		Search:      &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("judul").W(3), dt.Col("isi")}},
		DefaultSort: []dt.SortRule{{Key: "id", Dir: dt.Desc}},
	})
	run := func(q dt.TableQuery) []int {
		p, err := table.QueryParsed(ctx, q)
		return ids(t, p, err, func(a artikel) int { return a.ID })
	}
	q := base()
	q.Search = "kopi"
	// skor: #3 = 3+1, #2 = 3, #1 = 1 → relevansi didahulukan
	if got := run(q); !reflect.DeepEqual(got, []int{3, 2, 1}) {
		t.Fatalf("relevansi: %v", got)
	}
	// sort eksplisit mengalahkan relevansi
	q.Sort = []dt.SortRule{{Key: "id", Dir: dt.Asc}}
	if got := run(q); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("sort eksplisit: %v", got)
	}
	// tanpa search → defaultSort
	q = base()
	if got := run(q); !reflect.DeepEqual(got, []int{4, 3, 2, 1}) {
		t.Fatalf("defaultSort: %v", got)
	}
	// skor sama → defaultSort sebagai penentu
	tie := New([]artikel{{1, "kopi", ""}, {2, "kopi", ""}}, Options{
		Search:      &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("judul")}},
		DefaultSort: []dt.SortRule{{Key: "id", Dir: dt.Desc}},
	})
	q.Search = "kopi"
	p, _ := tie.QueryParsed(ctx, q)
	if p.Data[0].ID != 2 {
		t.Fatalf("tiebreak: %v", p.Data)
	}

	// typo: satu kecocokan persis mengalahkan bonus trigram
	fuzzy := New([]artikel{{1, "kopiah kopiah kopiah", ""}, {2, "kopi", ""}, {3, "kapi kopa kipo", ""}}, Options{
		Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("judul")}, TypoTolerance: &dt.TypoTolerance{MinWordLength: 4}},
	})
	q.Search = "kopi"
	p, _ = fuzzy.QueryParsed(ctx, q)
	got := []int{}
	for _, a := range p.Data {
		got = append(got, a.ID)
	}
	if got[len(got)-1] != 3 || len(got) != 3 {
		t.Fatalf("fuzzy: %v", got)
	}
}

func TestSortValues(t *testing.T) {
	table := New(barangRows, Options{})
	tests := []struct {
		rule dt.SortRule
		want []int
	}{
		{dt.SortRule{Key: "harga", Dir: dt.Asc}, []int{3, 2, 1}},
		{dt.SortRule{Key: "createdAt", Dir: dt.Desc}, []int{3, 2, 1}},
		{dt.SortRule{Key: "aktif", Dir: dt.Asc}, []int{2, 1, 3}},
		{dt.SortRule{Key: "kategori.nama", Dir: dt.Asc}, []int{3, 1, 2}},
		{dt.SortRule{Key: "nama", Dir: dt.Asc}, []int{1, 3, 2}},
		{dt.SortRule{Key: "tidakAda", Dir: dt.Desc}, []int{1, 2, 3}},
	}
	for _, tt := range tests {
		q := base()
		q.Sort = []dt.SortRule{tt.rule}
		if got := barangIDs(t, table, q); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%+v: got %v want %v", tt.rule, got, tt.want)
		}
	}
}

func TestURLValuesAPI(t *testing.T) {
	calls := 0
	table := NewFunc(func() []barang { calls++; return barangRows }, Options{
		Prefix:  "b_",
		Search:  &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}},
		Filters: []dt.Filter{dt.Multi("id", "Id", dt.Field("id"), dt.Options(dt.Opt(1, "a")), dt.AllowExclude())},
	})
	v := url.Values{"b_f.id": {"1"}, "b_f.id.not": {"1"}, "b_sort": {"id:desc"}, "b_limit": {"1"}, "search": {"abaikan"}}
	p, err := table.Query(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if p.Meta.Total != 2 || p.Meta.Limit != 1 || p.Data[0].ID != 3 || !p.Meta.HasMore {
		t.Fatalf("query: %+v %v", p.Meta, p.Data)
	}
	all, err := table.ExportRows(ctx, v)
	if err != nil || len(all) != 2 || all[0].ID != 3 || all[1].ID != 2 {
		t.Fatalf("export: %v %v", all, err)
	}
	n, err := table.Count(ctx, table.Parse(url.Values{"b_search": {"kopi"}}))
	if err != nil || n != 2 {
		t.Fatalf("count: %d %v", n, err)
	}
	if calls != 3 {
		t.Fatalf("sumber dinamis dipanggil tiap query: %d", calls)
	}
	all, err = table.All(ctx, dt.TableQuery{})
	if err != nil || len(all) != 3 {
		t.Fatalf("all tanpa query: %v %v", all, err)
	}
}

func TestConfigPanics(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{"id filter duplikat", Options{Filters: []dt.Filter{dt.Select("a", "A", dt.Field("a")), dt.Multi("a", "A", dt.Field("b"))}}},
		{"offset tidak valid", Options{UTCOffset: "WITA"}},
		{"search tidak valid", Options{Search: &dt.SearchConfig{Match: "most"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				err, _ := r.(error)
				var ce *dt.ConfigError
				if !errors.As(err, &ce) {
					t.Fatalf("harus panic *ConfigError, dapat %#v", r)
				}
			}()
			New(barangRows, tt.opts)
		})
	}
}

func TestStringDateCells(t *testing.T) {
	type row struct {
		ID int `json:"id"`
		At any `json:"at"`
	}
	rows := []row{
		{1, "2026-01-05"},                // tanggal saja → UTC
		{2, "2026-01-05T23:30"},          // tanpa zona → UTCOffset (+08:00)
		{3, "2026-01-05T23:30:00+08:00"}, // dengan zona
		{4, float64(time.Date(2026, 1, 6, 1, 0, 0, 0, time.UTC).UnixMilli())}, // epoch ms
		{5, "bukan tanggal"},
		{6, nil},
	}
	table := New(rows, Options{UTCOffset: "+08:00", Filters: []dt.Filter{dt.Date("tgl", "Tgl", dt.Field("at"))}})
	q := base()
	q.Filters["tgl"] = dt.StringValue("2026-01-05") // [2026-01-04T16:00Z, 2026-01-05T16:00Z)
	p, err := table.QueryParsed(ctx, q)
	if got := ids(t, p, err, func(r row) int { return r.ID }); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("got %v", got)
	}
	q.Filters["tgl"] = dt.StringValue("2026-01-06")
	p, err = table.QueryParsed(ctx, q)
	if got := ids(t, p, err, func(r row) int { return r.ID }); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("epoch: %v", got)
	}
}
