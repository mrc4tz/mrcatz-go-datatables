package gormtable

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Test PostgreSQL berjalan hanya bila MRCATZ_TEST_POSTGRES_DSN diisi, mis.
//
//	docker run -d --rm --name mrcatz-go-pg -e POSTGRES_PASSWORD=pg -p 54399:5432 postgres:17
//	MRCATZ_TEST_POSTGRES_DSN="host=localhost port=54399 user=postgres password=pg dbname=postgres sslmode=disable" go test ./...

var pgSchemaSeq atomic.Int64

// openPG membuka koneksi ke schema baru yang terisolasi per test.
func openPG(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MRCATZ_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MRCATZ_TEST_POSTGRES_DSN tidak diisi")
	}
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), cfg)
	if err != nil {
		t.Fatal(err)
	}
	schemaName := fmt.Sprintf("mrc_test_%d_%d", time.Now().UnixNano()%1e9, pgSchemaSeq.Add(1))
	if err := admin.Exec("CREATE SCHEMA " + schemaName).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm").Error; err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schemaName+",public"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			s.Close()
		}
		admin.Exec("DROP SCHEMA " + schemaName + " CASCADE")
		if s, err := admin.DB(); err == nil {
			s.Close()
		}
	})
	return db
}

func TestPostgresEngine(t *testing.T) {
	db := openPG(t)
	seed(t, db)
	rec := record(db)
	def := baseProduk(db)
	def.Search.Columns = append(def.Search.Columns, dt.Col("stok").Numeric())
	tb := mustTable(t, def)

	page, err := tb.Query(ctx, vals("search", "MINUM", "sort", "nama:asc"))
	if err != nil {
		t.Fatal(err)
	}
	eqIDs(t, produkIDs(page.Data), 1, 5, 2)
	if s := strings.Join(rec.all(), "\n"); !strings.Contains(s, `"produks"."nama" ILIKE '%MINUM%' ESCAPE '!'`) ||
		!strings.Contains(s, `"Kategori"."nama" ILIKE`) {
		t.Fatalf("SQL:\n%s", s)
	}
	page, _ = tb.Query(ctx, vals("search", "42"))
	eqIDs(t, produkIDs(page.Data), 6)
	page, _ = tb.Query(ctx, vals("search", "50%_"))
	eqIDs(t, produkIDs(page.Data), 5)

	// filter tanggal (timestamptz) dengan offset +08:00, multi exclude
	page, _ = tb.Query(ctx, vals("f.dibuat.from", "2026-01-01", "f.dibuat.to", "2026-02-10", "sort", "nama:asc"))
	eqIDs(t, produkIDs(page.Data), 1, 3)
	page, _ = tb.Query(ctx, vals("f.kat", "1", "f.kat.not", "1"))
	eqIDs(t, produkIDs(page.Data), 6, 4, 3)

	ft := mustTable(t, Definition[Produk]{DB: db, UTCOffset: "+08:00", Filters: []dt.Filter{
		dt.Date("tgl", "Tgl", dt.Field("createdAt")),
		dt.Date("bukan", "Bukan", dt.Field("createdAt"), dt.Op(dt.OpNe)),
		dt.Select("stok", "Stok", dt.Field("stok"), dt.Op(dt.OpGte), dt.Options(dt.Opt(1, "1"))),
		dt.Select("aktif", "Aktif", dt.Field("aktif"), dt.Options(dt.Opt(true, "Ya"))),
	}})
	page, _ = ft.Query(ctx, vals("f.tgl", "2026-10-19"))
	eqIDs(t, produkIDs(page.Data), 5, 6)
	page, _ = ft.Query(ctx, vals("f.bukan", "2026-10-19", "f.stok", "10", "f.aktif", "true"))
	eqIDs(t, produkIDs(page.Data), 1, 4)

	// Scope sebagai subquery untuk aksi massal
	sub, err := tb.Scope(ctx, vals("search", "minum"))
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := db.Model(&Produk{}).Where("id IN (?)", sub.Select("produks.id")).Count(&n).Error; err != nil || n != 3 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
}

func relevanceTable(t *testing.T, db *gorm.DB, typo *dt.TypoTolerance) *Table[Produk] {
	return mustTable(t, Definition[Produk]{
		DB:        db,
		Search:    &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama").W(5), dt.Col("sku").W(1)}, TypoTolerance: typo},
		Relevance: &Relevance{},
		Sortable:  []string{"nama"},
		Filters:   []dt.Filter{dt.Select("status", "Status", dt.Field("status"))},
	})
}

func TestPostgresRelevance(t *testing.T) {
	db := openPG(t)
	seed(t, db)
	// baris tambahan: "kopi" hanya di SKU (skor rendah), "Kopi" persis di nama
	mustCreate(t, db, &[]Produk{
		{ID: 7, Nama: "Gula Aren", SKU: "KOPI-GULA", Status: "aktif", KategoriID: 2, CreatedAt: utc("2026-01-01T00:00:00Z")},
		{ID: 8, Nama: "Kopi", SKU: "KP-008", Status: "aktif", KategoriID: 2, CreatedAt: utc("2026-01-01T00:00:00Z")},
	})
	rec := record(db)
	tb := relevanceTable(t, db, nil)

	page, err := tb.Query(ctx, vals("search", "kopi"))
	if err != nil {
		t.Fatal(err)
	}
	// skor: 1, 5, 8 = 5 (nama); 7 = 1 (hanya SKU) → 7 terakhir
	got := produkIDs(page.Data)
	if len(got) != 4 || got[3] != 7 {
		t.Fatalf("ids = %v (7 harus terakhir)", got)
	}
	if page.Meta != dt.NewMeta(4, 1, 10) {
		t.Fatalf("meta = %+v", page.Meta)
	}
	s := strings.Join(rec.all(), "\n")
	if !strings.Contains(s, `SELECT "id" AS id, (CASE WHEN LOWER(CAST("nama" AS TEXT)) LIKE '%kopi%'`) {
		t.Fatalf("SQL:\n%s", s)
	}

	// filter sisa di SQL + pagination atas id terurut
	page, _ = tb.Query(ctx, vals("search", "kopi", "f.status", "aktif", "limit", "2", "page", "2"))
	if page.Meta.Total != 4 || len(page.Data) != 2 || page.Data[1].ID != 7 {
		t.Fatalf("page = %+v", page)
	}
	page, _ = tb.Query(ctx, vals("search", "kopi", "f.status", "arsip"))
	if page.Meta.Total != 0 || len(page.Data) != 0 {
		t.Fatalf("page = %+v", page)
	}

	// kata kunci di dua kolom → skor lebih tinggi
	mustCreate(t, db, &Produk{ID: 9, Nama: "Kopi Spesial", SKU: "KOPI-9", Status: "aktif", KategoriID: 2, CreatedAt: utc("2026-01-01T00:00:00Z")})
	page, _ = tb.Query(ctx, vals("search", "kopi"))
	if page.Data[0].ID != 9 {
		t.Fatalf("ids = %v (9 harus pertama)", produkIDs(page.Data))
	}

	// sort pengguna → jalur ILIKE biasa
	rec.reset()
	page, _ = tb.Query(ctx, vals("search", "kopi", "sort", "nama:asc"))
	eqIDs(t, produkIDs(page.Data), 7, 8, 1, 9, 5)
	if s := strings.Join(rec.all(), "\n"); strings.Contains(s, "score") || !strings.Contains(s, "ILIKE") {
		t.Fatalf("SQL:\n%s", s)
	}

	// match all lintas kolom; tidak ada hasil
	page, _ = tb.Query(ctx, vals("search", "kopi gula"))
	eqIDs(t, produkIDs(page.Data), 7)
	page, _ = tb.Query(ctx, vals("search", "zzz"))
	if len(page.Data) != 0 || page.Meta.Total != 0 {
		t.Fatalf("page = %+v", page)
	}

	rows, err := tb.ExportRows(ctx, vals("search", "kopi"))
	if err != nil || len(rows) != 5 || rows[0].ID != 9 {
		t.Fatalf("rows = %v, err = %v", produkIDs(rows), err)
	}
}

func TestPostgresTypoTolerance(t *testing.T) {
	db := openPG(t)
	seed(t, db)
	mustCreate(t, db, &Produk{ID: 8, Nama: "Kopi", SKU: "X-8", Status: "aktif", KategoriID: 2, CreatedAt: utc("2026-01-01T00:00:00Z")})

	// pg_trgm: similarity("kopi","kopy") ≈ 0.43 ≥ 0.3
	pg := relevanceTable(t, db, &dt.TypoTolerance{Driver: dt.TypoPgTrgm})
	page, err := pg.Query(ctx, vals("search", "kopy"))
	if err != nil {
		t.Fatal(err)
	}
	eqIDs(t, produkIDs(page.Data), 8)
	if none, _ := relevanceTable(t, db, nil).Query(ctx, vals("search", "kopy")); len(none.Data) != 0 {
		t.Fatalf("tanpa typo harus kosong: %v", produkIDs(none.Data))
	}

	// trigram: "kop" cocok → semua kopi; kecocokan persis (×100) di atas
	tri := relevanceTable(t, db, &dt.TypoTolerance{})
	page, _ = tri.Query(ctx, vals("search", "kopi"))
	if len(page.Data) != 3 {
		t.Fatalf("ids = %v", produkIDs(page.Data))
	}
	page, _ = tri.Query(ctx, vals("search", "kopy"))
	got := produkIDs(page.Data)
	if len(got) != 3 {
		t.Fatalf("ids = %v", got)
	}
}

// --- relevansi dengan joins ---

type Customer struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type Order struct {
	ID         uint        `json:"id"`
	Nomor      string      `json:"nomor"`
	CustomerID uint        `json:"customerId"`
	Customer   *Customer   `json:"customer,omitempty"`
	Items      []OrderItem `json:"items,omitempty"`
}

type OrderItem struct {
	ID      uint   `json:"id"`
	OrderID uint   `json:"orderId"`
	Produk  string `json:"produk"`
}

func TestPostgresRelevanceJoins(t *testing.T) {
	db := openPG(t)
	if err := db.AutoMigrate(&Customer{}, &Order{}, &OrderItem{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&[]Customer{{ID: 1, Name: "Budi Santoso"}, {ID: 2, Name: "Siti"}})
	db.Create(&[]Order{
		{ID: 1, Nomor: "INV-001", CustomerID: 2},
		{ID: 2, Nomor: "INV-BUDI", CustomerID: 1},
		{ID: 3, Nomor: "INV-003", CustomerID: 1},
		{ID: 4, Nomor: "INV-004", CustomerID: 2},
	})
	db.Create(&[]OrderItem{
		{OrderID: 1, Produk: "Kopi Budi"}, {OrderID: 1, Produk: "Kopi Budi Besar"}, {OrderID: 1, Produk: "Teh"},
		{OrderID: 4, Produk: "Roti"},
	})
	rec := record(db)
	tb := mustTable(t, Definition[Order]{
		DB:    db,
		Joins: []string{"Customer"},
		Search: &dt.SearchConfig{Columns: []dt.SearchColumn{
			dt.Col("nomor").W(10),
			dt.Col("customer.name").As("c.name").W(3),
			dt.Col("items.produk").As("i.produk").W(1),
		}},
		Relevance: &Relevance{Joins: []RelevanceJoin{
			{Table: "customers", Alias: "c", On: []string{"c.id = orders.customer_id"}},
			{Table: "order_items", Alias: "i", On: []string{"i.order_id = orders.id"}},
		}},
		Sortable: []string{"nomor"},
	})
	page, err := tb.Query(ctx, vals("search", "budi"))
	if err != nil {
		t.Fatal(err)
	}
	// 2 = nomor (10) + customer (3); 3 = customer (3); 1 = item (MAX per id = 1)
	eqIDs(t, ids(page.Data, func(o Order) uint { return o.ID }), 2, 3, 1)
	if page.Meta.Total != 3 || page.Data[0].Customer == nil || page.Data[0].Customer.Name != "Budi Santoso" {
		t.Fatalf("page = %+v", page)
	}
	s := strings.Join(rec.all(), "\n")
	for _, want := range []string{
		`SELECT id, MAX(score) AS score FROM (SELECT "orders"."id" AS id`,
		`FROM "orders" LEFT JOIN "customers" AS "c" ON "c"."id" = "orders"."customer_id" LEFT JOIN "order_items" AS "i" ON "i"."order_id" = "orders"."id"`,
		`LOWER(CAST("c"."name" AS TEXT))`,
		`GROUP BY id ORDER BY score DESC LIMIT 2000`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SQL tidak memuat %q:\n%s", want, s)
		}
	}

	// sort pengguna → jalur ILIKE: kolom khusus relevansi (items) dilewati
	page, _ = tb.Query(ctx, vals("search", "budi", "sort", "nomor:desc"))
	eqIDs(t, ids(page.Data, func(o Order) uint { return o.ID }), 2, 3)

	// tanpa Relevance, relasi has-many tidak bisa dipetakan
	_, err = Define(Definition[Order]{DB: db, Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("items.produk").As("i.produk")}}})
	if !isConfigErr(err) {
		t.Fatalf("err = %v", err)
	}
}

func mustCreate(t *testing.T, db *gorm.DB, v any) {
	t.Helper()
	if err := db.Create(v).Error; err != nil {
		t.Fatal(err)
	}
}
