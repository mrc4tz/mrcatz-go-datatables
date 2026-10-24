package gormtable

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"gorm.io/gorm"
)

type instansiKey struct{}

func baseProduk(db *gorm.DB) Definition[Produk] {
	return Definition[Produk]{
		DB:    db,
		Joins: []string{"Kategori"},
		BaseWhere: func(ctx context.Context, db *gorm.DB) *gorm.DB {
			if id, ok := ctx.Value(instansiKey{}).(int); ok {
				return db.Where("produks.instansi_id = ?", id)
			}
			return db
		},
		Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama"), dt.Col("kategori.nama")}},
		Filters: []dt.Filter{
			dt.Select("status", "Status", dt.Field("status"), dt.Options(dt.Opt("aktif", "Aktif"))),
			dt.Multi("kat", "Kategori", dt.Field("kategoriId"), dt.Options(dt.Opt(1, "A")), dt.AllowExclude()),
			dt.DateRange("dibuat", "Dibuat", dt.Field("createdAt")),
		},
		Sortable:    []string{"nama", "kategori.nama", "createdAt"},
		DefaultSort: []dt.SortRule{{Key: "createdAt", Dir: dt.Desc}},
		UTCOffset:   "+08:00",
	}
}

func TestQueryFilterSearchSortPagination(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	rec := record(db)
	tb := mustTable(t, baseProduk(db))
	c := context.WithValue(ctx, instansiKey{}, 7)

	page, err := tb.Query(c, vals(
		"search", "kopi", "page", "2", "limit", "1", "sort", "nama:desc",
		"f.status", "aktif", "f.kat", "1", "f.dibuat.from", "2026-01-01",
	))
	if err != nil {
		t.Fatal(err)
	}
	// kandidat: 1 (Kopi Budi) & 5 (Kopi Susu) → nama desc → [5, 1] → halaman 2 = [1]
	eqIDs(t, produkIDs(page.Data), 1)
	if page.Meta != dt.NewMeta(2, 2, 1) {
		t.Fatalf("meta = %+v", page.Meta)
	}
	if page.Data[0].Kategori == nil || page.Data[0].Kategori.Nama != "Minuman" {
		t.Fatalf("join kategori tidak termuat: %+v", page.Data[0])
	}
	sqls := strings.Join(rec.all(), "\n")
	for _, want := range []string{
		"produks.instansi_id = 7",
		"`produks`.`status` = \"aktif\"",
		"`produks`.`kategori_id` = 1",
		"`produks`.`created_at` >= \"2025-12-31 16:00:00",
		"LOWER(`produks`.`nama`) LIKE \"%kopi%\" ESCAPE '!'",
		"LOWER(`Kategori`.`nama`) LIKE \"%kopi%\" ESCAPE '!'",
		"ORDER BY `produks`.`nama` DESC,`produks`.`id` LIMIT 1 OFFSET 1",
	} {
		if !strings.Contains(sqls, want) {
			t.Errorf("SQL tidak memuat %q:\n%s", want, sqls)
		}
	}

	// mode Kecuali (NOT IN) + base where
	page, err = tb.Query(c, vals("f.kat", "1", "f.kat.not", "1"))
	if err != nil {
		t.Fatal(err)
	}
	// instansi 7, kategori ≠ 1 → 3, 6; default sort createdAt desc → [6, 3]
	eqIDs(t, produkIDs(page.Data), 6, 3)

	// tanpa konteks instansi → semua baris, default sort createdAt desc
	page, _ = tb.Query(ctx, nil)
	eqIDs(t, produkIDs(page.Data), 6, 5, 4, 3, 1, 2)
	if page.Meta != dt.NewMeta(6, 1, 10) {
		t.Fatalf("meta = %+v", page.Meta)
	}
}

func TestSearchRelationCaseInsensitive(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	tb := mustTable(t, baseProduk(db))
	page, err := tb.Query(ctx, vals("search", "MINUM", "sort", "nama:asc"))
	if err != nil {
		t.Fatal(err)
	}
	eqIDs(t, produkIDs(page.Data), 1, 5, 2)
	// sort lewat kolom relasi
	page, _ = tb.Query(ctx, vals("sort", "kategori.nama:asc,nama:asc"))
	eqIDs(t, produkIDs(page.Data), 6, 4, 3, 1, 5, 2)
}

func TestSearchModesMatchTypoEscape(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	def := func(cfg *dt.SearchConfig) *Table[Produk] {
		return mustTable(t, Definition[Produk]{DB: db, Search: cfg})
	}
	cases := []struct {
		name   string
		cfg    *dt.SearchConfig
		search string
		want   []uint
	}{
		{"match all", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}}, "kopi budi", []uint{1}},
		{"match any", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}, Match: dt.MatchAny}, "kopi budi", []uint{1, 3, 5}},
		{"tanpa typo", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}}, "kopy", nil},
		{"typo trigram", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}, TypoTolerance: &dt.TypoTolerance{}}, "kopy", []uint{1, 5}},
		{"typo kata pendek", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}, TypoTolerance: &dt.TypoTolerance{MinWordLength: 5}}, "kopy", nil},
		{"escape wildcard", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}}, "50%_", []uint{5}},
		{"escape persen", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}}, "%", []uint{5}},
		{"escape tanda seru", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}}, "!", nil},
		{"startsWith", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("sku").StartsWith()}}, "kp", []uint{1, 5}},
		{"startsWith bukan awal", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("sku").StartsWith()}}, "001", nil},
		{"equals", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("sku").Equals()}}, "kp-001", []uint{1}},
		{"numeric cocok", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama"), dt.Col("stok").Numeric()}}, "42", []uint{6}},
		{"numeric + teks", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama"), dt.Col("stok").Numeric()}}, "2", []uint{4}},
		{"hanya numeric, kata teks", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("stok").Numeric()}}, "abc", nil},
		{"spasi saja", &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}}, "   ", []uint{1, 2, 3, 4, 5, 6}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page, err := def(c.cfg).Query(ctx, vals("search", c.search))
			if err != nil {
				t.Fatal(err)
			}
			eqIDs(t, produkIDs(page.Data), c.want...)
			if page.Meta.Total != int64(len(c.want)) {
				t.Fatalf("total = %d", page.Meta.Total)
			}
		})
	}
}

func TestCaseSensitiveOption(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	rec := record(db)
	off := false
	tb := mustTable(t, Definition[Produk]{DB: db, CaseInsensitive: &off,
		Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama"), dt.Col("sku").Equals()}}})
	if _, err := tb.Query(ctx, vals("search", "Kopi")); err != nil {
		t.Fatal(err)
	}
	s := strings.Join(rec.all(), "\n")
	if strings.Contains(s, "LOWER(") || !strings.Contains(s, "`produks`.`nama` LIKE \"%Kopi%\"") || !strings.Contains(s, "`produks`.`sku` = \"Kopi\"") {
		t.Fatalf("SQL:\n%s", s)
	}
}

func TestSortWhitelistAndOrder(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	rec := record(db)
	tb := mustTable(t, Definition[Produk]{DB: db, Sortable: []string{"nama"}})
	_, err := tb.Query(ctx, vals("sort", "password:asc"))
	var qe *dt.QueryError
	if !errors.As(err, &qe) || qe.StatusCode() != 400 {
		t.Fatalf("err = %v", err)
	}
	if _, err := tb.Query(ctx, vals("sort", "stok:asc")); !isQueryErr(err) {
		t.Fatalf("kunci di luar whitelist harus 400, err = %v", err)
	}
	if len(rec.all()) != 0 {
		t.Fatalf("database tidak boleh disentuh: %v", rec.all())
	}

	// tanpa whitelist: kunci yang bisa dipetakan boleh, sisanya 400
	open := mustTable(t, Definition[Produk]{DB: db})
	page, err := open.Query(ctx, vals("sort", "stok:desc"))
	if err != nil {
		t.Fatal(err)
	}
	eqIDs(t, produkIDs(page.Data), 4, 6, 1, 3, 5, 2)
	for _, bad := range []string{"tidakAda:asc", "kategori.nama:asc", "a;b:asc"} {
		if _, err := open.Query(ctx, vals("sort", bad)); !isQueryErr(err) {
			t.Errorf("%s: err = %v", bad, err)
		}
	}

	// default sort + tiebreaker; tiebreaker tidak diulang bila sudah dipakai
	rec.reset()
	ds := mustTable(t, Definition[Produk]{DB: db, Sortable: []string{"status", "id"},
		DefaultSort: []dt.SortRule{{Key: "status", Dir: dt.Asc}}})
	page, _ = ds.Query(ctx, nil)
	eqIDs(t, produkIDs(page.Data), 1, 2, 4, 5, 3, 6)
	page, _ = ds.Query(ctx, vals("sort", "id:desc"))
	eqIDs(t, produkIDs(page.Data), 6, 5, 4, 3, 2, 1)
	s := strings.Join(rec.all(), "\n")
	if !strings.Contains(s, "ORDER BY `produks`.`status`,`produks`.`id` LIMIT") ||
		!strings.Contains(s, "ORDER BY `produks`.`id` DESC LIMIT") {
		t.Fatalf("SQL:\n%s", s)
	}

	// tiebreaker kustom & dimatikan
	rec.reset()
	nt := mustTable(t, Definition[Produk]{DB: db, NoTiebreaker: true})
	_, _ = nt.Query(ctx, nil)
	if s := strings.Join(rec.all(), "\n"); strings.Contains(s, "ORDER BY") {
		t.Fatalf("NoTiebreaker masih ORDER BY:\n%s", s)
	}
	ct := mustTable(t, Definition[Produk]{DB: db, Tiebreaker: []dt.SortRule{{Key: "sku", Dir: dt.Desc}}})
	page, _ = ct.Query(ctx, nil)
	eqIDs(t, produkIDs(page.Data), 2, 3, 4, 5, 1, 6)
}

func TestPaginationLimitsExport(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	tb := mustTable(t, Definition[Produk]{DB: db, DefaultLimit: 4, MaxLimit: 5, ExportLimit: 3})

	page, _ := tb.Query(ctx, nil)
	if len(page.Data) != 4 || page.Meta != (dt.PageMeta{Total: 6, Page: 1, Limit: 4, TotalPages: 2, HasMore: true}) {
		t.Fatalf("meta = %+v", page.Meta)
	}
	page, _ = tb.Query(ctx, vals("limit", "1000", "page", "0"))
	if page.Meta.Limit != 5 || page.Meta.Page != 1 || len(page.Data) != 5 {
		t.Fatalf("meta = %+v", page.Meta)
	}
	page, _ = tb.Query(ctx, vals("limit", "2", "page", "3"))
	eqIDs(t, produkIDs(page.Data), 5, 6)
	if page.Meta.HasMore || page.Meta.TotalPages != 3 {
		t.Fatalf("meta = %+v", page.Meta)
	}
	// halaman di luar jangkauan: data kosong ([] bukan null)
	page, _ = tb.Query(ctx, vals("page", "9"))
	b, _ := json.Marshal(page)
	if !strings.HasPrefix(string(b), `{"data":[],"meta":{"total":6,"page":9,"limit":4,"totalPages":2,"hasMore":false}}`) {
		t.Fatalf("json = %s", b)
	}

	rows, err := tb.ExportRows(ctx, vals("limit", "1", "page", "2"))
	if err != nil {
		t.Fatal(err)
	}
	eqIDs(t, produkIDs(rows), 1, 2, 3)

	// default limit bawaan 10 / 100 / 10000
	d := mustTable(t, Definition[Produk]{DB: db})
	page, _ = d.Query(ctx, vals("limit", "500"))
	if page.Meta.Limit != 100 {
		t.Fatalf("limit = %d", page.Meta.Limit)
	}
	rows, _ = d.ExportRows(ctx, vals("search", ""))
	if len(rows) != 6 {
		t.Fatalf("export = %d", len(rows))
	}

	// QueryParsed menormalkan page/limit
	page, _ = tb.QueryParsed(ctx, dt.TableQuery{Page: -1, Limit: 99})
	if page.Meta.Page != 1 || page.Meta.Limit != 5 {
		t.Fatalf("meta = %+v", page.Meta)
	}
}

func TestPrefix(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	tb := mustTable(t, Definition[Produk]{DB: db, Prefix: "p_",
		Search:  &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}},
		Filters: []dt.Filter{dt.Select("status", "Status", dt.Field("status"))}})
	page, err := tb.Query(ctx, vals("p_search", "kopi", "search", "teh", "p_f.status", "aktif", "p_limit", "1"))
	if err != nil {
		t.Fatal(err)
	}
	eqIDs(t, produkIDs(page.Data), 1)
	if page.Meta.Total != 2 {
		t.Fatalf("meta = %+v", page.Meta)
	}
}

func TestFilters(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	tb := mustTable(t, Definition[Produk]{
		DB:        db,
		UTCOffset: "+08:00",
		Filters: []dt.Filter{
			dt.Select("status", "Status", dt.Field("status"), dt.Op(dt.OpNe)),
			dt.Select("stok", "Stok", dt.Field("stok"), dt.Op(dt.OpGte), dt.Options(dt.Opt(1, "1"))),
			dt.Select("nama", "Nama", dt.Field("nama"), dt.Op(dt.OpContains)),
			dt.Select("aktif", "Aktif", dt.Field("aktif"), dt.Options(dt.Opt(true, "Ya"), dt.Opt(false, "Tidak"))),
			dt.Multi("kat", "Kategori", dt.Field("kategoriId"), dt.Options(dt.Opt(1, "A"))),
			dt.Date("tgl", "Tanggal", dt.Field("createdAt")),
			dt.Date("bukan", "Bukan tanggal", dt.Field("createdAt"), dt.Op(dt.OpNe)),
			dt.Date("bulan", "Bulan", dt.Field("createdAt"), dt.Format(dt.FormatMonth), dt.Op(dt.OpGte)),
			dt.DateRange("rentang", "Rentang", dt.Field("createdAt")),
		},
	})
	cases := []struct {
		name string
		q    []string
		want []uint
	}{
		{"select !=", []string{"f.status", "aktif"}, []uint{3, 6}},
		{"select >= angka", []string{"f.stok", "10"}, []uint{1, 4, 6}},
		{"select contains", []string{"f.nama", "KOPI"}, []uint{1, 5}},
		{"select boolean true", []string{"f.aktif", "true"}, []uint{1, 3, 4, 5}},
		{"select boolean false", []string{"f.aktif", "false"}, []uint{2, 6}},
		{"multi IN", []string{"f.kat", "1", "f.kat", "3"}, []uint{1, 2, 4, 5, 6}},
		{"multi exclude diabaikan tanpa AllowExclude", []string{"f.kat", "1", "f.kat.not", "1"}, []uint{1, 2, 5}},
		// 2026-10-19 WITA = [2026-10-18T16:00Z, 2026-10-19T16:00Z)
		{"date =", []string{"f.tgl", "2026-10-19"}, []uint{5, 6}},
		{"date !=", []string{"f.bukan", "2026-10-19"}, []uint{1, 2, 3, 4}},
		{"month >=", []string{"f.bulan", "2026-03"}, []uint{4, 5, 6}},
		{"range from", []string{"f.rentang.from", "2026-01-01"}, []uint{1, 3, 4, 5, 6}},
		{"range to", []string{"f.rentang.to", "2026-01-01"}, []uint{1, 2}},
		{"range terbalik ditukar", []string{"f.rentang.from", "2026-02-28", "f.rentang.to", "2026-01-01"}, []uint{1, 3}},
		{"nilai kosong diabaikan", []string{"f.status", "", "f.tgl", ""}, []uint{1, 2, 3, 4, 5, 6}},
		{"filter tak dikenal diabaikan", []string{"f.lain", "x"}, []uint{1, 2, 3, 4, 5, 6}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page, err := tb.Query(ctx, vals(c.q...))
			if err != nil {
				t.Fatal(err)
			}
			eqIDs(t, produkIDs(page.Data), c.want...)
		})
	}

	for _, bad := range [][]string{{"f.tgl", "kemarin"}, {"f.rentang.from", "2026-13"}, {"f.bulan", "2026-13"}} {
		_, err := tb.Query(ctx, vals(bad...))
		if !isQueryErr(err) || !errors.Is(err, dt.ErrInvalidDate) {
			t.Errorf("%v: err = %v", bad, err)
		}
	}
}

func TestCustomWhereAndTimeFormat(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	var seenFC dt.FilterContext
	tb := mustTable(t, Definition[Produk]{
		DB: db,
		Filters: []dt.Filter{
			dt.Select("punyaStok", "Punya stok", dt.Options(dt.Opt(true, "Ya"), dt.Opt(false, "Tidak")),
				Where(func(db *gorm.DB, v dt.Resolved, fc dt.FilterContext) *gorm.DB {
					seenFC = fc
					if v.Scalar == true {
						return db.Where("stok > 0")
					}
					return db.Where("stok = 0")
				})),
			// format jam lewat Where: kolom string "HH:MM:SS"
			dt.Date("jam", "Jam buka", dt.Format(dt.FormatTimeHm), dt.Op(dt.OpGte),
				dt.WithWhere(func(db *gorm.DB, v dt.Resolved, fc dt.FilterContext) *gorm.DB {
					p, _ := dt.PeriodOf(v.Scalar.(string), dt.FormatTimeHm, "")
					return db.Where("jam_buka >= ?", p.Start.Format("15:04:05"))
				})),
		},
	})
	page, err := tb.Query(ctx, vals("f.punyaStok", "true"))
	if err != nil {
		t.Fatal(err)
	}
	eqIDs(t, produkIDs(page.Data), 1, 3, 4, 5, 6)
	if seenFC.String("punyaStok") != "true" || seenFC.Ctx == nil {
		t.Fatalf("FilterContext = %+v", seenFC)
	}
	page, _ = tb.Query(ctx, vals("f.punyaStok", "false"))
	eqIDs(t, produkIDs(page.Data), 2)
	page, _ = tb.Query(ctx, vals("f.jam", "08:30"))
	eqIDs(t, produkIDs(page.Data), 2, 4, 5, 6)

	// format jam berbasis field → ConfigError saat Define
	for _, f := range []dt.Filter{
		dt.Date("jam", "Jam", dt.Field("createdAt"), dt.Format(dt.FormatTimeHm)),
		dt.DateRange("jam", "Jam", dt.Field("createdAt"), dt.Format(dt.FormatTime)),
	} {
		_, err := Define(Definition[Produk]{DB: db, Filters: []dt.Filter{f}})
		if !isConfigErr(err) || !strings.Contains(err.Error(), "Where") {
			t.Errorf("err = %v", err)
		}
	}
}

func TestDynamicDateOperatorAndDescribe(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	tb := mustTable(t, Definition[Produk]{
		DB:        db,
		UTCOffset: "Z",
		Filters: []dt.Filter{
			dt.Select("arah", "Arah", dt.Options(dt.Opt("sesudah", "Sesudah")),
				Where(func(db *gorm.DB, _ dt.Resolved, _ dt.FilterContext) *gorm.DB { return db })),
			dt.Date("tgl", "Tgl", dt.Field("createdAt"), dt.DependsOn("arah"),
				dt.OpFunc(func(fc dt.FilterContext) dt.Operator {
					if fc.String("arah") == "sesudah" {
						return dt.OpGt
					}
					return dt.OpLt
				}),
				dt.MinFunc(func(fc dt.FilterContext) string {
					if fc.String("arah") == "sesudah" {
						return "2026-01-01"
					}
					return ""
				}),
				dt.Max("2026-12-31")),
			dt.Select("kec", "Kecamatan", dt.Field("kategoriId"), dt.DependsOn("arah"),
				dt.VisibleWhen(func(fc dt.FilterContext) bool { return fc.String("arah") != "" }),
				dt.OptionsFrom(func(fc dt.FilterContext) ([]dt.Option, error) {
					if fc.Ctx == nil {
						return nil, errors.New("ctx kosong")
					}
					return []dt.Option{dt.Opt(1, "Murung Pudak")}, nil
				})),
		},
	})
	page, _ := tb.Query(ctx, vals("f.tgl", "2026-02-10", "f.arah", "sesudah"))
	eqIDs(t, produkIDs(page.Data), 4, 5, 6)
	page, _ = tb.Query(ctx, vals("f.tgl", "2026-01-01"))
	eqIDs(t, produkIDs(page.Data), 2)

	s, err := tb.Describe(ctx, vals("f.arah", "sesudah"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Filters[1].Min != "2026-01-01" || s.Filters[1].Max != "2026-12-31" || !s.Filters[2].Visible ||
		len(s.Filters[2].Options) != 1 || s.Filters[2].Options[0].Label != "Murung Pudak" {
		t.Fatalf("schema = %+v", s)
	}
	s, _ = tb.Describe(ctx, nil)
	if s.Filters[1].Min != "" || s.Filters[2].Visible || len(s.Filters[2].Options) != 0 {
		t.Fatalf("schema = %+v", s)
	}
	b, _ := json.Marshal(s)
	want := `{"filters":[{"id":"arah","label":"Arah","type":"select","visible":true,"options":[{"value":"sesudah","label":"Sesudah"}]},` +
		`{"id":"tgl","label":"Tgl","type":"date","visible":true,"dependsOn":["arah"],"format":"date","max":"2026-12-31"},` +
		`{"id":"kec","label":"Kecamatan","type":"select","visible":false,"dependsOn":["arah"],"options":[]}]}`
	if string(b) != want {
		t.Fatalf("json =\n%s\nwant\n%s", b, want)
	}
}

func TestScope(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	def := baseProduk(db)
	tb := mustTable(t, def)
	c := context.WithValue(ctx, instansiKey{}, 7)
	sc, err := tb.Scope(c, vals("f.status", "aktif", "search", "minum", "sort", "nama:asc", "limit", "1"))
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := sc.Count(&n).Error; err != nil || n != 3 {
		t.Fatalf("count = %d, err = %v", n, err)
	}
	// aksi massal lewat subquery (Joins tidak didukung UPDATE GORM)
	sub, _ := tb.Scope(c, vals("f.status", "aktif", "search", "minum"))
	if err := db.Model(&Produk{}).Where("id IN (?)", sub.Select("produks.id")).Update("status", "arsip").Error; err != nil {
		t.Fatal(err)
	}
	var arsip []Produk
	db.Where("status = ?", "arsip").Order("id").Find(&arsip)
	eqIDs(t, produkIDs(arsip), 1, 2, 3, 5)

	if _, err := tb.Scope(c, vals("f.dibuat.from", "x")); !isQueryErr(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestPreloadSelect(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	tb := mustTable(t, Definition[Produk]{DB: db, Preload: []string{"Kategori"}, Select: []string{"id", "nama", "kategori_id"}})
	page, err := tb.Query(ctx, vals("limit", "1"))
	if err != nil {
		t.Fatal(err)
	}
	p := page.Data[0]
	if p.Kategori == nil || p.Kategori.Nama != "Minuman" || p.SKU != "" || p.Nama != "Kopi Budi" {
		t.Fatalf("row = %+v", p)
	}
}

func TestConfigErrors(t *testing.T) {
	db := openSQLite(t)
	seed(t, db)
	sel := func(id, field string) dt.Filter { return dt.Select(id, id, dt.Field(field)) }
	cases := []struct {
		name string
		def  Definition[Produk]
		want string
	}{
		{"tanpa DB", Definition[Produk]{}, "DB"},
		{"id duplikat", Definition[Produk]{DB: db, Filters: []dt.Filter{sel("a", "nama"), sel("a", "sku")}}, "duplikat"},
		{"field tak dikenal", Definition[Produk]{DB: db, Filters: []dt.Filter{sel("a", "tidakAda")}}, "tidakAda"},
		{"dot-path tanpa join", Definition[Produk]{DB: db, Filters: []dt.Filter{sel("a", "kategori.nama")}}, "Joins"},
		{"join tak dikenal", Definition[Produk]{DB: db, Joins: []string{"Pemasok"}}, "Pemasok"},
		{"identifier jahat", Definition[Produk]{DB: db, Filters: []dt.Filter{sel("a", "nama; DROP")}}, "identifier"},
		{"hanya Match", Definition[Produk]{DB: db, Filters: []dt.Filter{dt.Select("a", "A", dt.MatchFunc(func(any, dt.Resolved) bool { return true }))}}, "Match"},
		{"Where salah tipe", Definition[Produk]{DB: db, Filters: []dt.Filter{dt.Select("a", "A", dt.WithWhere("x = 1"))}}, "WhereFunc"},
		{"kolom pencarian tak dikenal", Definition[Produk]{DB: db, Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("c.name")}}}, "Kolom pencarian"},
		{"search tidak valid", Definition[Produk]{DB: db, Search: &dt.SearchConfig{Match: "most"}}, "match"},
		{"sortable tak dikenal", Definition[Produk]{DB: db, Sortable: []string{"password"}}, "Sortable"},
		{"default sort tak dikenal", Definition[Produk]{DB: db, DefaultSort: []dt.SortRule{{Key: "x"}}}, "DefaultSort"},
		{"offset tidak valid", Definition[Produk]{DB: db, UTCOffset: "WITA"}, "Offset"},
		{"pg_trgm tanpa relevance", Definition[Produk]{DB: db, Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")},
			TypoTolerance: &dt.TypoTolerance{Driver: dt.TypoPgTrgm}}}, "pg_trgm"},
		{"relevance kolom relasi tanpa joins", Definition[Produk]{DB: db, Joins: []string{"Kategori"},
			Search:    &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("kategori.nama")}},
			Relevance: &Relevance{}}, "relasi"},
		{"relevance + driver", Definition[Produk]{DB: db, Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}},
			Relevance: &Relevance{}, Driver: &fakeDriver{name: "x"}}, "bersamaan"},
		{"primary key tak dikenal", Definition[Produk]{DB: db, PrimaryKey: "uuid"}, "PrimaryKey"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Define(c.def)
			if !isConfigErr(err) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v (want ConfigError berisi %q)", err, c.want)
			}
		})
	}

	// pg_trgm + relevance diterima; MustDefine panic dengan ConfigError
	if _, err := Define(Definition[Produk]{DB: db, Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")},
		TypoTolerance: &dt.TypoTolerance{Driver: dt.TypoPgTrgm}}, Relevance: &Relevance{}}); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if r := recover(); r == nil || !isConfigErr(r.(error)) {
				t.Fatalf("recover = %v", r)
			}
		}()
		MustDefine(Definition[Produk]{})
	}()
}

func TestConfigIntrospection(t *testing.T) {
	db := openSQLite(t)
	tb := mustTable(t, baseProduk(db))
	cfg := tb.Config()
	if cfg.PrimaryKey != "id" || cfg.Table != "produks" || len(cfg.Filters) != 3 || len(cfg.Sortable) != 3 ||
		cfg.Search.Columns[0].Weight != 1 || cfg.Driver != nil {
		t.Fatalf("config = %+v", cfg)
	}
	if mustTable(t, Definition[Produk]{DB: db}).Config().Sortable != nil {
		t.Fatal("tanpa whitelist harus nil")
	}
}

// Model tanpa tag json & tanpa primary key.
type Catatan struct {
	Judul string
	Isi   string `gorm:"column:isi_teks"`
}

func TestFieldNameFallbacks(t *testing.T) {
	db := openSQLite(t)
	if err := db.AutoMigrate(&Catatan{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&[]Catatan{{"b", "Halo"}, {"a", "Dunia"}})
	if _, err := Define(Definition[Catatan]{DB: db}); !isConfigErr(err) {
		t.Fatalf("tanpa primary key harus ConfigError, err = %v", err)
	}
	tb := mustTable(t, Definition[Catatan]{DB: db, NoTiebreaker: true,
		Search:      &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("isi_teks")}},
		DefaultSort: []dt.SortRule{{Key: "Judul"}}})
	page, err := tb.Query(ctx, vals("search", "A"))
	if err != nil || len(page.Data) != 2 || page.Data[0].Judul != "a" {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
}
