package datatable

import (
	"context"
	"fmt"
	"reflect"
)

// FilterType adalah jenis filter.
type FilterType string

const (
	FilterSelect    FilterType = "select"
	FilterMulti     FilterType = "multi"
	FilterDate      FilterType = "date"
	FilterDateRange FilterType = "dateRange"
)

// DateFormat adalah granularitas nilai filter tanggal.
//
// `time` (`HH:mm:ss`) dan `timeHm` (`HH:mm`) adalah jam dalam sehari
// (pengganti `time` / `time_hm` versi Laravel).
type DateFormat string

const (
	FormatDate     DateFormat = "date"
	FormatDatetime DateFormat = "datetime"
	FormatMonth    DateFormat = "month"
	FormatYear     DateFormat = "year"
	FormatTime     DateFormat = "time"
	FormatTimeHm   DateFormat = "timeHm"
)

// Operator perbandingan filter. `contains` hanya untuk filter select.
type Operator string

const (
	OpEq       Operator = "="
	OpNe       Operator = "!="
	OpGt       Operator = ">"
	OpGte      Operator = ">="
	OpLt       Operator = "<"
	OpLte      Operator = "<="
	OpContains Operator = "contains"
)

// Cast adalah tipe nilai di database untuk filter select/multi.
type Cast string

const (
	CastString  Cast = "string"
	CastNumber  Cast = "number"
	CastBoolean Cast = "boolean"
)

// Option adalah satu pilihan filter. Value: string, angka, atau bool.
type Option struct {
	Value any    `json:"value"`
	Label string `json:"label"`
}

// Opt adalah pembantu ringkas untuk membuat Option.
func Opt(value any, label string) Option { return Option{Value: value, Label: label} }

// FilterContext diterima loader opsi, predikat visibilitas, dan nilai
// dinamis (filter bertingkat).
type FilterContext struct {
	// Ctx adalah context request (boleh nil; pakai Context()).
	Ctx context.Context
	// Values: nilai filter aktif saat ini, dikunci berdasarkan id filter.
	Values map[string]FilterValue
}

// Context mengembalikan Ctx, atau context.Background() bila nil.
func (c FilterContext) Context() context.Context {
	if c.Ctx == nil {
		return context.Background()
	}
	return c.Ctx
}

// Value mengembalikan nilai filter id (nil bila tidak ada).
func (c FilterContext) Value(id string) FilterValue { return c.Values[id] }

// String mengembalikan nilai filter id bila berupa StringValue, selain itu "".
func (c FilterContext) String(id string) string {
	s, _ := c.Values[id].(StringValue)
	return string(s)
}

// Resolved adalah nilai filter setelah dinormalisasi & di-cast, sesuai tipe
// filternya — diteruskan ke Where/Match kustom.
type Resolved struct {
	// Scalar: select → string/float64/bool (sesuai cast); date → string.
	Scalar any
	// Values: multi → nilai yang sudah di-cast.
	Values []any
	// Exclude: multi → mode Kecuali (hanya bila AllowExclude).
	Exclude bool
	// From/To: dateRange → batas (nil = terbuka); sudah ditukar bila terbalik.
	From, To *string
}

// Filter adalah definisi filter. Buat dengan Select / Multi / Date /
// DateRange plus FilterOption. Satu definisi bisa dipakai engine server
// maupun memtable.
type Filter struct {
	ID    string
	Label string
	Type  FilterType
	// Field adalah kolom (json key) yang difilter; dot-path untuk relasi
	// (`kategori.nama`). Boleh kosong bila memakai Where / Match.
	Field string

	Options     []Option
	OptionsFunc func(FilterContext) ([]Option, error)

	// Operator: select (default "=") dan date (default "=").
	Operator Operator
	// OperatorFunc: operator dinamis filter date (pengganti
	// `setFilterDateBounds(..., condition)` versi Laravel).
	OperatorFunc func(FilterContext) Operator
	// Cast: tipe nilai di database. Default ditebak dari tipe Value opsi statis.
	Cast Cast

	// AllowExclude: multi — tampilkan toggle Termasuk/Kecuali (NOT IN).
	AllowExclude bool
	// SearchThreshold: multi — kotak cari opsi tampil bila jumlah opsi > n.
	// nil = tidak pernah. Default 5.
	SearchThreshold *int

	// Format: date/dateRange, default "date".
	Format DateFormat
	// Min/Max: batas pemilih tanggal; MinFunc/MaxFunc versi dinamis
	// (didahulukan). String kosong = tanpa batas.
	Min, Max         string
	MinFunc, MaxFunc func(FilterContext) string

	// Visible: tampil/sembunyi untuk filter bertingkat. nil = selalu tampil.
	Visible func(FilterContext) bool
	// DependsOn: id filter induk — opsi/visibilitas dihitung ulang saat
	// induknya berubah.
	DependsOn []string

	// Where adalah kondisi kustom khusus engine (mis. gormtable.WhereFunc),
	// diisi lewat WithWhere / gormtable.Where.
	Where any
	// Match adalah predikat kustom untuk memtable. row = elemen []T.
	Match func(row any, v Resolved) bool
}

// FilterOption mengubah definisi filter.
type FilterOption func(*Filter)

// Field mengatur kolom yang difilter.
func Field(field string) FilterOption { return func(f *Filter) { f.Field = field } }

// Options mengatur opsi statis.
func Options(opts ...Option) FilterOption {
	return func(f *Filter) {
		if opts == nil {
			opts = []Option{}
		}
		f.Options = opts
	}
}

// OptionsFrom mengatur loader opsi dinamis (dipanggil saat describe).
func OptionsFrom(fn func(FilterContext) ([]Option, error)) FilterOption {
	return func(f *Filter) { f.OptionsFunc = fn }
}

// Op mengatur operator statis.
func Op(op Operator) FilterOption { return func(f *Filter) { f.Operator = op } }

// OpFunc mengatur operator dinamis (filter date).
func OpFunc(fn func(FilterContext) Operator) FilterOption {
	return func(f *Filter) { f.OperatorFunc = fn }
}

// CastAs mengatur tipe nilai di database.
func CastAs(c Cast) FilterOption { return func(f *Filter) { f.Cast = c } }

// AllowExclude menyalakan toggle Termasuk/Kecuali (multi).
func AllowExclude() FilterOption { return func(f *Filter) { f.AllowExclude = true } }

// SearchThreshold mengatur ambang kotak cari opsi (multi).
func SearchThreshold(n int) FilterOption { return func(f *Filter) { f.SearchThreshold = &n } }

// NoSearch: kotak cari opsi tidak pernah tampil (searchThreshold null).
func NoSearch() FilterOption { return func(f *Filter) { f.SearchThreshold = nil } }

// Format mengatur granularitas tanggal.
func Format(df DateFormat) FilterOption { return func(f *Filter) { f.Format = df } }

// Min mengatur batas bawah statis pemilih tanggal.
func Min(s string) FilterOption { return func(f *Filter) { f.Min = s } }

// Max mengatur batas atas statis pemilih tanggal.
func Max(s string) FilterOption { return func(f *Filter) { f.Max = s } }

// MinFunc mengatur batas bawah dinamis (pengganti `setFilterDateBounds`).
func MinFunc(fn func(FilterContext) string) FilterOption {
	return func(f *Filter) { f.MinFunc = fn }
}

// MaxFunc mengatur batas atas dinamis.
func MaxFunc(fn func(FilterContext) string) FilterOption {
	return func(f *Filter) { f.MaxFunc = fn }
}

// VisibleWhen mengatur predikat visibilitas (filter bertingkat).
func VisibleWhen(fn func(FilterContext) bool) FilterOption {
	return func(f *Filter) { f.Visible = fn }
}

// Hidden menyembunyikan filter secara permanen (visible: false).
func Hidden() FilterOption {
	return func(f *Filter) { f.Visible = func(FilterContext) bool { return false } }
}

// DependsOn mengatur id filter induk.
func DependsOn(ids ...string) FilterOption {
	return func(f *Filter) {
		if ids == nil {
			ids = []string{}
		}
		f.DependsOn = ids
	}
}

// MatchFunc mengatur predikat kustom memtable.
func MatchFunc(fn func(row any, v Resolved) bool) FilterOption {
	return func(f *Filter) { f.Match = fn }
}

// WithWhere mengatur kondisi kustom engine (dipakai paket engine, mis.
// gormtable.Where).
func WithWhere(where any) FilterOption { return func(f *Filter) { f.Where = where } }

// NewFilter membangun dan memvalidasi definisi filter. Kesalahan selalu
// bertipe *FilterDefinitionError.
func NewFilter(typ FilterType, id, label string, opts ...FilterOption) (Filter, error) {
	f := Filter{ID: id, Label: label, Type: typ}
	switch typ {
	case FilterSelect:
		f.Operator = OpEq
	case FilterMulti:
		n := 5
		f.SearchThreshold = &n
	case FilterDate:
		f.Format, f.Operator = FormatDate, OpEq
	case FilterDateRange:
		f.Format = FormatDate
	default:
		return Filter{}, defErr("Filter %q: tipe %q tidak dikenal (select, multi, date, dateRange).", id, typ)
	}
	for _, o := range opts {
		if o != nil {
			o(&f)
		}
	}
	if f.Field == "" && isNilish(f.Where) && f.Match == nil {
		return Filter{}, defErr("Filter %q butuh salah satu dari: field, where, atau match.", id)
	}
	if err := f.validate(); err != nil {
		return Filter{}, err
	}
	return f, nil
}

func (f Filter) validate() error {
	switch f.Type {
	case FilterSelect:
		if f.Operator != "" && f.Operator != OpContains && !isCompareOp(f.Operator) {
			return defErr("Filter %q: operator %q tidak valid.", f.ID, f.Operator)
		}
	case FilterDate:
		if f.Operator != "" && !isCompareOp(f.Operator) {
			return defErr("Filter %q: operator %q tidak valid untuk filter tanggal.", f.ID, f.Operator)
		}
	}
	switch f.Cast {
	case "", CastString, CastNumber, CastBoolean:
	default:
		return defErr("Filter %q: cast %q tidak valid (string, number, boolean).", f.ID, f.Cast)
	}
	if f.Type == FilterDate || f.Type == FilterDateRange {
		switch f.Format {
		case "", FormatDate, FormatDatetime, FormatMonth, FormatYear, FormatTime, FormatTimeHm:
		default:
			return defErr("Filter %q: format %q tidak valid.", f.ID, f.Format)
		}
	}
	return nil
}

func defErr(format string, args ...any) *FilterDefinitionError {
	return &FilterDefinitionError{Msg: fmt.Sprintf(format, args...)}
}

func isNilish(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Func, reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Chan:
		return rv.IsNil()
	}
	return false
}

func isCompareOp(op Operator) bool {
	switch op {
	case OpEq, OpNe, OpGt, OpGte, OpLt, OpLte:
		return true
	}
	return false
}

func mustFilter(f Filter, err error) Filter {
	if err != nil {
		panic(err)
	}
	return f
}

// Select membuat filter pilihan tunggal. Panic (*FilterDefinitionError) bila
// definisinya tidak valid — definisi filter adalah kode developer.
//
//	datatable.Select("status", "Status", datatable.Field("status"), datatable.Options(STATUS...))
func Select(id, label string, opts ...FilterOption) Filter {
	return mustFilter(NewFilter(FilterSelect, id, label, opts...))
}

// Multi membuat filter multi-pilihan (IN / NOT IN).
func Multi(id, label string, opts ...FilterOption) Filter {
	return mustFilter(NewFilter(FilterMulti, id, label, opts...))
}

// Date membuat filter satu tanggal dengan operator perbandingan.
func Date(id, label string, opts ...FilterOption) Filter {
	return mustFilter(NewFilter(FilterDate, id, label, opts...))
}

// DateRange membuat filter rentang tanggal.
func DateRange(id, label string, opts ...FilterOption) Filter {
	return mustFilter(NewFilter(FilterDateRange, id, label, opts...))
}

// FilterTypes memetakan id → tipe, untuk ParseOptions.FilterTypes.
func FilterTypes(filters []Filter) map[string]FilterType {
	out := make(map[string]FilterType, len(filters))
	for _, f := range filters {
		out[f.ID] = f.Type
	}
	return out
}

// EffectiveFormat mengembalikan format tanggal efektif (default "date").
func (f Filter) EffectiveFormat() DateFormat {
	if f.Format == "" {
		return FormatDate
	}
	return f.Format
}

// EffectiveOperator mengembalikan operator efektif untuk nilai filter saat
// ini: date memakai OperatorFunc bila ada (hasil tidak valid → "="),
// select memakai Operator; default "=".
func (f Filter) EffectiveOperator(fc FilterContext) Operator {
	switch f.Type {
	case FilterDate:
		op := f.Operator
		if f.OperatorFunc != nil {
			op = f.OperatorFunc(fc)
		}
		if isCompareOp(op) {
			return op
		}
	case FilterSelect:
		if f.Operator != "" {
			return f.Operator
		}
	}
	return OpEq
}

// EffectiveCast mengembalikan cast efektif select/multi: Cast eksplisit, atau
// ditebak dari tipe Value opsi statis pertama. "" = tanpa cast.
func (f Filter) EffectiveCast() Cast {
	if f.Type != FilterSelect && f.Type != FilterMulti {
		return ""
	}
	if f.Cast != "" {
		return f.Cast
	}
	if f.OptionsFunc != nil || len(f.Options) == 0 {
		return ""
	}
	switch Primitive(f.Options[0].Value).(type) {
	case float64:
		return CastNumber
	case bool:
		return CastBoolean
	}
	return CastString
}

// CastValue mengubah nilai mentah sesuai cast: number → float64 (bila
// valid, selain itu string apa adanya), boolean → "true"/"1".
func CastValue(raw string, c Cast) any {
	switch c {
	case CastNumber:
		if n, ok := ParseNumber(raw); ok {
			return n
		}
		return raw
	case CastBoolean:
		return raw == "true" || raw == "1"
	}
	return raw
}

// ResolveValue mengubah nilai mentah (dari URL) menjadi nilai siap pakai
// sesuai tipe filter. ok=false bila kosong atau bentuknya tidak cocok —
// artinya filter tidak diterapkan. Rentang yang terbalik (to < from)
// otomatis ditukar.
func ResolveValue(f Filter, v FilterValue) (r Resolved, ok bool) {
	if IsEmpty(v) {
		return Resolved{}, false
	}
	cast := f.EffectiveCast()
	switch f.Type {
	case FilterSelect, FilterDate:
		s, isStr := v.(StringValue)
		if !isStr {
			return Resolved{}, false
		}
		if f.Type == FilterSelect {
			return Resolved{Scalar: CastValue(string(s), cast)}, true
		}
		return Resolved{Scalar: string(s)}, true
	case FilterMulti:
		mv, isMulti := v.(MultiValue)
		if !isMulti {
			return Resolved{}, false
		}
		vals := make([]any, len(mv.Values))
		for i, x := range mv.Values {
			vals[i] = CastValue(x, cast)
		}
		return Resolved{Values: vals, Exclude: f.AllowExclude && mv.Exclude}, true
	case FilterDateRange:
		// Seperti JS: nilai berbentuk lain menghasilkan rentang tanpa batas.
		rv, _ := v.(RangeValue)
		from, to := nonEmpty(rv.From), nonEmpty(rv.To)
		if from != nil && to != nil && *from > *to {
			from, to = to, from
		}
		return Resolved{From: from, To: to}, true
	}
	return Resolved{}, false
}

func nonEmpty(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	v := *s
	return &v
}
