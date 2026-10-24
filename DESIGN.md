# MrCatz DataTable for Go — design contract

Go port of [`mrcatz/datatable`](https://github.com/mrc4tz/mrcatz-datatables) (Laravel) following the
JavaScript port [`@mrcatz/datatable-*`](https://github.com/mrc4tz/mrcatz-js-datatables) as the blueprint
(`/Users/ryann.r/MyData/sites/mrcatz-js-datatable`). Stack: **Gin + GORM + reuse `@mrcatz/datatable-vue`**.

**Rule #1 — wire compatibility.** The HTTP protocol (query string in, JSON out, 400 errors) is byte-for-byte
the same as `@mrcatz/datatable-server`, so the existing Vue package works unchanged against a Go API.
The JS tests (`packages/core/test`, `packages/server/test`) are the conformance suite — port their cases as
table-driven Go tests.

Code comments are in **Indonesian** (like the JS port). Exported identifiers are idiomatic English Go.
Module: `github.com/mrc4tz/mrcatz-go-datatables`, `go 1.23`, generics where they help (`Page[T]`, `Table[T]`).

## Packages

| Package (dir) | JS equivalent | Contents |
|---|---|---|
| `datatable` | `@mrcatz/datatable-core` | Protocol types, `ParseQuery`/`FormatQuery`, filter definitions (`Select`, `Multi`, `Date`, `DateRange` + options), `DescribeFilters`, search config + `Highlight` + trigrams, date helpers (`PeriodOf`, `RangeBounds`, `CompareBounds`, `ParseTimeOfDay`), CSV (`ToCSV`), errors |
| `memtable` | `createMemoryTable` | In-memory table over `[]T` (reflection on json tags / dot paths) — used by tests & small apps |
| `gormtable` | `defineDataTable` (engine, prisma-where, relevance, search-driver) | `Define[T]` → `*Table[T]` with `Query`, `Describe`, `ExportRows`, `Scope`; PostgreSQL relevance (joins, `pg_trgm`); `SearchDriver` interface |
| `meili` | `@mrcatz/datatable-server/meilisearch` | `Driver(...)` implementing `gormtable.SearchDriver`, filter pushdown, `SyncSettings` |
| `httpdt` | `/nest` helpers | net/http: `Values(r)`, `WriteError(w, err)`, `Handler`s, error JSON |
| `gindt` | `/nest` helpers | Gin: `ErrorMiddleware()`, `Register(group, table)`, `List/Schema/Export` handlers |
| `editorimg` | `editorImages()` | Upload → tmp, `ProcessHTML`, `CleanupTmp`, `Storage` interface + `LocalDisk` |
| `cmd/mrcatz` | `mrcatz` CLI | `mrcatz make|remove <Name>` scaffolding (Go table + Gin handlers + Nuxt page) |

## Wire protocol (identical to JS)

Query params (optional `{prefix}` namespace, e.g. `users_`):

```
search=budi%20santoso
page=2&limit=25
sort=name:asc,createdAt:desc          (multi-sort, order = priority)
f.status=aktif                         (select / date)
f.kategori=1&f.kategori=3              (multi)  +  f.kategori.not=1 (exclude mode)
f.dibuat.from=2026-01-01&f.dibuat.to=2026-01-31   (dateRange)
```

- Unknown keys are ignored; `page < 1` → 1; `limit` missing → `DefaultLimit` (10); `limit > MaxLimit` (100) → clamped.
- Responses:
  - list: `{"data": [...], "meta": {"total": 92, "page": 1, "limit": 10, "totalPages": 10, "hasMore": true}}`
  - schema: `{"filters": [FilterSchema...]}` — same JSON field names/omitempty behaviour as JS `FilterSchema`
  - export: JSON array of rows
- Errors from user input (`*datatable.QueryError`: unknown sort key, invalid date…) → HTTP **400** with Nest's shape:
  `{"statusCode": 400, "message": "<msg>", "error": "Bad Request"}`.
- Row JSON comes from the user's model `json` tags; sort/filter/search keys are the **json names** the client sees
  (camelCase), mapped to DB columns through the GORM schema (see `gormtable` below).

## `datatable` (core) — API

Implemented (see the package for full doc comments). `Field`, `Options`, … are `FilterOption` constructors.

```go
// --- protocol types (types.go) ---
type SortDir string // Asc = "asc", Desc = "desc"
type SortRule struct { Key string `json:"key"`; Dir SortDir `json:"dir"` }

// FilterValue is one of: StringValue, RangeValue, MultiValue.
type FilterValue interface{ isFilterValue() }
type StringValue string
type RangeValue struct { From, To *string }               // json {"from":..,"to":..} (nil omitted)
type MultiValue struct { Values []string; Exclude bool }  // json {"values":[..],"exclude":bool}
func Str(s string) *string; func Range(from, to string) RangeValue // "" = open side
func In(values ...string) MultiValue; func NotIn(values ...string) MultiValue
func UnmarshalFilterValue(data []byte) (FilterValue, error) // JS shapes; numbers/bools → string; null → nil
func UnmarshalFilters(data []byte) (map[string]FilterValue, error)
func IsEmpty(v FilterValue) bool // "0" counts as filled

type TableQuery struct { Page, Limit int; Search string; Sort []SortRule; Filters map[string]FilterValue } // custom (Un)MarshalJSON
type PageMeta struct { Total int64 `json:"total"`; Page int `json:"page"`; Limit int `json:"limit"`; TotalPages int `json:"totalPages"`; HasMore bool `json:"hasMore"` }
type Page[T any] struct { Data []T `json:"data"`; Meta PageMeta `json:"meta"` } // nil Data → []
func NewMeta(total int64, page, limit int) PageMeta // totalPages ≥ 1, hasMore = page*limit < total

// --- query string (query.go) ---
const DefaultLimit = 10; const MaxLimit = 100
type ParseOptions struct { Prefix string; DefaultLimit, MaxLimit int; DefaultSort []SortRule; FilterTypes map[string]FilterType }
func ParseQuery(v url.Values, opts ParseOptions) TableQuery // never fails (same as JS); pass FilterTypes(filters)!
func FormatQuery(q TableQuery, prefix string) url.Values    // page 1 & empty filters omitted
func ParseSort(raw string) []SortRule                       // never fails; non-"desc" → asc
func FormatSort([]SortRule) string
func IsTableParam(key, prefix string) bool

// --- filters (filters.go, describe.go) ---
type FilterType string // FilterSelect | FilterMulti | FilterDate | FilterDateRange
type DateFormat string // FormatDate | FormatDatetime | FormatMonth | FormatYear | FormatTime | FormatTimeHm
type Operator string   // OpEq "=" OpNe "!=" OpGt OpGte OpLt OpLte OpContains "contains"
type Cast string       // CastString | CastNumber | CastBoolean
type Option struct { Value any `json:"value"`; Label string `json:"label"` }; func Opt(value any, label string) Option

type FilterContext struct { Ctx context.Context; Values map[string]FilterValue }
func (FilterContext) Context() context.Context // Background if nil
func (FilterContext) Value(id string) FilterValue
func (FilterContext) String(id string) string  // StringValue or ""

// Resolved = value after normalisation + cast, passed to custom Where/Match.
type Resolved struct {
    Scalar   any      // select: string|float64|bool (per cast); date: string
    Values   []any    // multi (cast)
    Exclude  bool     // multi (only when AllowExclude)
    From, To *string  // dateRange (nil = open; swapped when reversed)
}

type Filter struct {
    ID, Label string; Type FilterType; Field string
    Options []Option; OptionsFunc func(FilterContext) ([]Option, error)
    Operator Operator; OperatorFunc func(FilterContext) Operator; Cast Cast
    AllowExclude bool; SearchThreshold *int          // multi (default 5; nil = never → JSON null)
    Format DateFormat; Min, Max string; MinFunc, MaxFunc func(FilterContext) string // "" = no bound
    Visible func(FilterContext) bool; DependsOn []string                            // nil Visible = visible
    Where any   // engine-specific (gormtable.WhereFunc), set via gormtable.Where(...) / WithWhere
    Match func(row any, v Resolved) bool // memtable; row is the T element
}
type FilterOption func(*Filter)
func NewFilter(typ FilterType, id, label string, opts ...FilterOption) (Filter, error) // *FilterDefinitionError
func Select(id, label string, opts ...FilterOption) Filter   // panics (*FilterDefinitionError) on invalid def
func Multi(id, label string, opts ...FilterOption) Filter
func Date(id, label string, opts ...FilterOption) Filter
func DateRange(id, label string, opts ...FilterOption) Filter
// options: Field, Options, OptionsFrom, Op, OpFunc, CastAs, AllowExclude, SearchThreshold, NoSearch,
//          Format, Min, Max, MinFunc, MaxFunc, VisibleWhen, Hidden, DependsOn, MatchFunc, WithWhere(any)
// validation: needs Field|Where|Match; known type; valid operator (date: no "contains"), cast, format.
func FilterTypes(filters []Filter) map[string]FilterType
func (Filter) EffectiveFormat() DateFormat              // default "date"
func (Filter) EffectiveOperator(fc FilterContext) Operator // date: OperatorFunc wins (invalid → "="); select: Operator
func (Filter) EffectiveCast() Cast                      // explicit or inferred from first static option ("" = none)
func CastValue(raw string, c Cast) any                  // number → float64 (JS Number rules) or raw; boolean → "true"/"1"
func ResolveValue(f Filter, v FilterValue) (Resolved, bool) // false = empty/wrong shape → filter not applied
func ResolveOptions(f Filter, fc FilterContext) ([]Option, error)
func DescribeFilters(fc FilterContext, filters []Filter) (Schema, error) // hidden filters don't load options
type FilterSchema struct { ID, Label; Type; Visible bool; DependsOn []string; Options []Option; AllowExclude bool;
    SearchThreshold *int; Format DateFormat; Min, Max string } // MarshalJSON = JS key set/order per type
type Schema struct { Filters []FilterSchema `json:"filters"` }

// --- search (search.go, searchconfig.go) ---
type SearchMode string // ModeContains | ModeStartsWith | ModeEquals
type ColumnType string // ColumnString "string" | ColumnNumber "number"
type SearchColumn struct { Field, Column string; Weight int; Mode SearchMode; Type ColumnType }
func Col(field string) SearchColumn // .W(3) .StartsWith() .Equals() .Numeric() .As("db_col")
type TypoTolerance struct { Driver string /* TypoTrigram (default "") | TypoPgTrgm */; MinWordLength int /* 0 → 4 */ }
type SearchConfig struct { Columns []SearchColumn; Match string /* MatchAll (default) | MatchAny */; TypoTolerance *TypoTolerance /* nil = off */ }
func (*SearchConfig) Validate() error // *ConfigError
type NormalizedSearch struct { Columns []SearchColumn; Match string; Typo NormalizedTypo{Driver /* "none"|... */; MinWordLength} }
func NormalizeSearch(cfg *SearchConfig) NormalizedSearch
func SplitKeywords(s string) []string
func Trigrams(word string) []string; func NGrams(word string, size int) []string
type Segment struct { Text string `json:"text"`; Match bool `json:"match"` }
func Highlight(text, search string) []Segment; func HighlightWords(text string, words []string) []Segment

// --- dates (dates.go) — half-open windows ---
type UTCOffset string // "+08:00", "-0530", "Z"; "" = time.Local
func (UTCOffset) Location() (*time.Location, error)
type Period struct { Start, End time.Time }                     // [Start, End)
type Bounds struct { GTE, LT *time.Time; Not *Period }          // Not = outside period (op "!=")
func (Bounds) Contains(t time.Time) bool; func (Bounds) IsZero() bool
func PeriodOf(value string, f DateFormat, off UTCOffset) (Period, error)
func CompareBounds(p Period, op Operator) Bounds
func RangeBounds(from, to *string, f DateFormat, off UTCOffset) (Bounds, error)
func FilterBounds(f Filter, v Resolved, fc FilterContext, off UTCOffset) (Bounds, error) // date/dateRange helper
func IsTimeFormat(f DateFormat) bool
func ParseTimeOfDay(s string) (seconds int, err error)
func TimeOfDay(t time.Time, off UTCOffset) (time.Time, error) // → 1970-01-01 UTC
// time/timeHm periods live on 1970-01-01 UTC and ignore the offset.

// --- CSV & JS-value helpers ---
func ToCSV(headers []string, rows [][]any) string // BOM + CRLF + formula-injection guard (numbers untouched)
type CSVOptions struct { Delimiter string; NoBOM bool }; func ToCSVWith(headers []string, rows [][]any, o CSVOptions) string
func ParseNumber(s string) (float64, bool) // JS Number(s)
func FormatNumber(f float64) string        // JS String(n)
func FormatValue(v any) string             // JS String(v ?? ''); time → ISO
func ISOString(t time.Time) string
func Primitive(v any) any                  // → nil|string|float64|bool|time.Time (derefs, driver.Valuer)

// --- errors ---
type QueryError struct{ Msg string; Err error } // Error(); Unwrap(); StatusCode() int { return 400 }
func QueryErrorf(format string, args ...any) *QueryError
var ErrInvalidDate error // wrapped by the *QueryError from PeriodOf/RangeBounds/ParseTimeOfDay/Location
type ConfigError struct{ Msg string };  func ConfigErrorf(format string, args ...any) *ConfigError
type FilterDefinitionError struct{ Msg string }
```

Engines should: parse with `FilterTypes(filters)`; call `ResolveValue` per filter; for date/dateRange use
`FilterBounds` (errors are already `*QueryError`, i.e. 400); `Bounds.Not` → `NOT (col >= start AND col < end)`.

## `memtable` — API

```go
type Options struct {
    Search *datatable.SearchConfig; Filters []datatable.Filter; DefaultSort []datatable.SortRule
    UTCOffset datatable.UTCOffset; Prefix string; DefaultLimit, MaxLimit int
}
func New[T any](rows []T, opts Options) *Table[T]           // panics *datatable.ConfigError (dup filter id, bad search/offset)
func NewFunc[T any](rows func() []T, opts Options) *Table[T] // rows re-read per call
func (t *Table[T]) Parse(v url.Values) datatable.TableQuery
func (t *Table[T]) Query(ctx context.Context, v url.Values) (*datatable.Page[T], error)
func (t *Table[T]) QueryParsed(ctx context.Context, q datatable.TableQuery) (*datatable.Page[T], error)
func (t *Table[T]) ExportRows(ctx context.Context, v url.Values) ([]T, error)
func (t *Table[T]) All(ctx context.Context, q datatable.TableQuery) ([]T, error)
func (t *Table[T]) Count(ctx context.Context, q datatable.TableQuery) (int, error)
func (t *Table[T]) Describe(ctx context.Context, v url.Values) (datatable.Schema, error)
func (t *Table[T]) DescribeWith(fc datatable.FilterContext) (datatable.Schema, error)
func Lookup(row any, path string) any // json-tag dot paths (embedded, pointers, maps, "items.0")
```

Same method shapes as `gormtable.Table[T]` (`Query`/`Describe`/`ExportRows`), so it satisfies the HTTP
adapters' `Querier`. Semantics follow `createMemoryTable` (relevance first when searching without explicit
sort, `DefaultSort` as tiebreak; loose `==` so `'1'` matches `1`). Go-specific choices: a nil cell only
matches `!=` (SQL-like); naive datetime strings in cells use `UTCOffset`; time vs date-string compares
as instants; invalid date input returns `*QueryError` even with zero rows.

## `gormtable` — API

```go
type WhereFunc func(db *gorm.DB, v datatable.Resolved, fc datatable.FilterContext) *gorm.DB
func Where(fn WhereFunc) datatable.FilterOption // custom where (= createWithCallback)

type Definition[T any] struct {
    DB        *gorm.DB
    BaseWhere func(ctx context.Context, db *gorm.DB) *gorm.DB // scoping (= baseQuery)
    Joins     []string   // GORM association joins needed for dot-path keys, e.g. "Category"
    Preload   []string
    Select    []string
    Search    *datatable.SearchConfig
    Relevance *Relevance   // PostgreSQL ranking
    Driver    SearchDriver // e.g. meili.Driver(...); not combinable with Relevance
    Filters   []datatable.Filter
    Sortable  []string     // whitelist of json keys
    DefaultSort []datatable.SortRule
    Tiebreaker  []datatable.SortRule // default: primary key asc; empty slice + NoTiebreaker to disable
    NoTiebreaker bool
    PrimaryKey   string    // json key, default from schema
    DefaultLimit, MaxLimit, ExportLimit int // 10, 100, 10000
    Prefix          string
    CaseInsensitive *bool  // default true (ILIKE on postgres, LOWER() LIKE elsewhere)
    UTCOffset       datatable.UTCOffset
}
type Relevance struct { Table string; Joins []RelevanceJoin }
type RelevanceJoin struct { Table, Alias string; On []string; Type string /*"left"|"inner"*/ }

func Define[T any](def Definition[T]) (*Table[T], error) // *datatable.ConfigError on bad config
func MustDefine[T any](def Definition[T]) *Table[T]

func (t *Table[T]) Query(ctx context.Context, v url.Values) (*datatable.Page[T], error)
func (t *Table[T]) QueryParsed(ctx context.Context, q datatable.TableQuery) (*datatable.Page[T], error)
func (t *Table[T]) Describe(ctx context.Context, v url.Values) (datatable.Schema, error)
func (t *Table[T]) ExportRows(ctx context.Context, v url.Values) ([]T, error)
func (t *Table[T]) Scope(ctx context.Context, v url.Values) (*gorm.DB, error) // filtered db for bulk actions
func (t *Table[T]) Config() TableConfig // introspection for meili.SyncSettings / CLI

type SearchDriver interface {
    Name() string
    Search(ctx context.Context, req DriverRequest) (*DriverResult, error) // nil result = fall back to SQL search
}
```

Key mapping: json key → DB column via `schema.Parse(T)` (field `json` tag, fallback field name). Dot paths
(`category.name`) resolve through the association named in `Joins` (GORM alias `"Category"."name"`).
All identifiers are validated (`^[A-Za-z_][A-Za-z0-9_]*$` per segment) before reaching SQL.

## HTTP adapters

```go
// httpdt (net/http)
func Values(r *http.Request) url.Values
func WriteError(w http.ResponseWriter, err error) bool // true if handled (QueryError → 400 Nest shape)
func WriteJSON(w http.ResponseWriter, status int, v any)
type Querier interface { /* Query/Describe/ExportRows returning any */ }
func ListHandler(t Querier) http.HandlerFunc   // + SchemaHandler, ExportHandler

// gindt (Gin)
func ErrorMiddleware() gin.HandlerFunc           // maps c.Errors QueryError → 400 Nest shape
func List(t Querier) gin.HandlerFunc             // + Schema, Export
func Register(g gin.IRoutes, t Querier)          // GET "", "/schema", "/export"
```

## Laravel → Go naming (short)

| Laravel | JS | Go |
|---|---|---|
| `MrCatzDataTablesComponent` | `defineDataTable()` | `gormtable.Define[T](...)` |
| `baseQuery()` | `baseWhere` | `BaseWhere func(ctx, db) *gorm.DB` |
| `MrCatzDataTableFilter::create` | `filter.select` | `datatable.Select(id, label, datatable.Field(...), datatable.Options(...))` |
| `createWithCallback` | `where` | `gormtable.Where(func(db, v, fc) *gorm.DB)` |
| `setDefaultOrder` / `addOrderBy` | `defaultSort` / `tiebreaker` | `DefaultSort` / `Tiebreaker` |
| `configTable()` scoring / typo | `search.columns[].weight` / `typoTolerance` | `Search.Columns[].Weight` / `Search.TypoTolerance` |
| scout driver | `meilisearchDriver` | `meili.Driver` |
| `DataTableExceptionFilter` | — | `gindt.ErrorMiddleware()` / `httpdt.WriteError` |
| `php artisan mrcatz:make` | `npx mrcatz make` | `go run github.com/mrc4tz/mrcatz-go-datatables/cmd/mrcatz make` |
