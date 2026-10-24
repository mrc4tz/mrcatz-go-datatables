<p align="center">
  <a href="#status-beta"><img src="https://img.shields.io/badge/status-BETA-FF6B00?style=for-the-badge" alt="Status: BETA"></a>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/mrc4tz/mrcatz-go-datatables"><img src="https://pkg.go.dev/badge/github.com/mrc4tz/mrcatz-go-datatables.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="License"></a>
  <a href="https://datatable.catzoid.tech/?stack=go"><img src="https://img.shields.io/badge/docs-datatable.catzoid.tech-F57927" alt="Docs"></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go"></a>
  <a href="https://gin-gonic.com"><img src="https://img.shields.io/badge/Gin-1.11-008ECF?style=flat-square&logo=gin&logoColor=white" alt="Gin"></a>
  <a href="https://gorm.io"><img src="https://img.shields.io/badge/GORM-1.31-38B2AC?style=flat-square" alt="GORM"></a>
  <a href="https://www.postgresql.org"><img src="https://img.shields.io/badge/PostgreSQL-optional-4169E1?style=flat-square&logo=postgresql&logoColor=white" alt="PostgreSQL (optional)"></a>
</p>

# MrCatz DataTable for Go

**DataTable + Form Builder** for **Gin + GORM** and **Nuxt (Vue 3)** — build complete admin pages in minutes.

The Go version of [mrcatz/datatable](https://github.com/mrc4tz/mrcatz-datatables) (Laravel Livewire), ported from the
[JavaScript version](https://github.com/mrc4tz/mrcatz-js-datatables): same features, same look, same API shape — and the
**same Vue frontend**, unchanged.

📖 **[Full documentation →](https://datatable.catzoid.tech/?stack=go)**
🎮 **[Live demo →](https://datatable.catzoid.tech/demo)**

> <a id="status-beta"></a>**⚠️ BETA.** The Go port is new and has not yet been battle-tested in a real application.
> The test suite is green and the HTTP protocol is checked against the JS conformance cases, but **APIs may still
> change before v1** — pin a version and read the release notes when upgrading. Bug reports are very welcome.

---

## What is this?

A complete, opinionated DataTable + CRUD toolkit for Go back ends. Bundles everything you typically rebuild from scratch on every admin page — pagination, sorting, filtering, search, inline editing, bulk actions, expandable rows, exports, and a programmatic Form Builder — into a small set of composable APIs.

The Go API speaks **byte-for-byte the same HTTP protocol** as [`@mrcatz/datatable-server`](https://github.com/mrc4tz/mrcatz-js-datatables/tree/main/packages/server), so the existing Vue package works against it without changes:

| Package | Where | What it does |
|---|---|---|
| [`gormtable`](gormtable) | Go API | `gormtable.Define[T]()` on a GORM model: search, filters, sort whitelist, paging, export rows, PostgreSQL relevance, pluggable search driver |
| [`gindt`](gindt) | Go API (Gin) | `gindt.Register(group, table)` — list / schema / export endpoints, `ErrorMiddleware()` for Nest-shaped 400 errors |
| [`httpdt`](httpdt) | Go API (net/http) | The same handlers for plain `net/http` (`httpdt.Mount(mux, prefix, table)`) — no third-party dependencies |
| [`datatable`](datatable) | Go API | Framework-free core: query protocol, filter definitions (`Select`, `Multi`, `Date`, `DateRange`), search config, highlight, date helpers, CSV |
| [`memtable`](memtable) | Go API | In-memory table over a `[]T` with the same semantics — for tests, demos and small datasets |
| [`meili`](meili) | Go API | Meilisearch search driver (beta) with filter pushdown and `SyncSettings` — bring your own client |
| [`editorimg`](editorimg) | Go API | Rich text editor image pipeline: upload to tmp, `ProcessHTML`, `CleanupTmp`, pluggable `Storage` |
| [`cmd/mrcatz`](cmd/mrcatz) | CLI | `mrcatz make` / `mrcatz remove` — scaffolds the Go table, Gin handlers and the Nuxt page |
| [`@mrcatz/datatable-vue`](https://www.npmjs.com/package/@mrcatz/datatable-vue) | Nuxt / Vue 3 | `<MrcDataTable>`, `<MrcCrudTable>`, `<MrcForm>`, `col.*`, `field.*` — the frontend, shared with the JS version |

Think of it as **"Filament's CRUD productivity, but you keep full control over your stack."** You choose your DaisyUI theme, your auth, your router and your API shape — MrCatz slots into your existing Gin + Nuxt app instead of replacing it.

## Highlights

- ⚡ **CRUD in minutes** — `mrcatz make Product --path admin` scaffolds the GORM table, the Gin CRUD handlers and the Nuxt page
- 🔌 **Drop-in for the Vue frontend** — same query string, same JSON, same 400 error shape as the NestJS server
- 🔍 **Smart search** — multi-keyword, per-column weights, typo tolerance (trigram / `pg_trgm`), PostgreSQL relevance ranking (incl. joined columns), optional Meilisearch driver (beta)
- 🎯 **Powerful filters** — select, multi (include / exclude), dependent, and full date / date-range / time filters with operator support and dynamic bounds
- 🧬 **Typed with generics** — `gormtable.Table[Product]` returns `*datatable.Page[Product]`; keys are your model's `json` tags, mapped to DB columns through the GORM schema
- 🛡️ **Safe by default** — sort whitelist, validated identifiers, parameterised values, `LIKE` escaping, user-input errors as HTTP 400
- 🧰 **Gin or net/http** — `gindt` for Gin, `httpdt` for the standard library; `memtable` for tests
- ✏️ **Inline editing**, ☑️ **bulk actions** & **expandable rows** — from the Vue package; `Scope()` gives you the filtered query for bulk updates
- 📊 **PDF, CSV & Excel export** — export endpoint on the server, `.xlsx` / PDF / CSV generated in the browser
- 🧱 **Form Builder** — 30+ field types, sections, conditional fields, validation, rich text editors (with server-side image handling) and map picker
- 🔗 **Shareable state** — search, filters, sort, page and column layout live in the URL
- 📱 **Fully responsive** & 🎨 **themed via DaisyUI** — works with any DaisyUI theme

## Quick install

**API (Gin + GORM)**

```bash
go get github.com/mrc4tz/mrcatz-go-datatables
go get gorm.io/gorm gorm.io/driver/postgres github.com/gin-gonic/gin   # or any other GORM driver
```

Define a table on a GORM model and register it on a Gin group:

```go
package main

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"github.com/mrc4tz/mrcatz-go-datatables/gindt"
	"github.com/mrc4tz/mrcatz-go-datatables/gormtable"
)

// Product is a plain GORM model — the json tags are the keys the frontend
// sorts, filters and searches by.
type Product struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Price     float64   `json:"price"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func main() {
	db, err := gorm.Open(postgres.Open("host=localhost user=postgres password=secret dbname=shop sslmode=disable"), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	products, err := gormtable.Define(gormtable.Definition[Product]{
		DB: db,
		Search: &datatable.SearchConfig{
			Columns: []datatable.SearchColumn{datatable.Col("name").W(3)},
		},
		Filters: []datatable.Filter{
			datatable.Select("status", "Status", datatable.Field("status"),
				datatable.Options(datatable.Opt("active", "Active"), datatable.Opt("draft", "Draft"))),
			datatable.DateRange("created", "Created", datatable.Field("createdAt")),
		},
		Sortable:    []string{"id", "name", "price", "createdAt"},
		DefaultSort: []datatable.SortRule{{Key: "createdAt", Dir: datatable.Desc}},
	})
	if err != nil {
		log.Fatal(err) // *datatable.ConfigError: unknown column, bad filter, ...
	}

	r := gin.Default()
	api := r.Group("/api")

	// GET /api/products, /api/products/schema, /api/products/export
	gindt.Register(api.Group("/products", gindt.ErrorMiddleware()), products)

	log.Fatal(r.Run(":8080"))
}
```

`GET /api/products?search=kopi&f.status=active&sort=price:desc&page=2` now returns
`{"data": [...], "meta": {"total": 42, "page": 2, "limit": 10, "totalPages": 5, "hasMore": true}}`, and an unknown sort
key returns `400 {"message": "...", "error": "Bad Request", "statusCode": 400}` — exactly like the NestJS server.

Not using Gin? The same endpoints for the standard library:

```go
mux := http.NewServeMux()
httpdt.Mount(mux, "/api/products", products) // GET /api/products, /schema, /export
```

**Web (Nuxt 4)** — the frontend is the same `@mrcatz/datatable-vue` package as the JS version. Tailwind CSS v4 + DaisyUI 5 are required: the components ship class names, your build compiles them.

```bash
npm i @mrcatz/datatable-vue @tanstack/vue-query
npm i -D tailwindcss @tailwindcss/vite daisyui
```

```ts
// nuxt.config.ts
import tailwindcss from '@tailwindcss/vite';

export default defineNuxtConfig({
  css: ['~/assets/css/main.css'],
  vite: { plugins: [tailwindcss()] },
  // forward /api to the Go server (or serve both behind one reverse proxy)
  routeRules: { '/api/**': { proxy: 'http://localhost:8080/api/**' } },
});
```

```css
/* app/assets/css/main.css */
@import 'tailwindcss';
@plugin 'daisyui';
@source '../../../node_modules/@mrcatz/datatable-vue/dist';
```

Register the plugin (it replaces `config/mrcatz.php`) and mount `<MrcToaster />` once in your layout for toasts and the image lightbox:

```ts
// app/plugins/mrcatz.ts
import { VueQueryPlugin } from '@tanstack/vue-query';
import { createMrcatz } from '@mrcatz/datatable-vue';

export default defineNuxtPlugin((nuxt) => {
  nuxt.vueApp.use(VueQueryPlugin);
  nuxt.vueApp.use(createMrcatz({ locale: 'en' }));
});
```

```vue
<!-- app/layouts/default.vue -->
<template>
  <slot />
  <MrcToaster />
</template>
```

Point a page at the Go endpoints:

```vue
<!-- app/pages/products.vue -->
<script setup lang="ts">
import { col, httpSource } from '@mrcatz/datatable-vue';

const source = httpSource({
  url: '/api/products',
  schemaUrl: '/api/products/schema',
  exportUrl: '/api/products/export',
});

const columns = [
  col.rowNumber(),
  col.text('name', 'Name'),
  col.badge('status', 'Status', { active: 'success', draft: 'neutral' }),
  col.number('price', 'Price'),
  col.date('createdAt', 'Created'),
];
</script>

<template>
  <MrcCrudTable name="products" entity="Product" title="Products" :source="source" :columns="columns" />
</template>
```

Add `:fields`, `:create`, `:update` and `:remove` for the add / edit / delete forms — or let the CLI generate the whole thing:

```bash
go run github.com/mrc4tz/mrcatz-go-datatables/cmd/mrcatz@latest make Product --path admin
# or: go install github.com/mrc4tz/mrcatz-go-datatables/cmd/mrcatz@latest && mrcatz make Product --path admin
```

This writes `internal/admin/product/table.go` (placeholder GORM model + `gormtable.Define`), `internal/admin/product/handler.go`
(list / schema / export + create, update, delete and bulk delete) and `apps/web/app/pages/admin/products.vue`.
Flags: `--model`, `--table`, `--api <dir>`, `--web <dir>` / `--no-web`, `--api-base /api`, `--force`; undo with
`mrcatz remove Product --path admin`. Adjust the placeholder model, then register the generated package — Nuxt routes the page by its file path:

```go
import "example.com/shop/internal/admin/product"

api := r.Group("/api")
if err := product.Register(api, db); err != nil { // → /api/admin/products
	log.Fatal(err)
}
```

For the full setup walkthrough — Excel/PDF export, Meilisearch, editor images, Docker, and theming — see the **[Quick Start guide](https://datatable.catzoid.tech/docs/quick-start?stack=go)**.
A complete example app (Gin + GORM + Nuxt, the equivalent of `mrcatz-laravel-template`) is in the works: **mrcatz-go-template**.

## Documentation

The complete reference, with Laravel, JavaScript and Go code side by side for every feature, is hosted at **[datatable.catzoid.tech](https://datatable.catzoid.tech/?stack=go)**. The Go API reference lives on **[pkg.go.dev](https://pkg.go.dev/github.com/mrc4tz/mrcatz-go-datatables)**.

| Section | What's covered |
|---|---|
| [Getting Started](https://datatable.catzoid.tech/docs/introduction?stack=go) | Introduction, installation, quick-start |
| [Core Features](https://datatable.catzoid.tech/docs/columns?stack=go) | Columns, filters, search, sorting, pagination |
| [Editing & Actions](https://datatable.catzoid.tech/docs/inline-editing?stack=go) | Inline editing, bulk actions, expandable rows |
| [Form Builder](https://datatable.catzoid.tech/docs/form-builder?stack=go) | Programmatic forms, editors, map picker |
| [Export](https://datatable.catzoid.tech/docs/export?stack=go) | PDF, CSV & Excel with custom layouts |
| [Advanced Search](https://datatable.catzoid.tech/docs/advanced/scoring?stack=go) | Per-column scoring, typo tolerance, **Meilisearch (beta)** |
| [Customization](https://datatable.catzoid.tech/docs/customization/theming?stack=go) | Icons, localization, theming |
| [API Reference](https://datatable.catzoid.tech/docs/reference/api?stack=go) | Every prop, event, slot and option |

Coming from the Laravel package? **[MIGRATING.md](MIGRATING.md)** maps every Laravel method and property to its Go (and JS) name.
The design contract for contributors — wire protocol and package APIs — is in **[DESIGN.md](DESIGN.md)**.

## Requirements

- Go 1.23+
- GORM 1.31+ with any driver (`gormtable`)
- Gin 1.11+ (`gindt` only — `httpdt` works with plain `net/http`)
- Nuxt 4 / Vue 3.5+ with TanStack Query 5 (web)
- Tailwind CSS v4
- DaisyUI v5
- PostgreSQL — only for relevance ranking and `pg_trgm` typo tolerance (other GORM databases work for everything else)

## Optional dependencies

```bash
# API
go get github.com/meilisearch/meilisearch-go   # Meilisearch driver (beta) — wrap it in the small adapter shown in the meili package docs
# Web
npm i quill         # Rich text editor field — import '@mrcatz/datatable-vue/editor'
npm i tinymce       # Advanced rich text editor — import '@mrcatz/datatable-vue/editor-advanced'
npm i leaflet       # Map picker field — import '@mrcatz/datatable-vue/map'
```

The `meili` package itself has no dependency on `meilisearch-go`: it talks to a small `meili.Client` interface, so any client (or a test fake) works.

## Built with Claude

A large portion of this package — and its documentation site — was built collaboratively with [Anthropic's Claude](https://claude.com). The maintainer focused on architecture decisions, requirements, and design trade-offs; Claude handled most of the implementation, tests, and docs writing. A reminder that AI-assisted development, when guided well, multiplies what one developer can ship.

## Issues & contributions

Bug reports and feature requests welcome at [github.com/mrc4tz/mrcatz-go-datatables/issues](https://github.com/mrc4tz/mrcatz-go-datatables/issues) — especially while the Go version is in beta. Working on the code? See **[CONTRIBUTING.md](CONTRIBUTING.md)**.

## License

[MIT](LICENSE)
