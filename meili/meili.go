// Package meili adalah driver Meilisearch untuk gormtable (pengganti
// `search.driver = 'scout'` + `mrcatz:meilisearch:configure` versi Laravel,
// padanan `@mrcatz/datatable-server/meilisearch`).
//
// Paket `meilisearch-go` sengaja TIDAK menjadi dependensi: client diketik
// secara struktural (interface Client/Index), cukup bungkus client milik
// aplikasi dengan adapter kecil. Contoh untuk github.com/meilisearch/meilisearch-go
// (v0.3x; sesuaikan bila API versi Anda berbeda):
//
//	type meiliClient struct{ sm meilisearch.ServiceManager }
//
//	func (c meiliClient) Index(uid string) meili.Index { return meiliIndex{c.sm.Index(uid)} }
//
//	type meiliIndex struct{ im meilisearch.IndexManager }
//
//	func (x meiliIndex) Search(ctx context.Context, q string, p meili.SearchParams) (*meili.SearchResult, error) {
//		req := &meilisearch.SearchRequest{Limit: int64(p.Limit), Offset: int64(p.Offset), AttributesToRetrieve: p.AttributesToRetrieve}
//		if p.Filter != "" {
//			req.Filter = p.Filter
//		}
//		if s, ok := p.Extra["matchingStrategy"].(string); ok {
//			req.MatchingStrategy = meilisearch.MatchingStrategy(s)
//		}
//		res, err := x.im.SearchWithContext(ctx, q, req)
//		if err != nil {
//			return nil, err
//		}
//		out := &meili.SearchResult{}
//		for _, h := range res.Hits { // Hit = map[string]json.RawMessage
//			m := map[string]any{}
//			for k, raw := range h {
//				var v any
//				_ = json.Unmarshal(raw, &v)
//				m[k] = v
//			}
//			out.Hits = append(out.Hits, m)
//		}
//		return out, nil
//	}
//
//	func (x meiliIndex) UpdateSettings(ctx context.Context, s meili.IndexSettings) error {
//		set := &meilisearch.Settings{SearchableAttributes: s.SearchableAttributes,
//			FilterableAttributes: s.FilterableAttributes, SortableAttributes: s.SortableAttributes}
//		if t := s.TypoTolerance; t != nil {
//			set.TypoTolerance = &meilisearch.TypoTolerance{Enabled: t.Enabled, MinWordSizeForTypos: meilisearch.MinWordSizeForTypos{
//				OneTypo: int64(t.MinWordSizeForTypos.OneTypo), TwoTypos: int64(t.MinWordSizeForTypos.TwoTypos)}}
//		}
//		_, err := x.im.UpdateSettingsWithContext(ctx, set)
//		return err
//	}
//
// Pemakaian:
//
//	drv := meili.MustDriver(meili.Options{
//		Client:     meiliClient{meilisearch.New(host, meilisearch.WithAPIKey(key))},
//		Index:      "products",
//		DateFields: map[string]string{"createdAt": "createdAtTs"}, // atribut berisi unix detik
//	})
//	products := gormtable.MustDefine(gormtable.Definition[Product]{DB: db, Search: ..., Driver: drv})
//	_, err := meili.SyncSettings(ctx, products, nil, "", false) // lalu indeks ulang dokumen
package meili

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"github.com/mrc4tz/mrcatz-go-datatables/gormtable"
)

// ---------------------------------------------------------------------------
// Client minimal (struktural)
// ---------------------------------------------------------------------------

// SearchParams adalah parameter pencarian yang dikirim driver.
type SearchParams struct {
	Filter               string
	Limit                int
	Offset               int
	AttributesToRetrieve []string
	// Extra: Options.SearchParams apa adanya (mis. {"matchingStrategy": "all"}).
	Extra map[string]any
}

// SearchResult adalah hasil pencarian; setiap hit cukup berisi atribut id.
type SearchResult struct {
	Hits []map[string]any
}

// Index adalah bagian index Meilisearch yang dipakai driver.
type Index interface {
	Search(ctx context.Context, query string, params SearchParams) (*SearchResult, error)
}

// SettingsUpdater boleh diimplementasikan Index; dibutuhkan SyncSettings.
type SettingsUpdater interface {
	UpdateSettings(ctx context.Context, settings IndexSettings) error
}

// Client adalah client Meilisearch minimal.
type Client interface {
	Index(uid string) Index
}

// ---------------------------------------------------------------------------
// Driver
// ---------------------------------------------------------------------------

// FilterPushdown menentukan filter mana yang dikirim ke Meilisearch.
type FilterPushdown string

const (
	// PushdownAuto: filter yang bisa diterjemahkan dikirim ke Meilisearch,
	// sisanya tetap di SQL (default).
	PushdownAuto FilterPushdown = "auto"
	// PushdownAlways: semua filter wajib bisa diterjemahkan; bila tidak →
	// *datatable.ConfigError saat gormtable.Define.
	PushdownAlways FilterPushdown = "always"
	// PushdownNever: semua filter tetap di SQL; Meilisearch hanya untuk
	// pencarian.
	PushdownNever FilterPushdown = "never"
)

// Options mengatur driver Meilisearch.
type Options struct {
	Client Client
	// Index: nama (uid) index.
	Index string
	// MaxResults: batas id yang diambil (pengganti `scout_max_results`).
	// Default 1000.
	MaxResults int
	// FilterPushdown: default PushdownAuto.
	FilterPushdown FilterPushdown
	// DateFields: filter tanggal → atribut Meilisearch berisi unix timestamp
	// (detik), pengganti `date_field_map`. Kunci boleh id filter atau Field-nya.
	DateFields map[string]string
	// PrimaryKey: atribut id di dokumen Meilisearch. Default primary key tabel.
	PrimaryKey string
	// CastID mengubah id dari Meilisearch sebelum dipakai di SQL. Default:
	// angka JSON bulat → int64, selain itu apa adanya.
	CastID func(id any) any
	// SearchParams: parameter pencarian tambahan (diteruskan ke SearchParams.Extra).
	SearchParams map[string]any
	// OnFallback dipanggil (mode auto) saat filter tidak bisa dikirim dan
	// tetap diterapkan di SQL.
	OnFallback func(filterID, reason string)
}

// SearchDriver adalah driver Meilisearch; memenuhi gormtable.SearchDriver
// dan gormtable.DriverValidator.
type SearchDriver struct {
	opts       Options
	mode       FilterPushdown
	maxResults int
	dateFields map[string]string
}

var attrRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// Driver membuat driver Meilisearch. Kesalahan bertipe *datatable.ConfigError.
func Driver(opts Options) (*SearchDriver, error) {
	mode := opts.FilterPushdown
	if mode == "" {
		mode = PushdownAuto
	}
	if mode != PushdownAuto && mode != PushdownAlways && mode != PushdownNever {
		return nil, dt.ConfigErrorf("FilterPushdown tidak valid: %q (pilih auto, always, atau never).", mode)
	}
	if opts.Client == nil {
		return nil, dt.ConfigErrorf("meili.Driver butuh `Client`.")
	}
	if opts.Index == "" {
		return nil, dt.ConfigErrorf("meili.Driver butuh `Index`.")
	}
	maxResults := opts.MaxResults
	if maxResults == 0 {
		maxResults = 1000
	}
	if maxResults < 1 {
		return nil, dt.ConfigErrorf("MaxResults harus ≥ 1.")
	}
	dateFields := map[string]string{}
	for k, attr := range opts.DateFields {
		if !attrRE.MatchString(attr) {
			return nil, dt.ConfigErrorf("DateFields %q bukan nama atribut Meilisearch yang valid.", attr)
		}
		dateFields[k] = attr
	}
	if opts.PrimaryKey != "" && !attrRE.MatchString(opts.PrimaryKey) {
		return nil, dt.ConfigErrorf("PrimaryKey %q bukan nama atribut Meilisearch yang valid.", opts.PrimaryKey)
	}
	return &SearchDriver{opts: opts, mode: mode, maxResults: maxResults, dateFields: dateFields}, nil
}

// MustDriver sama dengan Driver tetapi panic bila opsi tidak valid.
func MustDriver(opts Options) *SearchDriver {
	d, err := Driver(opts)
	if err != nil {
		panic(err)
	}
	return d
}

// Name selalu "meilisearch".
func (d *SearchDriver) Name() string { return "meilisearch" }

// Options mengembalikan opsi driver.
func (d *SearchDriver) Options() Options { return d.opts }

// Validate (mode always): semua filter tabel wajib bisa diterjemahkan.
func (d *SearchDriver) Validate(cfg gormtable.TableConfig) error {
	if d.mode != PushdownAlways {
		return nil
	}
	for _, f := range cfg.Filters {
		if reason := PushdownBlocker(f, d.dateFields); reason != "" {
			return dt.ConfigErrorf("FilterPushdown 'always': filter %q %s. Pakai mode 'auto' atau 'never'.", f.ID, reason)
		}
	}
	return nil
}

// Search mencari id di Meilisearch dan menerjemahkan filter (pushdown).
func (d *SearchDriver) Search(ctx context.Context, req gormtable.DriverRequest) (*gormtable.DriverResult, error) {
	var exprs []string
	pushed := []string{}
	if d.mode != PushdownNever {
		for _, af := range req.Filters {
			if reason := PushdownBlocker(af.Filter, d.dateFields); reason != "" {
				if d.mode == PushdownAlways {
					return nil, dt.ConfigErrorf("Filter %q %s.", af.Filter.ID, reason)
				}
				if d.opts.OnFallback != nil {
					d.opts.OnFallback(af.Filter.ID, reason)
				}
				continue
			}
			e, err := TranslateFilter(af, req.FilterContext, d.dateFields, req.UTCOffset)
			if err != nil {
				return nil, err
			}
			if e != "" {
				exprs = append(exprs, e)
			}
			pushed = append(pushed, af.Filter.ID)
		}
	}

	idAttr := d.opts.PrimaryKey
	if idAttr == "" {
		idAttr = req.PrimaryKey
	}
	params := SearchParams{Limit: d.maxResults, AttributesToRetrieve: []string{idAttr}, Extra: d.opts.SearchParams}
	if len(exprs) > 0 {
		params.Filter = strings.Join(exprs, " AND ")
	}
	res, err := d.opts.Client.Index(d.opts.Index).Search(ctx, req.Search, params)
	if err != nil {
		return nil, err
	}
	cast := d.opts.CastID
	if cast == nil {
		cast = defaultCastID
	}
	out := &gormtable.DriverResult{IDs: []any{}, PushedFilters: pushed}
	if res != nil {
		for _, h := range res.Hits {
			raw, ok := h[idAttr]
			if !ok || raw == nil {
				continue
			}
			if id := cast(raw); id != nil {
				out.IDs = append(out.IDs, id)
			}
		}
	}
	return out, nil
}

// defaultCastID: angka JSON bulat (float64 / json.Number) → int64.
func defaultCastID(id any) any {
	switch v := id.(type) {
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return int64(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
		return v.String()
	}
	return id
}

// Value menulis literal filter Meilisearch: angka & boolean apa adanya,
// string di-quote (dengan escape `\` dan `"`).
func Value(v any) string {
	switch x := dt.Primitive(v).(type) {
	case float64:
		if !math.IsInf(x, 0) && !math.IsNaN(x) {
			return dt.FormatNumber(x)
		}
	case bool:
		return strconv.FormatBool(x)
	}
	s := dt.FormatValue(v)
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
	return `"` + s + `"`
}

func dateAttr(f dt.Filter, dateFields map[string]string) string {
	if a, ok := dateFields[f.ID]; ok {
		return a
	}
	if f.Field != "" {
		return dateFields[f.Field]
	}
	return ""
}

// PushdownBlocker mengembalikan alasan filter tidak bisa diterjemahkan ke
// Meilisearch, atau "" bila bisa. Murni dari definisi — sehingga mode
// always bisa diperiksa saat Define.
func PushdownBlocker(f dt.Filter, dateFields map[string]string) string {
	if f.Where != nil {
		return "memakai Where kustom (tidak bisa diterjemahkan)"
	}
	if f.Field == "" {
		return "tidak punya Field"
	}
	switch f.Type {
	case dt.FilterSelect:
		if f.Operator == dt.OpContains {
			return "operator 'contains' tidak didukung filter Meilisearch"
		}
		fallthrough
	case dt.FilterMulti:
		if !attrRE.MatchString(f.Field) {
			return "field \"" + f.Field + "\" bukan atribut Meilisearch yang valid"
		}
		return ""
	case dt.FilterDate, dt.FilterDateRange:
		if dt.IsTimeFormat(f.EffectiveFormat()) {
			return "format jam (time/timeHm) tidak bisa diterjemahkan"
		}
		if dateAttr(f, dateFields) == "" {
			return "tidak ada entri DateFields untuk filter tanggal ini"
		}
		return ""
	}
	return "tipe filter tidak dikenal"
}

// TranslateFilter menerjemahkan satu filter aktif ke ekspresi filter
// Meilisearch (anggap sudah lolos PushdownBlocker). Tanggal → unix detik
// sesuai UTCOffset; tanggal tidak valid → *datatable.QueryError. "" = tanpa
// kondisi (rentang terbuka di kedua sisi).
func TranslateFilter(af gormtable.ActiveFilter, fc dt.FilterContext, dateFields map[string]string, off dt.UTCOffset) (string, error) {
	f, v := af.Filter, af.Value
	switch f.Type {
	case dt.FilterSelect:
		return f.Field + " " + string(f.EffectiveOperator(fc)) + " " + Value(v.Scalar), nil
	case dt.FilterMulti:
		parts := make([]string, len(v.Values))
		for i, x := range v.Values {
			parts[i] = Value(x)
		}
		op := "IN"
		if v.Exclude {
			op = "NOT IN"
		}
		return f.Field + " " + op + " [" + strings.Join(parts, ", ") + "]", nil
	case dt.FilterDate, dt.FilterDateRange:
		b, err := dt.FilterBounds(f, v, fc, off)
		if err != nil {
			return "", err
		}
		return boundsExpr(dateAttr(f, dateFields), b), nil
	}
	return "", nil
}

func boundsExpr(attr string, b dt.Bounds) string {
	sec := func(t interface{ Unix() int64 }) string { return strconv.FormatInt(t.Unix(), 10) }
	if b.Not != nil {
		return "(" + attr + " < " + sec(b.Not.Start) + " OR " + attr + " >= " + sec(b.Not.End) + ")"
	}
	var parts []string
	if b.GTE != nil {
		parts = append(parts, attr+" >= "+sec(*b.GTE))
	}
	if b.LT != nil {
		parts = append(parts, attr+" < "+sec(*b.LT))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " AND ") + ")"
}

// ---------------------------------------------------------------------------
// Sinkronisasi pengaturan index (pengganti `mrcatz:meilisearch:configure`)
// ---------------------------------------------------------------------------

// IndexSettings adalah pengaturan index Meilisearch (JSON sama dengan API).
type IndexSettings struct {
	SearchableAttributes []string       `json:"searchableAttributes,omitempty"`
	FilterableAttributes []string       `json:"filterableAttributes,omitempty"`
	SortableAttributes   []string       `json:"sortableAttributes,omitempty"`
	TypoTolerance        *TypoTolerance `json:"typoTolerance,omitempty"`
}

// TypoTolerance adalah pengaturan toleransi salah ketik Meilisearch.
type TypoTolerance struct {
	Enabled             bool                `json:"enabled"`
	MinWordSizeForTypos MinWordSizeForTypos `json:"minWordSizeForTypos"`
}

// MinWordSizeForTypos: panjang kata minimum untuk 1 / 2 salah ketik.
type MinWordSizeForTypos struct {
	OneTypo  int `json:"oneTypo"`
	TwoTypos int `json:"twoTypos"`
}

// Configurer dipenuhi *gormtable.Table[T] (lewat Config()).
type Configurer interface {
	Config() gormtable.TableConfig
}

func unique(xs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// Settings menghitung pengaturan index dari definisi tabel; DateFields
// diambil dari driver Meilisearch tabel (bila ada). Lihat SettingsWith.
func Settings(table Configurer) IndexSettings { return SettingsWith(table, nil) }

// SettingsWith menghitung pengaturan index:
//   - searchableAttributes dari kolom pencarian, urut bobot terbesar,
//   - filterableAttributes dari Field filter yang bisa dikirim + DateFields,
//   - sortableAttributes dari Sortable,
//   - typoTolerance dari Search.TypoTolerance (hanya bila diaktifkan).
//
// dateFields nil = pakai DateFields milik driver tabel.
func SettingsWith(table Configurer, dateFields map[string]string) IndexSettings {
	cfg := table.Config()
	if dateFields == nil {
		if d, ok := cfg.Driver.(*SearchDriver); ok {
			dateFields = d.dateFields
		}
	}
	var s IndexSettings

	type wc struct {
		field  string
		weight int
	}
	cols := make([]wc, len(cfg.Search.Columns))
	for i, c := range cfg.Search.Columns {
		cols[i] = wc{c.Field, c.Weight}
	}
	sort.SliceStable(cols, func(i, j int) bool { return cols[i].weight > cols[j].weight })
	var searchable []string
	for _, c := range cols {
		searchable = append(searchable, c.field)
	}
	if len(searchable) > 0 {
		s.SearchableAttributes = unique(searchable)
	}

	var filterable []string
	for _, f := range cfg.Filters {
		if PushdownBlocker(f, dateFields) != "" {
			continue
		}
		if f.Type == dt.FilterDate || f.Type == dt.FilterDateRange {
			filterable = append(filterable, dateAttr(f, dateFields))
		} else {
			filterable = append(filterable, f.Field)
		}
	}
	extra := make([]string, 0, len(dateFields))
	for _, a := range dateFields {
		extra = append(extra, a)
	}
	sort.Strings(extra) // map tidak berurutan → urut abjad agar deterministik
	filterable = append(filterable, extra...)
	if len(filterable) > 0 {
		s.FilterableAttributes = unique(filterable)
	}

	if len(cfg.Sortable) > 0 {
		s.SortableAttributes = unique(cfg.Sortable)
	}
	if cfg.Search.Typo.Driver != dt.TypoNone {
		m := cfg.Search.Typo.MinWordLength
		s.TypoTolerance = &TypoTolerance{Enabled: true, MinWordSizeForTypos: MinWordSizeForTypos{OneTypo: m, TwoTypos: m * 2}}
	}
	return s
}

// SyncSettings mengirim pengaturan index ke Meilisearch. client nil / index
// "" = ambil dari driver Meilisearch tabel. dryRun = hanya hitung. Index
// wajib memenuhi SettingsUpdater. Kembalikan pengaturan yang (akan) dikirim.
func SyncSettings(ctx context.Context, table Configurer, client Client, index string, dryRun bool) (IndexSettings, error) {
	if d, ok := table.Config().Driver.(*SearchDriver); ok {
		if client == nil {
			client = d.opts.Client
		}
		if index == "" {
			index = d.opts.Index
		}
	}
	if client == nil || index == "" {
		return IndexSettings{}, dt.ConfigErrorf("SyncSettings butuh client dan index (atau tabel dengan meili.Driver).")
	}
	settings := Settings(table)
	if dryRun {
		return settings, nil
	}
	up, ok := client.Index(index).(SettingsUpdater)
	if !ok {
		return IndexSettings{}, dt.ConfigErrorf("Index Meilisearch tidak punya UpdateSettings(ctx, settings).")
	}
	if err := up.UpdateSettings(ctx, settings); err != nil {
		return IndexSettings{}, err
	}
	return settings, nil
}
