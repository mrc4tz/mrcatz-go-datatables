package gormtable

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"sync"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// Nilai bawaan batas baris.
const (
	DefaultExportLimit = 10000
)

// Definition adalah definisi tabel server (pengganti
// `MrCatzDataTablesComponent` + `configTable()` + `setFilter()` Laravel).
type Definition[T any] struct {
	// DB: koneksi GORM (wajib).
	DB *gorm.DB
	// BaseWhere: kondisi dasar yang selalu berlaku (pengganti `baseQuery()`),
	// mis. scoping per instansi atau soft-delete. ctx = context request.
	BaseWhere func(ctx context.Context, db *gorm.DB) *gorm.DB
	// Joins: asosiasi GORM yang di-join (belongs_to/has_one), dibutuhkan
	// kunci dot-path seperti `kategori.nama`. Contoh: "Kategori",
	// "Kategori.Induk".
	Joins []string
	// Preload & Select hanya diterapkan pada query pengambilan baris.
	Preload []string
	Select  []string

	Search *dt.SearchConfig
	// Relevance: peringkat relevansi PostgreSQL (lihat Relevance).
	Relevance *Relevance
	// Driver: mesin pencari eksternal, mis. meili.Driver(...). Tidak bisa
	// digabung dengan Relevance.
	Driver  SearchDriver
	Filters []dt.Filter

	// Sortable: whitelist kunci sort (json key). Sangat disarankan diisi —
	// tanpa daftar ini, kunci apa pun yang bisa dipetakan ke kolom boleh
	// dipakai untuk sort.
	Sortable []string
	// DefaultSort: urutan bila pengguna belum memilih (setDefaultOrder).
	DefaultSort []dt.SortRule
	// Tiebreaker: urutan penentu terakhir agar pagination stabil. nil =
	// [{primaryKey asc}]; NoTiebreaker mematikannya.
	Tiebreaker   []dt.SortRule
	NoTiebreaker bool
	// PrimaryKey: json key primary key. Default dari schema GORM.
	PrimaryKey string

	// DefaultLimit (10), MaxLimit (100), ExportLimit (10000).
	DefaultLimit, MaxLimit, ExportLimit int
	// Prefix query string (multi-tabel di satu halaman).
	Prefix string
	// CaseInsensitive: pencarian teks tidak peka huruf besar/kecil (ILIKE di
	// PostgreSQL, LOWER(..) LIKE di database lain). nil = true.
	CaseInsensitive *bool
	// UTCOffset: zona waktu filter tanggal, mis. "+08:00" (WITA).
	UTCOffset dt.UTCOffset
}

type compiledFilter struct {
	f      dt.Filter
	col    column
	hasCol bool
	where  WhereFunc
}

type orderItem struct {
	col  column
	desc bool
}

// Table adalah tabel data hasil Define. Aman dipakai bersamaan oleh banyak
// goroutine.
type Table[T any] struct {
	def        Definition[T]
	sch        *schema.Schema
	res        *resolver
	search     dt.NormalizedSearch
	searchCols []column
	filters    []compiledFilter
	types      map[string]dt.FilterType
	sortable   map[string]column // nil = tanpa whitelist
	defSort    []orderItem
	tiebreaker []orderItem
	pkKey      string
	pk         column
	relevance  *Relevance
	relDB      []string // nama kolom DB per kolom pencarian (mode relevansi)
	opts       sqlOpts
	defLimit   int
	maxLimit   int
	expLimit   int
}

// MustDefine sama dengan Define tetapi panic bila konfigurasi tidak valid.
func MustDefine[T any](def Definition[T]) *Table[T] {
	t, err := Define(def)
	if err != nil {
		panic(err)
	}
	return t
}

// Define memvalidasi definisi dan membangun tabel. Semua kesalahan
// konfigurasi bertipe *datatable.ConfigError.
func Define[T any](def Definition[T]) (*Table[T], error) {
	cfgErr := func(format string, args ...any) (*Table[T], error) {
		return nil, dt.ConfigErrorf(format, args...)
	}
	if def.DB == nil {
		return cfgErr("gormtable: Definition.DB wajib diisi.")
	}
	if err := def.Search.Validate(); err != nil {
		return nil, err
	}
	if _, err := def.UTCOffset.Location(); err != nil {
		return cfgErr("%s", err.Error())
	}
	sch, err := schema.Parse(new(T), &sync.Map{}, def.DB.NamingStrategy)
	if err != nil {
		return cfgErr("gormtable: gagal mem-parse schema %T: %v", *new(T), err)
	}
	joins, err := validateJoins(sch, def.Joins)
	if err != nil {
		return cfgErr("gormtable: %v", err)
	}
	t := &Table[T]{
		def:    def,
		sch:    sch,
		res:    &resolver{sch: sch, joins: joins},
		search: dt.NormalizeSearch(def.Search),
		types:  dt.FilterTypes(def.Filters),
		opts: sqlOpts{
			insensitive: def.CaseInsensitive == nil || *def.CaseInsensitive,
			postgres:    def.DB.Dialector != nil && def.DB.Dialector.Name() == "postgres",
			off:         def.UTCOffset,
		},
		defLimit: positive(def.DefaultLimit, dt.DefaultLimit),
		maxLimit: positive(def.MaxLimit, dt.MaxLimit),
		expLimit: positive(def.ExportLimit, DefaultExportLimit),
	}

	if def.Relevance != nil && def.Driver != nil {
		return cfgErr("Relevance dan Driver (%q) tidak bisa dipakai bersamaan — pilih salah satu.", def.Driver.Name())
	}
	if t.search.Typo.Driver == dt.TypoPgTrgm && def.Relevance == nil {
		return cfgErr("TypoTolerance.Driver %q hanya bekerja bersama Relevance (SQL mentah PostgreSQL). "+
			"Tambahkan `Relevance` atau pakai TypoTolerance{Driver: datatable.TypoTrigram}.", dt.TypoPgTrgm)
	}

	// Kolom pencarian.
	for _, sc := range t.search.Columns {
		col, err := t.res.resolve(sc.Field)
		if err != nil && def.Relevance != nil && sc.Column != "" {
			// Kolom khusus relevansi (mis. relasi one-to-many lewat Relevance.Joins):
			// dilewati di jalur SQL biasa (saat pengguna mengurutkan kolom / Scope).
			t.searchCols = append(t.searchCols, column{key: sc.Field})
			continue
		}
		if err != nil {
			return cfgErr("Kolom pencarian %q: %v. Untuk kolom khusus relevansi, isi Field dengan json key "+
				"(mis. \"customer.name\" + Joins) dan Column dengan `alias.kolom`.", sc.Field, err)
		}
		t.searchCols = append(t.searchCols, col)
	}

	// Filter.
	seen := map[string]bool{}
	for _, f := range def.Filters {
		if seen[f.ID] {
			return cfgErr("Id filter duplikat: %q", f.ID)
		}
		seen[f.ID] = true
		cf := compiledFilter{f: f}
		if f.Where != nil {
			fn, ok := whereFuncOf(f.Where)
			if !ok {
				return cfgErr("Filter %q: Where harus gormtable.WhereFunc (pakai gormtable.Where(...)), bukan %T.", f.ID, f.Where)
			}
			cf.where = fn
		}
		if (f.Type == dt.FilterDate || f.Type == dt.FilterDateRange) && dt.IsTimeFormat(f.EffectiveFormat()) && cf.where == nil {
			return cfgErr("%s", timeFormatMessage(f.ID))
		}
		if cf.where == nil {
			if f.Field == "" {
				return cfgErr("Filter %q butuh Field atau gormtable.Where(...) (Match hanya untuk memtable).", f.ID)
			}
			col, err := t.res.resolve(f.Field)
			if err != nil {
				return cfgErr("Filter %q: %v", f.ID, err)
			}
			cf.col, cf.hasCol = col, true
		}
		t.filters = append(t.filters, cf)
	}

	// Primary key.
	t.pkKey = def.PrimaryKey
	if t.pkKey == "" && sch.PrioritizedPrimaryField != nil {
		f := sch.PrioritizedPrimaryField
		t.pkKey = jsonName(f.Tag.Get("json"), f.Name)
	}
	needPK := !def.NoTiebreaker && def.Tiebreaker == nil || def.Relevance != nil || def.Driver != nil
	if t.pkKey != "" {
		pk, err := t.res.resolve(t.pkKey)
		if err != nil || pk.table != clause.CurrentTable {
			return cfgErr("PrimaryKey %q tidak bisa dipetakan ke kolom tabel %s.", t.pkKey, sch.Table)
		}
		t.pk = pk
	} else if needPK {
		return cfgErr("Model %s tidak punya primary key — isi Definition.PrimaryKey.", sch.Name)
	}

	// Sort.
	if def.Sortable != nil {
		t.sortable = map[string]column{}
		for _, k := range def.Sortable {
			col, err := t.res.resolve(k)
			if err != nil {
				return cfgErr("Sortable %q: %v", k, err)
			}
			t.sortable[k] = col
		}
	}
	if t.defSort, err = t.devOrder(def.DefaultSort, "DefaultSort"); err != nil {
		return nil, err
	}
	tb := def.Tiebreaker
	if tb == nil {
		tb = []dt.SortRule{{Key: t.pkKey, Dir: dt.Asc}}
	}
	if def.NoTiebreaker {
		tb = nil
	}
	if t.tiebreaker, err = t.devOrder(tb, "Tiebreaker"); err != nil {
		return nil, err
	}

	// Relevansi.
	if def.Relevance != nil {
		rel := *def.Relevance
		if rel.Table == "" {
			rel.Table = sch.Table
		}
		if rel.PrimaryKeyColumn == "" {
			rel.PrimaryKeyColumn = t.pk.name
		}
		t.relDB = make([]string, len(t.searchCols))
		for i, c := range t.searchCols {
			if c.name != "" && c.table == clause.CurrentTable {
				t.relDB[i] = c.name
			}
		}
		if _, err := QuoteIdent(rel.Table); err != nil {
			return nil, err
		}
		if _, err := QuoteIdent(rel.PrimaryKeyColumn); err != nil {
			return nil, err
		}
		joins, err := compileJoins(rel)
		if err != nil {
			return nil, err
		}
		for i, sc := range t.search.Columns {
			if _, err := columnRef(relevanceColumn(sc, t.relDB[i]), rel, joins); err != nil {
				return nil, err
			}
		}
		t.relevance = &rel
	}

	if v, ok := def.Driver.(DriverValidator); ok {
		if err := v.Validate(t.Config()); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func positive(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func timeFormatMessage(id string) string {
	return fmt.Sprintf("Filter %q memakai format jam (time/timeHm): kolom tanggal tidak bisa dibandingkan jamnya secara portabel. "+
		"Isi gormtable.Where(...), mis. memakai datatable.PeriodOf(v, FormatTimeHm, \"\") untuk kolom TIME, "+
		"atau datatable.ParseTimeOfDay(v) untuk kolom detik.", id)
}

// devOrder memetakan urutan yang didefinisikan developer (tanpa whitelist).
func (t *Table[T]) devOrder(rules []dt.SortRule, what string) ([]orderItem, error) {
	out := make([]orderItem, 0, len(rules))
	for _, r := range rules {
		col, err := t.res.resolve(r.Key)
		if err != nil {
			return nil, dt.ConfigErrorf("%s %q: %v", what, r.Key, err)
		}
		out = append(out, orderItem{col: col, desc: r.Dir == dt.Desc})
	}
	return out, nil
}

// Config mengembalikan ringkasan konfigurasi (introspeksi).
func (t *Table[T]) Config() TableConfig {
	var sortable []string
	if t.def.Sortable != nil {
		sortable = append([]string{}, t.def.Sortable...)
	}
	return TableConfig{
		Search:     t.search,
		Filters:    append([]dt.Filter{}, t.def.Filters...),
		Sortable:   sortable,
		PrimaryKey: t.pkKey,
		Table:      t.sch.Table,
		Driver:     t.def.Driver,
	}
}

// Parse mem-parse query string memakai prefix, batas limit, dan tipe filter
// tabel ini.
func (t *Table[T]) Parse(v url.Values) dt.TableQuery { return t.parse(v, t.maxLimit) }

func (t *Table[T]) parse(v url.Values, limitCap int) dt.TableQuery {
	return dt.ParseQuery(v, dt.ParseOptions{
		Prefix:       t.def.Prefix,
		DefaultLimit: t.defLimit,
		MaxLimit:     limitCap,
		FilterTypes:  t.types,
	})
}

// Query mem-parse query string lalu mengambil satu halaman data. Input
// pengguna yang tidak valid → *datatable.QueryError (HTTP 400).
func (t *Table[T]) Query(ctx context.Context, v url.Values) (*dt.Page[T], error) {
	return t.run(ctx, t.Parse(v))
}

// QueryParsed mengambil satu halaman untuk TableQuery yang sudah jadi (page
// < 1 → 1, limit dipangkas ke [1, MaxLimit]).
func (t *Table[T]) QueryParsed(ctx context.Context, q dt.TableQuery) (*dt.Page[T], error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Limit < 1 {
		q.Limit = t.defLimit
	}
	q.Limit = min(q.Limit, t.maxLimit)
	return t.run(ctx, q)
}

// ExportRows mengembalikan semua baris hasil filter + pencarian (tanpa
// pagination, maksimal ExportLimit) untuk export.
func (t *Table[T]) ExportRows(ctx context.Context, v url.Values) ([]T, error) {
	q := t.parse(v, t.expLimit)
	q.Page, q.Limit = 1, t.expLimit
	page, err := t.run(ctx, q)
	if err != nil {
		return nil, err
	}
	if page.Data == nil {
		return []T{}, nil
	}
	return page.Data, nil
}

// Describe menghitung schema filter (opsi + visibilitas + min/max dinamis)
// untuk nilai filter di query string.
func (t *Table[T]) Describe(ctx context.Context, v url.Values) (dt.Schema, error) {
	return t.DescribeWith(dt.FilterContext{Ctx: ctx, Values: t.Parse(v).Filters})
}

// DescribeWith menghitung schema filter untuk konteks filter tertentu.
func (t *Table[T]) DescribeWith(fc dt.FilterContext) (dt.Schema, error) {
	if fc.Values == nil {
		fc.Values = map[string]dt.FilterValue{}
	}
	return dt.DescribeFilters(fc, t.def.Filters)
}

// Scope mengembalikan *gorm.DB yang sudah berisi Joins + BaseWhere + filter +
// pencarian tabel (tanpa order/limit) — untuk aksi massal "semua hasil
// filter". Pencarian di sini selalu versi SQL (LIKE/ILIKE), bukan driver /
// relevansi. Catatan: GORM tidak mendukung UPDATE/DELETE dengan Joins; bila
// tabel memakai Joins, pakai subquery:
//
//	sub, _ := table.Scope(ctx, q)
//	db.Where("id IN (?)", sub.Select("users.id")).Delete(&User{})
//
// Kolom tabel join tidak ikut di-SELECT, jadi `.Select(...)`, `.Count` dan
// `.Pluck` aman dipakai langsung.
func (t *Table[T]) Scope(ctx context.Context, v url.Values) (*gorm.DB, error) {
	q := t.Parse(v)
	scopes, err := t.conditions(ctx, q, true, nil)
	if err != nil {
		return nil, err
	}
	return t.chain(ctx, scopes), nil
}

func filterCtx(ctx context.Context, q dt.TableQuery) dt.FilterContext {
	vals := q.Filters
	if vals == nil {
		vals = map[string]dt.FilterValue{}
	}
	return dt.FilterContext{Ctx: ctx, Values: vals}
}

type scope = func(*gorm.DB) *gorm.DB

// conditions: filter (+ pencarian bila withSearch). skip = id filter yang
// sudah diterapkan search driver (pushdown).
func (t *Table[T]) conditions(ctx context.Context, q dt.TableQuery, withSearch bool, skip map[string]bool) ([]scope, error) {
	fc := filterCtx(ctx, q)
	var out []scope
	for _, cf := range t.filters {
		if skip[cf.f.ID] {
			continue
		}
		v, ok := dt.ResolveValue(cf.f, q.Filters[cf.f.ID])
		if !ok {
			continue
		}
		if cf.where != nil {
			fn := cf.where
			out = append(out, func(db *gorm.DB) *gorm.DB { return fn(db, v, fc) })
			continue
		}
		e, err := filterExpr(cf.f, cf.col.expr(), v, fc, t.opts)
		if err != nil {
			var qe *dt.QueryError
			if !errors.As(err, &qe) {
				err = &dt.QueryError{Msg: err.Error(), Err: err}
			}
			return nil, err
		}
		if e != nil {
			out = append(out, whereClause(e))
		}
	}
	if withSearch {
		if e := searchExpr(q.Search, t.search, t.searchCols, t.opts); e != nil {
			out = append(out, whereClause(e))
		}
	}
	return out, nil
}

// chain: query dasar (model + Joins + BaseWhere) plus scope, untuk
// count/pluck/Scope. Kolom tabel join tidak ikut di-SELECT (Omit("*")) agar
// chain bisa dipakai sebagai subquery `.Select("tabel.id")`.
func (t *Table[T]) chain(ctx context.Context, scopes []scope) *gorm.DB {
	db := t.def.DB.WithContext(ctx).Model(new(T))
	for _, j := range t.def.Joins {
		db = db.Joins(j, t.def.DB.Session(&gorm.Session{NewDB: true}).Omit("*"))
	}
	if t.def.BaseWhere != nil {
		db = t.def.BaseWhere(ctx, db)
	}
	for _, s := range scopes {
		db = s(db)
	}
	return db
}

// rowsChain: chain untuk mengambil baris — Joins penuh (struct relasi ikut
// terisi) + Preload/Select.
func (t *Table[T]) rowsChain(ctx context.Context, scopes []scope) *gorm.DB {
	db := t.def.DB.WithContext(ctx).Model(new(T))
	for _, j := range t.def.Joins {
		db = db.Joins(j)
	}
	if t.def.BaseWhere != nil {
		db = t.def.BaseWhere(ctx, db)
	}
	for _, s := range scopes {
		db = s(db)
	}
	return t.rowsQuery(db)
}

// rowsQuery menambahkan Preload/Select (hanya untuk pengambilan baris).
func (t *Table[T]) rowsQuery(db *gorm.DB) *gorm.DB {
	for _, p := range t.def.Preload {
		db = db.Preload(p)
	}
	if len(t.def.Select) > 0 {
		db = db.Select(t.def.Select)
	}
	return db
}

// order: sort pengguna (lolos whitelist) + default (bila pengguna belum
// memilih) + tiebreaker yang kuncinya belum dipakai.
func (t *Table[T]) order(q dt.TableQuery) ([]clause.OrderByColumn, error) {
	var out []clause.OrderByColumn
	seen := map[string]bool{}
	for _, s := range q.Sort {
		col, ok := t.sortable[s.Key]
		if t.sortable == nil {
			var err error
			col, err = t.res.resolve(s.Key)
			ok = err == nil
		}
		if !ok || !keyRE.MatchString(s.Key) {
			return nil, dt.QueryErrorf("Kolom %q tidak bisa diurutkan.", s.Key)
		}
		seen[s.Key] = true
		out = append(out, clause.OrderByColumn{Column: col.expr(), Desc: s.Dir == dt.Desc})
	}
	if len(q.Sort) == 0 {
		for _, o := range t.defSort {
			seen[o.col.key] = true
			out = append(out, clause.OrderByColumn{Column: o.col.expr(), Desc: o.desc})
		}
	}
	for _, o := range t.tiebreaker {
		if !seen[o.col.key] {
			out = append(out, clause.OrderByColumn{Column: o.col.expr(), Desc: o.desc})
		}
	}
	return out, nil
}

func applyOrder(db *gorm.DB, order []clause.OrderByColumn) *gorm.DB {
	if len(order) == 0 {
		return db
	}
	return db.Clauses(clause.OrderBy{Columns: order})
}

func (t *Table[T]) run(ctx context.Context, q dt.TableQuery) (*dt.Page[T], error) {
	order, err := t.order(q) // validasi sort sebelum menyentuh driver/database
	if err != nil {
		return nil, err
	}
	if page, err := t.runDriver(ctx, q, order); err != nil || page != nil {
		return page, err
	}
	if page, err := t.runRelevance(ctx, q); err != nil || page != nil {
		return page, err
	}
	scopes, err := t.conditions(ctx, q, true, nil)
	if err != nil {
		return nil, err
	}
	return t.sqlPage(ctx, q, scopes, order)
}

// sqlPage: count + ambil satu halaman dengan urutan SQL.
func (t *Table[T]) sqlPage(ctx context.Context, q dt.TableQuery, scopes []scope, order []clause.OrderByColumn) (*dt.Page[T], error) {
	var total int64
	if err := t.chain(ctx, scopes).Count(&total).Error; err != nil {
		return nil, err
	}
	rows := []T{}
	offset := (q.Page - 1) * q.Limit
	if int64(offset) < total {
		db := applyOrder(t.rowsChain(ctx, scopes), order).Offset(offset).Limit(q.Limit)
		if err := db.Find(&rows).Error; err != nil {
			return nil, err
		}
	}
	return &dt.Page[T]{Data: rows, Meta: dt.NewMeta(total, q.Page, q.Limit)}, nil
}

func (t *Table[T]) pkIn(ids []any) scope {
	vals := make([]any, len(ids))
	for i, id := range ids {
		vals[i] = sqlValue(id)
	}
	return whereClause(clause.IN{Column: t.pk.expr(), Values: vals})
}

// rankedPage: pipeline bersama mode relevansi & search driver — id terurut
// peringkat → filter SQL (`pk IN ids`) → urutkan ulang sesuai peringkat →
// paginate. Bila pengguna memilih sort, id hanya dipakai sebagai filter dan
// urutan mengikuti sort pengguna.
func (t *Table[T]) rankedPage(ctx context.Context, q dt.TableQuery, ranked []any, skip map[string]bool, order []clause.OrderByColumn) (*dt.Page[T], error) {
	if len(ranked) == 0 {
		return &dt.Page[T]{Data: []T{}, Meta: dt.NewMeta(0, q.Page, q.Limit)}, nil
	}
	scopes, err := t.conditions(ctx, q, false, skip)
	if err != nil {
		return nil, err
	}
	scopes = append(scopes, t.pkIn(ranked))
	if len(q.Sort) > 0 {
		return t.sqlPage(ctx, q, scopes, order)
	}

	rank := map[string]int{}
	for i, id := range ranked {
		k := idKey(id)
		if _, ok := rank[k]; !ok {
			rank[k] = i
		}
	}
	at := func(id any) int {
		if i, ok := rank[idKey(id)]; ok {
			return i
		}
		return int(^uint(0) >> 1)
	}

	var matching []any
	if err := t.chain(ctx, scopes).Pluck(t.sch.Table+"."+t.pk.name, &matching).Error; err != nil {
		return nil, err
	}
	sort.SliceStable(matching, func(i, j int) bool { return at(matching[i]) < at(matching[j]) })

	start := min((q.Page-1)*q.Limit, len(matching))
	end := min(start+q.Limit, len(matching))
	pageIDs := matching[start:end]
	rows := []T{}
	if len(pageIDs) > 0 {
		// Seperti JS: hanya `pk IN pageIds` (filter sudah diterapkan di atas).
		db := t.def.DB.WithContext(ctx).Model(new(T))
		for _, j := range t.def.Joins {
			db = db.Joins(j)
		}
		if err := t.pkIn(pageIDs)(t.rowsQuery(db)).Find(&rows).Error; err != nil {
			return nil, err
		}
		pos := map[string]int{}
		for i, id := range pageIDs {
			pos[idKey(id)] = i
		}
		sort.SliceStable(rows, func(i, j int) bool {
			return pos[idKey(t.rowPK(ctx, &rows[i]))] < pos[idKey(t.rowPK(ctx, &rows[j]))]
		})
	}
	return &dt.Page[T]{Data: rows, Meta: dt.NewMeta(int64(len(matching)), q.Page, q.Limit)}, nil
}

func (t *Table[T]) rowPK(ctx context.Context, row *T) any {
	v, _ := t.pk.field.ValueOf(ctx, reflect.ValueOf(row).Elem())
	return v
}

// idKey menyamakan id lintas tipe (int64 dari DB, float64 dari JSON, string)
// seperti `String(id)` di JS.
func idKey(id any) string {
	switch v := dt.Primitive(id).(type) {
	case float64:
		return dt.FormatNumber(v)
	case []byte:
		return string(v)
	case nil:
		return ""
	default:
		return dt.FormatValue(v)
	}
}

func (t *Table[T]) runRelevance(ctx context.Context, q dt.TableQuery) (*dt.Page[T], error) {
	if t.relevance == nil || q.Search == "" || len(q.Sort) > 0 {
		return nil, nil
	}
	sqlText, args, ok, err := buildRelevanceSQL(q.Search, *t.relevance, t.search, t.relDB)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := t.def.DB.WithContext(ctx).Raw(sqlText, args...).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		id    any
		score float64
		i     int
	}
	var list []scored
	for rows.Next() {
		var s scored
		if err := rows.Scan(&s.id, &s.score); err != nil {
			return nil, err
		}
		s.i = len(list)
		list = append(list, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Urutan stabil berdasarkan skor (SQL sudah ORDER BY score, ini pengaman).
	sort.SliceStable(list, func(a, b int) bool { return list[a].score > list[b].score })
	ids := make([]any, len(list))
	for i, s := range list {
		ids[i] = s.id
	}
	return t.rankedPage(ctx, q, ids, nil, nil)
}

func (t *Table[T]) runDriver(ctx context.Context, q dt.TableQuery, order []clause.OrderByColumn) (*dt.Page[T], error) {
	if t.def.Driver == nil || len(dt.SplitKeywords(q.Search)) == 0 {
		return nil, nil
	}
	fc := filterCtx(ctx, q)
	var active []ActiveFilter
	for _, cf := range t.filters {
		if v, ok := dt.ResolveValue(cf.f, q.Filters[cf.f.ID]); ok {
			active = append(active, ActiveFilter{Filter: cf.f, Value: v})
		}
	}
	res, err := t.def.Driver.Search(ctx, DriverRequest{
		Search:        q.Search,
		Query:         q,
		Filters:       active,
		FilterContext: fc,
		PrimaryKey:    t.pkKey,
		UTCOffset:     t.def.UTCOffset,
	})
	if err != nil || res == nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, id := range res.PushedFilters {
		skip[id] = true
	}
	return t.rankedPage(ctx, q, res.IDs, skip, order)
}
