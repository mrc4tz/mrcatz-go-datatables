# Migrating from mrcatz/datatable (Laravel)

Every Laravel method, property and config key mapped to its Go name — with the JavaScript name alongside, since the Go port
follows the JS port ([`@mrcatz/datatable-*`](https://github.com/mrc4tz/mrcatz-js-datatables)) and talks to the same frontend.
The naming stays consistent: **one `Definition[T]` struct** per table (fields instead of overridden methods),
**functional options** for filters (`datatable.Field(...)`, `datatable.Options(...)`), and **`json` tag names** as the keys
the client sorts, filters and searches by.

The Go port only replaces the **server side** (Livewire component → Gin / net/http API). Everything you see in the browser —
columns, the CRUD page, forms, bulk actions, expandable rows, export layouts, icons, locale — is the unchanged
[`@mrcatz/datatable-vue`](https://www.npmjs.com/package/@mrcatz/datatable-vue) package, so those rows are the same as in the
[JS MIGRATING.md](https://github.com/mrc4tz/mrcatz-js-datatables/blob/main/MIGRATING.md) (see [Frontend](#frontend) below).

For full examples of each item, open the matching page on [datatable.catzoid.tech](https://datatable.catzoid.tech/?stack=go) —
every page shows the Laravel, JavaScript and Go code side by side. Package names below are the import paths under
`github.com/mrc4tz/mrcatz-go-datatables/` (`datatable`, `gormtable`, `gindt`, `httpdt`, `meili`, `memtable`, `editorimg`).

> **BETA** — the Go API may still change before v1.

## Table definition

| Laravel (`MrCatzDataTablesComponent`) | JS (`defineDataTable()`) | Go (`gormtable.Definition[T]`) |
|---|---|---|
| `MrCatzDataTablesComponent` (table class) | `defineDataTable({ model, ... })` | `gormtable.Define(gormtable.Definition[T]{DB: db, ...})` → `(*gormtable.Table[T], error)`, or `gormtable.MustDefine(...)` |
| Eloquent model | Prisma delegate (`model`) | GORM model struct `T` + `DB *gorm.DB` |
| `baseQuery()` | `baseWhere` | `BaseWhere: func(ctx context.Context, db *gorm.DB) *gorm.DB` |
| `->join()` / `->with()` in `baseQuery()` for relation columns | dot-path keys (`category.name`, nested Prisma relation filters); `include` for rows | `Joins: []string{"Category"}` (belongs-to / has-one) + dot-path keys `"category.name"`; `Preload` / `Select` for row queries |
| `configTable()` → `table_name`, `table_id` | `search.relevance.table`, `primaryKey` | `Relevance: &gormtable.Relevance{Table: "orders"}`, `PrimaryKey` (json key, default from the GORM schema) |
| `withColumn(..., sort: true)` | `sortable` | `Sortable: []string{"name", "createdAt", "category.name"}` (json keys; `nil` = any mapped column) |
| `setDefaultOrder()` / `addOrderBy()` | `defaultSort` / `tiebreaker` (`false` = none) | `DefaultSort` / `Tiebreaker` (`NoTiebreaker: true` = none) — `[]datatable.SortRule{{Key: "createdAt", Dir: datatable.Desc}}` |
| `getRowPerPageOption()` / `$p` | `defaultLimit`, `maxLimit` | `DefaultLimit` (10), `MaxLimit` (100) |
| `setPageName()` / `urlPrefix()` | `prefix` | `Prefix: "orders_"` |
| export row limit | `exportLimit` | `ExportLimit` (10 000) |
| case-insensitive `LIKE` | `caseInsensitive` | `CaseInsensitive *bool` (default true: `ILIKE` on PostgreSQL, `LOWER(col) LIKE` elsewhere) |
| `config('app.timezone')` for date filters | `utcOffset` | `UTCOffset: "+07:00"` (`""` = `time.Local`) |
| Livewire render → paginated rows | `table.query(req.query)` | `table.Query(ctx, url.Values)` → `*datatable.Page[T]` (`Data`, `Meta`) |
| filter schema for the view | `table.describe(query)` | `table.Describe(ctx, url.Values)` → `datatable.Schema` |
| `beforeExport()` rows | `table.exportRows(query)` | `table.ExportRows(ctx, url.Values)` → `[]T` |
| `processBulkActionData()` on the filtered query | `table.where(query)` | `table.Scope(ctx, url.Values)` → `*gorm.DB` (no order / limit), e.g. `scope.Select("orders.id")` as a subquery |
| — (Collection-based tables) | `createMemoryTable(rows, opts)` | `memtable.New(rows, memtable.Options{...})` / `memtable.NewFunc(func() []T, ...)` |
| invalid configuration exception | `DataTableConfigError` | `*datatable.ConfigError` (returned by `Define`, panics in `MustDefine`) |
| invalid user input | `DataTableQueryError` (→ 400) | `*datatable.QueryError` (→ 400) |

## Search

| Laravel (`configTable()` / `$typeSearch`) | JS (`search`) | Go (`datatable.SearchConfig`) |
|---|---|---|
| searchable columns | `search.columns: ['name', ...]` | `Search: &datatable.SearchConfig{Columns: []datatable.SearchColumn{datatable.Col("name")}}` |
| `scoring` (per-column weight) | `columns[].weight` | `datatable.Col("name").W(3)` |
| column match mode | `columns[].mode: 'startsWith' \| 'equals'` | `.StartsWith()` / `.Equals()` |
| numeric column | `columns[].type: 'number'` | `.Numeric()` (matches numeric keywords with `=`) |
| all / any keyword | `match: 'all' \| 'any'` | `Match: datatable.MatchAll` / `datatable.MatchAny` |
| `typo_tolerance` | `typoTolerance: { driver, minWordLength }` | `TypoTolerance: &datatable.TypoTolerance{Driver: datatable.TypoTrigram \| datatable.TypoPgTrgm, MinWordLength: 4}` |
| relevance ranking (PostgreSQL) | `search.relevance: { db, table }` | `Relevance: &gormtable.Relevance{Table, PrimaryKeyColumn, MaxCandidates}` |
| `scoring` on joined columns (`customers.name`) | `search.relevance.joins` + `column: 'alias.column'` | `Relevance.Joins: []gormtable.RelevanceJoin{{Table: "customers", Alias: "c", On: []string{"c.id = orders.customer_id"}}}` + `datatable.Col("customer").As("c.name")` |
| `setSearchWord()` | `highlight()` / `<MrcHighlight>` | client: `<MrcHighlight>`; server: `datatable.Highlight(text, search)` → `[]datatable.Segment` |
| `search.driver = 'scout'` + `scout_model` | `search.driver: meilisearchDriver({ client, index })` | `Driver: meili.MustDriver(meili.Options{Client, Index: "orders"})` (any `gormtable.SearchDriver`) |
| `scout_max_results`, `filter_pushdown`, `date_field_map` | `maxResults`, `filterPushdown`, `dateFields` | `MaxResults`, `FilterPushdown: meili.PushdownAuto \| PushdownAlways \| PushdownNever`, `DateFields: map[string]string{"createdAt": "createdAtTs"}` |
| — | `onFallback` | `OnFallback: func(filterID, reason string)` |
| `mrcatz:meilisearch:configure` | `syncMeilisearchSettings(table, client?, index?)` | `meili.SyncSettings(ctx, table, client, index, dryRun)` (`nil, ""` = the driver's own); `meili.Settings(table)` to inspect |
| `mrcatz:make-search-proxy` | not needed — your app indexes the documents | not needed — your app indexes the documents (id = primary key) |
| Scout's Meilisearch client | `meilisearch` npm package | any client wrapped in the small `meili.Client` / `meili.Index` interfaces (adapter for `meilisearch-go` in the package docs) |

## Filters

| Laravel (`MrCatzDataTableFilter`) | JS (`filter.*`) | Go (`datatable.*`) |
|---|---|---|
| `create($id, $label, $data, $value, $option, $key)` | `filter.select(id, label, { field, options })` | `datatable.Select(id, label, datatable.Field("status"), datatable.Options(...))` |
| `createWithCallback(..., $callback)` | `filter.select(id, label, { options, where })` | `datatable.Select(id, label, datatable.Options(...), gormtable.Where(func(db *gorm.DB, v datatable.Resolved, fc datatable.FilterContext) *gorm.DB { ... }))` |
| `createCheck(...)` / `createCheckWithCallback` | `filter.multi(id, label, { field \| where, options })` | `datatable.Multi(id, label, datatable.Field(...) \| gormtable.Where(...), datatable.Options(...))` |
| `->allowExclude()` | `allowExclude: true` | `datatable.AllowExclude()` (custom `Where` reads `v.Exclude`) |
| `->allowSearchWhen(5)` | `searchThreshold: 5` | `datatable.SearchThreshold(5)` / `datatable.NoSearch()` |
| `createDate(..., $format, $condition)` | `filter.date(id, label, { field, format, operator })` | `datatable.Date(id, label, datatable.Field(...), datatable.Format(datatable.FormatDate), datatable.Op(datatable.OpGte))` |
| `createDateRange(...)` | `filter.dateRange(id, label, { field, format })` | `datatable.DateRange(id, label, datatable.Field(...), datatable.Format(...))` |
| format `date` / `datetime` / `year` | `date` / `datetime` / `year` | `datatable.FormatDate` / `FormatDatetime` / `FormatYear` |
| format `month_year` | format `month` | `datatable.FormatMonth` |
| format `time` / `time_hm` | format `time` / `timeHm` (server: requires `where`) | `datatable.FormatTime` / `FormatTimeHm` (requires `gormtable.Where`; helpers `datatable.ParseTimeOfDay`, `datatable.PeriodOf`) |
| conditions `=`, `!=`, `>`, `>=`, `<`, `<=`, `like` | `operator` | `datatable.OpEq`, `OpNe`, `OpGt`, `OpGte`, `OpLt`, `OpLte`, `OpContains` (select only) |
| value cast | `cast: 'number' \| 'boolean'` | `datatable.CastAs(datatable.CastNumber \| datatable.CastBoolean)` (inferred from the first static option) |
| `setFilterDateBounds($id, min, max, condition)` / `clearFilterOverride()` | `min`, `max`, `operator` as functions `(ctx) => ...` | `datatable.Min("2024-01-01")` / `datatable.MinFunc(fn)`, `MaxFunc(fn)`, `OpFunc(fn)` with `fn(fc datatable.FilterContext)` |
| `whereIn` / `whereNotIn` conditions | Include / Exclude mode (`exclude`) | `IN` / `NOT IN`; wire value `datatable.In(...)` / `datatable.NotIn(...)` |
| `setFilterShow()` / `onFilterChanged()` | `visible: (ctx) => ...` + `dependsOn` | `datatable.VisibleWhen(func(fc datatable.FilterContext) bool)` + `datatable.DependsOn("status")`; `datatable.Hidden()` |
| `setFilterData()` | `options: (ctx) => ...` (recomputed from `dependsOn`) | `datatable.OptionsFrom(func(fc datatable.FilterContext) ([]datatable.Option, error))` |
| `$data`, `$value`, `$option` | `options: [{ value, label }]` | `datatable.Options(datatable.Opt(value, label), ...)` |
| current value of another filter | `ctx.values.status` | `fc.String("status")` / `fc.Value("status")`; request context via `fc.Context()` |
| — (Collection filter) | `match` (memory table) | `datatable.MatchFunc(func(row any, v datatable.Resolved) bool)` (`memtable` only) |

A custom filter that combines several of these:

```go
datatable.Multi("category", "Category",
	datatable.OptionsFrom(func(fc datatable.FilterContext) ([]datatable.Option, error) {
		return loadCategories(fc.Context(), fc.String("shop"))
	}),
	datatable.CastAs(datatable.CastNumber),
	datatable.AllowExclude(),
	datatable.DependsOn("shop"),
	gormtable.Where(func(db *gorm.DB, v datatable.Resolved, fc datatable.FilterContext) *gorm.DB {
		if v.Exclude {
			return db.Where("orders.category_id NOT IN ?", v.Values)
		}
		return db.Where("orders.category_id IN ?", v.Values)
	}),
)
```

## HTTP layer

| Laravel | JS (`@mrcatz/datatable-server/nest`) | Go (`gindt` / `httpdt`) |
|---|---|---|
| Livewire component endpoint | Nest controller with `table.query(...)` | `gindt.Register(group, table)` → `GET ""`, `/schema`, `/export`; net/http: `httpdt.Mount(mux, "/api/orders", table)` |
| — | `@DataTableParams()` | `gindt.Values(c)` / `httpdt.Values(r)` → `url.Values` |
| `DataTableExceptionFilter` (validation → 400) | `DataTableExceptionFilter` / `withBadRequest(fn)` | `gindt.ErrorMiddleware()` on the group / `httpdt.WriteError(w, err)` |
| individual endpoints | — | `gindt.List(table)`, `gindt.Schema(table)`, `gindt.Export(table)` (same in `httpdt`, plus `httpdt.WithErrorHandler`) |
| `abort(404, '...')` | `throw new NotFoundException('...')` | `gindt.Abort(c, http.StatusNotFound, "...")` (Nest-shaped body) |
| server error | Nest's default 500 | `gindt.InternalError(c, err)` / `httpdt.DefaultErrorHandler` (`{"statusCode":500,"message":"Internal server error"}`) |
| `response()->json()` | `res.json()` | `gindt.JSON(c, status, v)` / `httpdt.WriteJSON(w, status, v)` (no HTML escaping, like Express) |

## CRUD page, editor images & CLI

| Laravel | JS | Go |
|---|---|---|
| `saveData()` / `dropData()` / `dropBulkData()` | your Nest controller (`POST`, `PATCH /:id`, `DELETE /:id`, `POST /bulk-delete`) | your Gin handlers — `mrcatz make` generates all four in `handler.go` |
| `processEditorImages()` + `EditorImageUploadController` | `editorImages({ storage }).processHtml()` / `.upload()` | `editorimg.New(editorimg.Options{Storage: ...})` → `.ProcessHTML(ctx, html, oldHTML)` / `.Upload(ctx, file)` / `.Handler("image")` (`gin.WrapF(...)`) |
| `mrcatz:cleanup-editor-images` | `.cleanupTmp()` | `.CleanupTmp(ctx, time.Time{})` (also run by `ProcessHTML`) |
| `config('mrcatz.editor_image')` disk / path / size | `localDiskStorage({ root, publicUrl })`, `path`, `maxSize`, `tmpLifetimeHours`, `mimeTypes` | `editorimg.LocalDisk(root, publicURL)` (or your own `editorimg.Storage`), `Path`, `MaxSizeKB`, `TmpLifetime`, `MimeTypes` |
| `php artisan mrcatz:make` / `mrcatz:remove` | `npx mrcatz make` / `npx mrcatz remove` | `go run github.com/mrc4tz/mrcatz-go-datatables/cmd/mrcatz@latest make` / `remove` |
| `--path`, model, table | `--path`, `--model` (Prisma delegate), `--table`, `--api`, `--web` / `--no-web`, `--prisma`, `--api-base`, `--force` | `--path`, `--model` (GORM struct type), `--table`, `--api` (default `internal`), `--web` / `--no-web`, `--api-base`, `--force` |

## Frontend

Unchanged from the JS version — install `@mrcatz/datatable-vue` and point `httpSource({ url, schemaUrl, exportUrl })` at the Go
endpoints. The full Laravel → Vue mapping (`withColumn` → `col.text`, `setForm()` → `fields`, `MrCatzFormField::*` → `field.*`,
`enableBulk` → `selectable`, `enableExpand` → `expandable`, `$exportTitle` → `exportable`, `config('mrcatz.locale')` →
`createMrcatz({ locale })`, …) is in the [JS MIGRATING.md](https://github.com/mrc4tz/mrcatz-js-datatables/blob/main/MIGRATING.md#table--columns).

> A complete example app (Gin + GORM + Nuxt, the equivalent of `mrcatz-laravel-template`): **mrcatz-go-template**.
