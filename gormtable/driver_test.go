package gormtable

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
)

type fakeDriver struct {
	name     string
	result   *DriverResult
	err      error
	calls    []DriverRequest
	validate func(TableConfig) error
}

func (d *fakeDriver) Name() string { return d.name }
func (d *fakeDriver) Search(_ context.Context, req DriverRequest) (*DriverResult, error) {
	d.calls = append(d.calls, req)
	return d.result, d.err
}

type validatingDriver struct{ *fakeDriver }

func (d validatingDriver) Validate(cfg TableConfig) error { return d.validate(cfg) }

func TestDriverRankedPipeline(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	rec := record(db)
	drv := &fakeDriver{name: "custom", result: &DriverResult{IDs: []any{float64(3), int64(1), "4", 2, 99}, PushedFilters: []string{"status"}}}
	tb := mustTable(t, Definition[Produk]{
		DB:     db,
		Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}},
		Driver: drv,
		Filters: []dt.Filter{
			dt.Select("status", "Status", dt.Field("status")),
			dt.Select("aktif", "Aktif", dt.Field("aktif"), dt.CastAs(dt.CastBoolean)),
		},
	})
	page, err := tb.Query(ctx, vals("search", "n", "limit", "2", "f.status", "tidak-ada", "f.aktif", "true"))
	if err != nil {
		t.Fatal(err)
	}
	// aktif: 1,3,4,5 ∩ ids → urutan peringkat [3, 1, 4]; status di-pushdown → tidak di SQL
	eqIDs(t, produkIDs(page.Data), 3, 1)
	if page.Meta != dt.NewMeta(3, 1, 2) {
		t.Fatalf("meta = %+v", page.Meta)
	}
	sqls := strings.Join(rec.all(), "\n")
	if strings.Contains(sqls, "tidak-ada") || !strings.Contains(sqls, "`produks`.`aktif` = true") ||
		strings.Contains(sqls, "LIKE") {
		t.Fatalf("SQL:\n%s", sqls)
	}
	page, _ = tb.Query(ctx, vals("search", "n", "limit", "2", "page", "2", "f.aktif", "true"))
	eqIDs(t, produkIDs(page.Data), 4)

	req := drv.calls[0]
	if req.Search != "n" || req.PrimaryKey != "id" || len(req.Filters) != 2 || req.Filters[0].Filter.ID != "status" ||
		req.Filters[1].Value.Scalar != true || req.FilterContext.String("status") != "tidak-ada" {
		t.Fatalf("req = %+v", req)
	}

	// hasil kosong → halaman kosong tanpa query
	drv.result = &DriverResult{IDs: nil}
	page, _ = tb.Query(ctx, vals("search", "zzz"))
	if len(page.Data) != 0 || page.Meta.Total != 0 || page.Meta.TotalPages != 1 {
		t.Fatalf("page = %+v", page)
	}
}

func TestDriverUserSortAndFallback(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	rec := record(db)
	drv := &fakeDriver{name: "x", result: &DriverResult{IDs: []any{2, 1, 5}}}
	tb := mustTable(t, Definition[Produk]{DB: db, Sortable: []string{"nama"},
		Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}}, Driver: drv})
	page, err := tb.Query(ctx, vals("search", "n", "sort", "nama:desc"))
	if err != nil {
		t.Fatal(err)
	}
	eqIDs(t, produkIDs(page.Data), 2, 5, 1)
	if s := strings.Join(rec.all(), "\n"); !strings.Contains(s, "`produks`.`id` IN (2,1,5) ORDER BY `produks`.`nama` DESC,`produks`.`id` LIMIT 10") {
		t.Fatalf("SQL:\n%s", s)
	}
	// sort tidak valid ditolak sebelum driver dipanggil
	n := len(drv.calls)
	if _, err := tb.Query(ctx, vals("search", "n", "sort", "stok:asc")); !isQueryErr(err) || len(drv.calls) != n {
		t.Fatalf("err = %v, calls = %d", err, len(drv.calls))
	}

	// tanpa kata kunci → driver tidak dipanggil; nil → jalur SQL biasa
	drv.calls, drv.result = nil, nil
	if _, err := tb.Query(ctx, vals("search", "   ")); err != nil || len(drv.calls) != 0 {
		t.Fatalf("calls = %d, err = %v", len(drv.calls), err)
	}
	page, _ = tb.Query(ctx, vals("search", "budi"))
	if len(drv.calls) != 1 {
		t.Fatal("driver harus dipanggil")
	}
	eqIDs(t, produkIDs(page.Data), 1, 3)

	// error driver diteruskan
	drv.err = errors.New("meili mati")
	if _, err := tb.Query(ctx, vals("search", "budi")); err == nil || err.Error() != "meili mati" {
		t.Fatalf("err = %v", err)
	}
}

func TestDriverValidate(t *testing.T) {
	db := openSQLite(t)
	var got TableConfig
	d := validatingDriver{&fakeDriver{name: "v", validate: func(c TableConfig) error { got = c; return nil }}}
	mustTable(t, Definition[Produk]{DB: db, Driver: d, Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}}})
	if got.PrimaryKey != "id" || got.Sortable != nil || got.Driver == nil {
		t.Fatalf("cfg = %+v", got)
	}
	d.validate = func(TableConfig) error { return dt.ConfigErrorf("tolak") }
	if _, err := Define(Definition[Produk]{DB: db, Driver: d}); !isConfigErr(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestRelevanceSQLEscapeAndParams(t *testing.T) {
	sql, args, ok, err := BuildRelevanceSQL("50%_off'", Relevance{Table: "public.produk"},
		dt.NormalizeSearch(&dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama").W(2)}}))
	if err != nil || !ok {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `FROM "public"."produk"`) || strings.Contains(sql, "50%") {
		t.Fatalf("sql = %s", sql)
	}
	if !reflect.DeepEqual(args, []any{`%50\%\_off'%`}) {
		t.Fatalf("args = %#v", args)
	}
	want := `SELECT "id" AS id, (CASE WHEN LOWER(CAST("nama" AS TEXT)) LIKE $1 ESCAPE '\' THEN 2 ELSE 0 END) AS score ` +
		`FROM "public"."produk" WHERE (LOWER(CAST("nama" AS TEXT)) LIKE $1 ESCAPE '\') ORDER BY score DESC LIMIT 2000`
	if sql != want {
		t.Fatalf("sql =\n%s\nwant\n%s", sql, want)
	}
	if _, _, ok, _ := BuildRelevanceSQL("  ", Relevance{Table: "x"}, dt.NormalizeSearch(&dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("a")}})); ok {
		t.Fatal("tanpa kata kunci harus ok=false")
	}
}

func TestRelevanceSQLTypo(t *testing.T) {
	s := dt.NormalizeSearch(&dt.SearchConfig{
		Columns:       []dt.SearchColumn{dt.Col("nama").W(3), dt.Col("sku").StartsWith(), dt.Col("kode").Equals()},
		Match:         dt.MatchAny,
		TypoTolerance: &dt.TypoTolerance{Driver: dt.TypoPgTrgm},
	})
	sql, args, _, err := BuildRelevanceSQL("kopi ab", Relevance{Table: "produk", MaxCandidates: 50}, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"THEN 300 ELSE 0", "THEN 100 ELSE 0", `LOWER(CAST("nama" AS TEXT)) % $2`, ") OR (", "LIMIT 50"} {
		if !strings.Contains(sql, want) {
			t.Errorf("sql tidak memuat %q:\n%s", want, sql)
		}
	}
	// kopi: 3 kolom LIKE + 3 pg_trgm; ab (pendek): 3 LIKE saja
	want := []any{"%kopi%", "kopi", "kopi%", "kopi", "kopi", "kopi", "%ab%", "ab%", "ab"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v", args)
	}
	if strings.Contains(sql, "?") || !strings.Contains(sql, "$9") || strings.Contains(sql, "$10") {
		t.Fatalf("placeholder: %s", sql)
	}

	tri := dt.NormalizeSearch(&dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}, TypoTolerance: &dt.TypoTolerance{}})
	_, args, _, _ = BuildRelevanceSQL("kopi", Relevance{Table: "produk"}, tri)
	if !reflect.DeepEqual(args, []any{"%kopi%", "%kop%", "%opi%"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestRelevanceJoinsSQL(t *testing.T) {
	cfg := Relevance{
		Table: "orders",
		Joins: []RelevanceJoin{
			{Table: "customers", Alias: "c", On: []string{"c.id = orders.customer_id"}},
			{Table: "public.regions", On: []string{"regions.id = c.region_id"}, Type: "inner"},
		},
	}
	s := dt.NormalizeSearch(&dt.SearchConfig{Columns: []dt.SearchColumn{
		dt.Col("nomor").W(2), dt.Col("customer.name").As("c.name").W(3), dt.Col("regions.name"),
	}})
	sql, args, _, err := BuildRelevanceSQL("budi", cfg, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`FROM "orders" LEFT JOIN "customers" AS "c" ON "c"."id" = "orders"."customer_id" ` +
			`INNER JOIN "public"."regions" AS "regions" ON "regions"."id" = "c"."region_id"`,
		`LOWER(CAST("orders"."nomor" AS TEXT))`,
		`LOWER(CAST("c"."name" AS TEXT))`,
		`LOWER(CAST("regions"."name" AS TEXT))`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("sql tidak memuat %q:\n%s", want, sql)
		}
	}
	if !strings.HasPrefix(sql, `SELECT id, MAX(score) AS score FROM (SELECT "orders"."id" AS id, `) ||
		!strings.HasSuffix(sql, `) AS mrc_scored GROUP BY id ORDER BY score DESC LIMIT 2000`) {
		t.Fatalf("sql = %s", sql)
	}
	if !reflect.DeepEqual(args, []any{"%budi%", "%budi%", "%budi%"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestRelevanceJoinValidation(t *testing.T) {
	s := dt.NormalizeSearch(&dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nomor")}})
	bad := map[string]RelevanceJoin{
		"on SQL bebas":               {Table: "customers", On: []string{"c.id = orders.customer_id OR 1=1"}},
		"on tanpa kualifikasi":       {Table: "customers", On: []string{"id = customer_id"}},
		"on alias tak dikenal":       {Table: "customers", Alias: "c", On: []string{"x.id = orders.customer_id"}},
		"tabel tidak valid":          {Table: "customers; DROP TABLE x", On: []string{"customers.id = orders.customer_id"}},
		"alias tidak valid":          {Table: "customers", Alias: `c"`, On: []string{"c.id = orders.customer_id"}},
		"alias duplikat tabel dasar": {Table: "orders", On: []string{"orders.id = orders.id"}},
		"tipe join tidak valid":      {Table: "customers", Type: "cross", On: []string{"customers.id = orders.customer_id"}},
		"tanpa on":                   {Table: "customers"},
	}
	for name, j := range bad {
		if err := ValidateRelevance(Relevance{Table: "orders", Joins: []RelevanceJoin{j}}, s); !isConfigErr(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	joins := []RelevanceJoin{{Table: "customers", Alias: "c", On: []string{"c.id = orders.customer_id"}}}
	for _, col := range []string{"x.name", "c.addr.city"} {
		s := dt.NormalizeSearch(&dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col(col)}})
		if err := ValidateRelevance(Relevance{Table: "orders", Joins: joins}, s); !isConfigErr(err) {
			t.Errorf("%s: err = %v", col, err)
		}
	}
	ok := dt.NormalizeSearch(&dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nomor"), dt.Col("c.name")}})
	if err := ValidateRelevance(Relevance{Table: "orders", Joins: joins}, ok); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRelevance(Relevance{Table: "orders"}, dt.NormalizeSearch(&dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("kategori.nama")}})); !isConfigErr(err) {
		t.Fatalf("kolom relasi tanpa joins harus ditolak, err = %v", err)
	}
	if _, err := QuoteIdent("a.b"); err != nil {
		t.Fatal(err)
	}
}
