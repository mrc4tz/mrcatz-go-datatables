package gormtable

import (
	"context"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
)

// SearchDriver adalah kontrak mesin pencari eksternal (pengganti
// `search.driver = 'scout'` versi Laravel), mis. meili.Driver(...).
//
// Driver mengembalikan id baris yang sudah terurut relevansi; engine lalu
// memakai pipeline yang sama dengan mode relevansi:
// id terurut → `WHERE pk IN ?` (+ filter sisa) → urutkan ulang sesuai
// peringkat (atau sesuai sort pengguna bila ada) → paginate.
type SearchDriver interface {
	// Name adalah nama driver (untuk pesan error/log).
	Name() string
	// Search mencari id yang cocok. Kembalikan (nil, nil) untuk memakai
	// pencarian SQL biasa (mis. driver sedang tidak tersedia).
	Search(ctx context.Context, req DriverRequest) (*DriverResult, error)
}

// DriverValidator boleh diimplementasikan driver untuk memvalidasi
// konfigurasi tabel saat Define (kembalikan *datatable.ConfigError).
type DriverValidator interface {
	Validate(cfg TableConfig) error
}

// ActiveFilter adalah filter aktif (nilai sudah dinormalisasi) yang
// diteruskan ke driver.
type ActiveFilter struct {
	Filter dt.Filter
	Value  dt.Resolved
}

// DriverRequest adalah permintaan ke SearchDriver.
type DriverRequest struct {
	// Search: kata kunci (tidak pernah kosong).
	Search string
	Query  dt.TableQuery
	// Filters: filter aktif — driver boleh menerapkan sebagian (pushdown).
	Filters       []ActiveFilter
	FilterContext dt.FilterContext
	// PrimaryKey: json key primary key tabel.
	PrimaryKey string
	UTCOffset  dt.UTCOffset
}

// DriverResult adalah hasil SearchDriver.
type DriverResult struct {
	// IDs: id baris, urut dari yang paling relevan.
	IDs []any
	// PushedFilters: id filter yang sudah diterapkan driver — engine tidak
	// menerapkannya lagi di SQL.
	PushedFilters []string
}

// TableConfig adalah ringkasan konfigurasi tabel (introspeksi untuk
// meili.SyncSettings, CLI, dan DriverValidator).
type TableConfig struct {
	Search  dt.NormalizedSearch
	Filters []dt.Filter
	// Sortable: whitelist kunci sort (nil = tanpa whitelist).
	Sortable []string
	// PrimaryKey: json key primary key.
	PrimaryKey string
	// Table: nama tabel database dari schema GORM.
	Table  string
	Driver SearchDriver
}
