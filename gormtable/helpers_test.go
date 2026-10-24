package gormtable

import (
	"context"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Kategori struct {
	ID   uint   `json:"id"`
	Nama string `json:"nama"`
}

type Produk struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	Nama       string    `json:"nama"`
	SKU        string    `json:"sku" gorm:"column:sku"`
	Status     string    `json:"status"`
	Stok       int       `json:"stok"`
	KategoriID uint      `json:"kategoriId"`
	Kategori   *Kategori `json:"kategori,omitempty"`
	InstansiID uint      `json:"instansiId"`
	Aktif      bool      `json:"aktif"`
	JamBuka    string    `json:"jamBuka"` // "HH:MM:SS"
	CreatedAt  time.Time `json:"createdAt"`
}

var ctx = context.Background()

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

// sqlRecorder mencatat SQL query (SELECT) yang dijalankan.
type sqlRecorder struct {
	mu   sync.Mutex
	sqls []string
}

func (r *sqlRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.sqls...)
}

func (r *sqlRecorder) reset() {
	r.mu.Lock()
	r.sqls = nil
	r.mu.Unlock()
}

func record(db *gorm.DB) *sqlRecorder {
	r := &sqlRecorder{}
	cb := func(d *gorm.DB) {
		r.mu.Lock()
		r.sqls = append(r.sqls, d.Dialector.Explain(d.Statement.SQL.String(), d.Statement.Vars...))
		r.mu.Unlock()
	}
	_ = db.Callback().Query().After("gorm:query").Register("test:record", cb)
	_ = db.Callback().Row().After("gorm:row").Register("test:record", cb)
	return r
}

func openSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	return db
}

func seed(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&Kategori{}, &Produk{}); err != nil {
		t.Fatal(err)
	}
	kats := []Kategori{{ID: 1, Nama: "Minuman"}, {ID: 2, Nama: "Makanan"}, {ID: 3, Nama: "Alat Tulis"}}
	if err := db.Create(&kats).Error; err != nil {
		t.Fatal(err)
	}
	rows := []Produk{
		{ID: 1, Nama: "Kopi Budi", SKU: "KP-001", Status: "aktif", Stok: 10, KategoriID: 1, InstansiID: 7, Aktif: true, JamBuka: "08:00:00", CreatedAt: utc("2026-01-01T02:00:00Z")},
		{ID: 2, Nama: "Teh Manis", SKU: "TH-002", Status: "aktif", Stok: 0, KategoriID: 1, InstansiID: 7, Aktif: false, JamBuka: "09:30:00", CreatedAt: utc("2025-12-31T15:00:00Z")},
		{ID: 3, Nama: "Roti Budi Santoso", SKU: "RT-003", Status: "arsip", Stok: 5, KategoriID: 2, InstansiID: 7, Aktif: true, JamBuka: "07:15:00", CreatedAt: utc("2026-02-10T00:00:00Z")},
		{ID: 4, Nama: "Pensil 2B", SKU: "PN-004", Status: "aktif", Stok: 120, KategoriID: 3, InstansiID: 8, Aktif: true, JamBuka: "10:00:00", CreatedAt: utc("2026-03-05T00:00:00Z")},
		{ID: 5, Nama: "Kopi Susu 50%_off", SKU: "KP-005", Status: "aktif", Stok: 3, KategoriID: 1, InstansiID: 7, Aktif: true, JamBuka: "08:30:00", CreatedAt: utc("2026-10-18T17:00:00Z")},
		{ID: 6, Nama: "Buku Tulis", SKU: "BK-006", Status: "draft", Stok: 42, KategoriID: 3, InstansiID: 7, Aktif: false, JamBuka: "13:00:00", CreatedAt: utc("2026-10-19T15:59:00Z")},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
}

func ids[T any](rows []T, id func(T) uint) []uint {
	out := make([]uint, len(rows))
	for i, r := range rows {
		out[i] = id(r)
	}
	return out
}

func produkIDs(rows []Produk) []uint { return ids(rows, func(p Produk) uint { return p.ID }) }

func vals(pairs ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		v.Add(pairs[i], pairs[i+1])
	}
	return v
}

func mustTable[T any](t *testing.T, def Definition[T]) *Table[T] {
	t.Helper()
	tb, err := Define(def)
	if err != nil {
		t.Fatalf("Define: %v", err)
	}
	return tb
}

func isConfigErr(err error) bool {
	_, ok := err.(*dt.ConfigError)
	return ok
}

func isQueryErr(err error) bool {
	_, ok := err.(*dt.QueryError)
	return ok
}

func eqIDs(t *testing.T, got []uint, want ...uint) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}
