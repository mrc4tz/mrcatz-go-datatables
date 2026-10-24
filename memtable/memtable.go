// Package memtable adalah engine in-memory MrCatz DataTable di atas []T —
// padanan `createMemoryTable` versi JS. Semantik filter/pencarian/sort sama
// dengan engine server; cocok untuk data kecil, demo, dan unit test.
//
// Kunci field/sort/search adalah nama json (dot-path untuk struct/map
// bersarang), dibaca lewat reflection (lihat Lookup).
package memtable

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
)

// Options mengatur tabel in-memory.
type Options struct {
	Search  *dt.SearchConfig
	Filters []dt.Filter
	// DefaultSort: urutan bawaan bila pengguna belum mengurutkan kolom.
	DefaultSort []dt.SortRule
	// UTCOffset: zona waktu untuk filter tanggal ("" = lokal proses).
	UTCOffset dt.UTCOffset
	// Prefix, DefaultLimit (10), MaxLimit (100): untuk parsing query string.
	Prefix       string
	DefaultLimit int
	MaxLimit     int
}

// Table adalah tabel in-memory atas baris bertipe T.
type Table[T any] struct {
	rows   func() []T
	opts   Options
	search dt.NormalizedSearch
	types  map[string]dt.FilterType
	loc    *time.Location
}

// New membuat tabel dari slice baris. Panic (*datatable.ConfigError) bila
// konfigurasi tidak valid (id filter duplikat, search/offset tidak valid).
func New[T any](rows []T, opts Options) *Table[T] {
	return NewFunc(func() []T { return rows }, opts)
}

// NewFunc membuat tabel dengan sumber baris dinamis (dipanggil tiap query).
func NewFunc[T any](rows func() []T, opts Options) *Table[T] {
	t, err := build(rows, opts)
	if err != nil {
		panic(err)
	}
	return t
}

func build[T any](rows func() []T, opts Options) (*Table[T], error) {
	if err := opts.Search.Validate(); err != nil {
		return nil, err
	}
	loc, err := opts.UTCOffset.Location()
	if err != nil {
		return nil, dt.ConfigErrorf("%s", err.Error())
	}
	seen := map[string]bool{}
	for _, f := range opts.Filters {
		if seen[f.ID] {
			return nil, dt.ConfigErrorf("Id filter duplikat: %q", f.ID)
		}
		seen[f.ID] = true
	}
	return &Table[T]{
		rows:   rows,
		opts:   opts,
		search: dt.NormalizeSearch(opts.Search),
		types:  dt.FilterTypes(opts.Filters),
		loc:    loc,
	}, nil
}

// Parse mem-parse query string memakai prefix, batas limit, dan tipe filter
// tabel ini.
func (t *Table[T]) Parse(v url.Values) dt.TableQuery {
	return dt.ParseQuery(v, dt.ParseOptions{
		Prefix:       t.opts.Prefix,
		DefaultLimit: t.opts.DefaultLimit,
		MaxLimit:     t.opts.MaxLimit,
		FilterTypes:  t.types,
	})
}

// Query mem-parse query string lalu mengembalikan satu halaman.
func (t *Table[T]) Query(ctx context.Context, v url.Values) (*dt.Page[T], error) {
	return t.QueryParsed(ctx, t.Parse(v))
}

// QueryParsed mengembalikan satu halaman untuk TableQuery yang sudah jadi.
// Page < 1 → 1; limit < 1 → DefaultLimit; limit > MaxLimit dipangkas.
// Tanggal tidak valid → *datatable.QueryError.
func (t *Table[T]) QueryParsed(ctx context.Context, q dt.TableQuery) (*dt.Page[T], error) {
	q = t.normalize(q)
	items, err := t.sorted(ctx, q)
	if err != nil {
		return nil, err
	}
	total := len(items)
	start := min((q.Page-1)*q.Limit, total)
	end := min(start+q.Limit, total)
	data := make([]T, 0, end-start)
	for _, it := range items[start:end] {
		data = append(data, it.row)
	}
	return &dt.Page[T]{Data: data, Meta: dt.NewMeta(int64(total), q.Page, q.Limit)}, nil
}

// All mengembalikan semua baris hasil filter + urutan (tanpa pagination).
func (t *Table[T]) All(ctx context.Context, q dt.TableQuery) ([]T, error) {
	items, err := t.sorted(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]T, len(items))
	for i, it := range items {
		out[i] = it.row
	}
	return out, nil
}

// ExportRows mem-parse query string lalu mengembalikan semua baris (export).
func (t *Table[T]) ExportRows(ctx context.Context, v url.Values) ([]T, error) {
	return t.All(ctx, t.Parse(v))
}

// Count menghitung baris yang lolos pencarian + filter.
func (t *Table[T]) Count(ctx context.Context, q dt.TableQuery) (int, error) {
	items, err := t.filtered(ctx, q)
	return len(items), err
}

// Describe menghitung schema filter untuk nilai filter di query string.
func (t *Table[T]) Describe(ctx context.Context, v url.Values) (dt.Schema, error) {
	return t.DescribeWith(dt.FilterContext{Ctx: ctx, Values: t.Parse(v).Filters})
}

// DescribeWith menghitung schema filter untuk konteks filter tertentu.
func (t *Table[T]) DescribeWith(fc dt.FilterContext) (dt.Schema, error) {
	if fc.Values == nil {
		fc.Values = map[string]dt.FilterValue{}
	}
	return dt.DescribeFilters(fc, t.opts.Filters)
}

func (t *Table[T]) normalize(q dt.TableQuery) dt.TableQuery {
	maxLimit := t.opts.MaxLimit
	if maxLimit <= 0 {
		maxLimit = dt.MaxLimit
	}
	defLimit := t.opts.DefaultLimit
	if defLimit <= 0 {
		defLimit = dt.DefaultLimit
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Limit < 1 {
		q.Limit = defLimit
	}
	q.Limit = min(q.Limit, maxLimit)
	return q
}

type item[T any] struct {
	row   T
	score int
	keys  []any
}

type activeFilter struct {
	f      dt.Filter
	v      dt.Resolved
	bounds dt.Bounds
}

func (t *Table[T]) filtered(ctx context.Context, q dt.TableQuery) ([]item[T], error) {
	fc := dt.FilterContext{Ctx: ctx, Values: q.Filters}
	if fc.Values == nil {
		fc.Values = map[string]dt.FilterValue{}
	}
	var active []activeFilter
	for _, f := range t.opts.Filters {
		v, ok := dt.ResolveValue(f, q.Filters[f.ID])
		if !ok {
			continue
		}
		a := activeFilter{f: f, v: v}
		if f.Match == nil && f.Field != "" && (f.Type == dt.FilterDate || f.Type == dt.FilterDateRange) {
			b, err := dt.FilterBounds(f, v, fc, t.opts.UTCOffset)
			if err != nil {
				return nil, err
			}
			a.bounds = b
		}
		active = append(active, a)
	}

	words := dt.SplitKeywords(q.Search)
	useSearch := len(words) > 0 && len(t.search.Columns) > 0
	out := []item[T]{}
rows:
	for _, row := range t.rows() {
		for _, a := range active {
			if !t.applyFilter(row, a, fc) {
				continue rows
			}
		}
		if useSearch {
			hit := false
			if t.search.Match == dt.MatchAll {
				hit = true
				for _, w := range words {
					if !t.matchesWord(row, w) {
						hit = false
						break
					}
				}
			} else {
				for _, w := range words {
					if t.matchesWord(row, w) {
						hit = true
						break
					}
				}
			}
			if !hit {
				continue
			}
		}
		it := item[T]{row: row}
		if len(words) > 0 {
			it.score = t.scoreRow(row, words)
		}
		out = append(out, it)
	}
	return out, nil
}

func (t *Table[T]) sorted(ctx context.Context, q dt.TableQuery) ([]item[T], error) {
	items, err := t.filtered(ctx, q)
	if err != nil {
		return nil, err
	}
	rules := q.Sort
	if len(rules) == 0 {
		rules = t.opts.DefaultSort
	}
	// Seperti versi Laravel: tanpa sort eksplisit, skor relevansi didahulukan;
	// urutan bawaan hanya menjadi penentu bila skornya sama.
	byRelevance := len(q.Sort) == 0 && q.Search != ""
	if len(rules) == 0 && !byRelevance {
		return items, nil
	}
	for i := range items {
		items[i].keys = make([]any, len(rules))
		for j, r := range rules {
			items[i].keys[j] = sortValue(dt.Primitive(Lookup(items[i].row, r.Key)))
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if byRelevance && a.score != b.score {
			return a.score > b.score
		}
		for k, r := range rules {
			c := compareSort(a.keys[k], b.keys[k])
			if c == 0 {
				continue
			}
			if r.Dir == dt.Desc {
				return c > 0
			}
			return c < 0
		}
		return false
	})
	return items, nil
}

var pureTimeRE = regexp.MustCompile(`^\d{2}:\d{2}(:\d{2})?$`)

// cellDate membaca sel sebagai waktu; untuk format jam diubah ke
// jam-dalam-sehari (1970-01-01 UTC).
func (t *Table[T]) cellDate(cell any, format dt.DateFormat) (time.Time, bool) {
	if dt.IsTimeFormat(format) {
		// Kolom jam murni ('14:30' / '14:30:15') dibaca apa adanya, tanpa zona waktu.
		if s, ok := cell.(string); ok && pureTimeRE.MatchString(strings.TrimSpace(s)) {
			secs, err := dt.ParseTimeOfDay(s)
			if err != nil {
				return time.Time{}, false
			}
			return time.Unix(int64(secs), 0).UTC(), true
		}
		d, ok := t.toDate(cell)
		if !ok {
			return time.Time{}, false
		}
		tod, err := dt.TimeOfDay(d, t.opts.UTCOffset)
		return tod, err == nil
	}
	return t.toDate(cell)
}

func (t *Table[T]) toDate(cell any) (time.Time, bool) {
	switch x := cell.(type) {
	case time.Time:
		return x, true
	case float64:
		return time.UnixMilli(int64(x)), true
	case string:
		return parseDate(x, t.loc)
	}
	return time.Time{}, false
}

func (t *Table[T]) applyFilter(row T, a activeFilter, fc dt.FilterContext) bool {
	f := a.f
	if f.Match != nil {
		return f.Match(row, a.v)
	}
	if f.Field == "" {
		return true // filter khusus engine lain (Where) diabaikan
	}
	cell := dt.Primitive(Lookup(row, f.Field))
	switch f.Type {
	case dt.FilterSelect:
		return compareOp(cell, f.EffectiveOperator(fc), a.v.Scalar, t.loc)
	case dt.FilterMulti:
		hit := false
		for _, v := range a.v.Values {
			if looseEqual(v, cell, t.loc) {
				hit = true
				break
			}
		}
		return hit != a.v.Exclude
	case dt.FilterDate, dt.FilterDateRange:
		d, ok := t.cellDate(cell, f.EffectiveFormat())
		return ok && a.bounds.Contains(d)
	}
	return true
}

var numericWordRE = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

func (t *Table[T]) fuzzy(word string) bool {
	return t.search.Typo.Driver != dt.TypoNone && utf8.RuneCountInString(word) >= t.search.Typo.MinWordLength
}

func (t *Table[T]) matchesWord(row T, word string) bool {
	lower := strings.ToLower(word)
	fuzzy := t.fuzzy(word)
	for _, col := range t.search.Columns {
		raw := dt.Primitive(Lookup(row, col.Field))
		if col.Type == dt.ColumnNumber {
			if raw != nil && numericWordRE.MatchString(word) && toNumber(raw) == toNumber(word) {
				return true
			}
			continue
		}
		value := strings.ToLower(dt.FormatValue(raw))
		switch col.Mode {
		case dt.ModeEquals:
			if value == lower {
				return true
			}
		case dt.ModeStartsWith:
			if strings.HasPrefix(value, lower) {
				return true
			}
		default:
			if strings.Contains(value, lower) {
				return true
			}
			if fuzzy {
				for _, g := range dt.Trigrams(word) {
					if strings.Contains(value, g) {
						return true
					}
				}
			}
		}
	}
	return false
}

// scoreRow menghitung skor relevansi: kecocokan persis bernilai bobot kolom
// (×100 bila toleransi salah ketik aktif, agar satu kecocokan persis selalu
// mengalahkan berapa pun bonus trigram), tiap trigram yang cocok +1.
func (t *Table[T]) scoreRow(row T, words []string) int {
	fuzzyOn := t.search.Typo.Driver != dt.TypoNone
	score := 0
	for _, word := range words {
		lower := strings.ToLower(word)
		grams := []string(nil)
		if t.fuzzy(word) {
			grams = dt.Trigrams(word)
		}
		for _, col := range t.search.Columns {
			value := strings.ToLower(dt.FormatValue(dt.Primitive(Lookup(row, col.Field))))
			if value == "" {
				continue
			}
			if strings.Contains(value, lower) {
				if fuzzyOn {
					score += col.Weight * 100
				} else {
					score += col.Weight
				}
			}
			for _, g := range grams {
				if strings.Contains(value, g) {
					score++
				}
			}
		}
	}
	return score
}
