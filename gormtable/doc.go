// Package gormtable adalah engine server MrCatz DataTable di atas GORM —
// padanan `defineDataTable` versi JS (engine + prisma-where + relevance +
// search-driver).
//
//	type User struct {
//		ID         uint      `json:"id"`
//		Nama       string    `json:"nama"`
//		Email      string    `json:"email"`
//		Status     string    `json:"status"`
//		KategoriID uint      `json:"kategoriId"`
//		Kategori   Kategori  `json:"kategori"`
//		CreatedAt  time.Time `json:"createdAt"`
//	}
//
//	var users = gormtable.MustDefine(gormtable.Definition[User]{
//		DB:    db,
//		Joins: []string{"Kategori"},
//		BaseWhere: func(ctx context.Context, db *gorm.DB) *gorm.DB {
//			return db.Where("produks.instansi_id = ?", instansiFrom(ctx)) // qualify: tabel lain bisa ikut di-join
//		},
//		Search: &datatable.SearchConfig{Columns: []datatable.SearchColumn{
//			datatable.Col("nama").W(3), datatable.Col("email"), datatable.Col("kategori.nama"),
//		}},
//		Filters: []datatable.Filter{
//			datatable.Select("status", "Status", datatable.Field("status"), datatable.Options(STATUS...)),
//			datatable.DateRange("dibuat", "Dibuat", datatable.Field("createdAt")),
//		},
//		Sortable:    []string{"nama", "email", "createdAt", "kategori.nama"},
//		DefaultSort: []datatable.SortRule{{Key: "createdAt", Dir: datatable.Desc}},
//		UTCOffset:   "+08:00",
//	})
//
//	page, err := users.Query(ctx, r.URL.Query()) // *datatable.QueryError → 400
//
// Kunci sort/filter/search adalah nama json yang dilihat client; engine
// memetakannya ke kolom database lewat schema GORM (tag `json`, lalu nama
// kolom DB, lalu nama field Go). Dot-path (`kategori.nama`) memakai asosiasi
// yang terdaftar di Definition.Joins (alias GORM `"Kategori"."nama"`). Semua
// identifier divalidasi (`^[A-Za-z_][A-Za-z0-9_]*$` per segmen) sebelum
// sampai ke SQL; nilai selalu lewat parameter.
package gormtable
