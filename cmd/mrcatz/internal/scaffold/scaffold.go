// Package scaffold adalah generator CRUD untuk `mrcatz make` / `mrcatz remove`
// (pengganti `php artisan mrcatz:make`, port dari scaffold.ts versi JS).
//
// Fungsi di sini murni (tanpa I/O) agar mudah diuji; main.go yang menulis file.
//
//	mrcatz make OrderItem --path admin
//	→ internal/admin/order-item/table.go     (model GORM + gormtable.Define)
//	  internal/admin/order-item/handler.go   (route Gin: DataTable + CRUD + bulk-delete)
//	  apps/web/app/pages/admin/order-items.vue
package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"go/token"
	"path"
	"regexp"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.tmpl"))

// Options adalah opsi `mrcatz make`.
type Options struct {
	// Name: nama resource, mis. "Product" atau "order-item".
	Name string
	// Path: sub-folder, mis. "admin" (pengganti `--path`).
	Path string
	// Model: nama tipe struct model GORM. Default PascalCase nama.
	Model string
	// Table: nama tabel database (TableName + relevansi). Default snake_case jamak.
	Table string
	// APIDir: folder paket Go. Default "internal".
	APIDir string
	// WebDir: folder halaman Nuxt. Default "apps/web/app/pages".
	WebDir string
	// NoWeb: tanpa halaman Nuxt.
	NoWeb bool
	// APIBase: prefix URL API yang dipakai halaman. Default "/api".
	APIBase string
}

// File adalah satu file hasil generator (path memakai "/").
type File struct {
	Path    string
	Content string
}

// Names adalah semua nama turunan yang dipakai template.
type Names struct {
	Name        string // input asli
	Pascal      string // OrderItem
	Kebab       string // order-item
	Package     string // orderitem
	Model       string // tipe struct model
	Table       string // order_items
	Label       string // "Order item"
	LabelPlural string // "Order items"
	Route       string // admin/order-items (tanpa slash depan)
	APIBase     string // /api
	URL         string // /api/admin/order-items
	PackageDir  string // internal/admin/order-item
}

var (
	nameRe  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)
	pathRe  = regexp.MustCompile(`^[a-zA-Z0-9_/-]+$`)
	camelRe = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	sepRe   = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	yRe     = regexp.MustCompile(`(?i)[^aeiou]y$`)
	esRe    = regexp.MustCompile(`(?i)(s|x|z|ch|sh)$`)
	identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func words(s string) []string {
	var out []string
	for _, w := range sepRe.Split(camelRe.ReplaceAllString(s, "$1 $2"), -1) {
		if w != "" {
			out = append(out, strings.ToLower(w))
		}
	}
	return out
}

// Pascal: "order-item" → "OrderItem".
func Pascal(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

// Camel: "order-item" → "orderItem".
func Camel(s string) string {
	p := Pascal(s)
	if p == "" {
		return p
	}
	return strings.ToLower(p[:1]) + p[1:]
}

// Kebab: "OrderItem" → "order-item".
func Kebab(s string) string { return strings.Join(words(s), "-") }

// Snake: "OrderItem" → "order_item".
func Snake(s string) string { return strings.Join(words(s), "_") }

// Plural adalah bentuk jamak sederhana bahasa Inggris (cukup untuk nama tabel/URL).
func Plural(s string) string {
	switch {
	case yRe.MatchString(s):
		return s[:len(s)-1] + "ies"
	case esRe.MatchString(s):
		return s + "es"
	}
	return s + "s"
}

func human(s string) string {
	w := strings.Join(words(s), " ")
	if w == "" {
		return w
	}
	return strings.ToUpper(w[:1]) + w[1:]
}

// cleanDir menormalkan folder ("./internal/" → "internal").
func cleanDir(dir, def string) string {
	if dir == "" {
		dir = def
	}
	return strings.TrimSuffix(path.Clean(strings.ReplaceAll(dir, `\`, "/")), "/")
}

// Resolve menghitung semua nama turunan dan memvalidasi input.
func Resolve(o Options) (Names, error) {
	if !nameRe.MatchString(o.Name) {
		return Names{}, fmt.Errorf("Nama resource tidak valid: %q", o.Name)
	}
	if o.Path != "" && !pathRe.MatchString(o.Path) {
		return Names{}, fmt.Errorf("Path tidak valid: %q", o.Path)
	}
	k := Kebab(o.Name)
	pkg := strings.Join(words(o.Name), "")
	if token.IsKeyword(pkg) {
		pkg += "pkg" // mis. "type" → "typepkg"
	}
	model := Pascal(o.Name)
	if o.Model != "" {
		model = Pascal(o.Model)
	}
	if !identRe.MatchString(model) {
		return Names{}, fmt.Errorf("Nama model tidak valid: %q", o.Model)
	}
	table := Plural(Snake(o.Name))
	if o.Table != "" {
		if !identRe.MatchString(o.Table) {
			return Names{}, fmt.Errorf("Nama tabel tidak valid: %q", o.Table)
		}
		table = o.Table
	}
	var sub []string
	for _, s := range strings.Split(o.Path, "/") {
		if s != "" {
			sub = append(sub, Kebab(s))
		}
	}
	route := strings.Join(append(append([]string{}, sub...), Plural(k)), "/")
	apiBase := strings.TrimRight(o.APIBase, "/")
	if o.APIBase == "" {
		apiBase = "/api"
	}
	return Names{
		Name:        o.Name,
		Pascal:      Pascal(o.Name),
		Kebab:       k,
		Package:     pkg,
		Model:       model,
		Table:       table,
		Label:       human(o.Name),
		LabelPlural: human(Plural(o.Name)),
		Route:       route,
		APIBase:     apiBase,
		URL:         apiBase + "/" + route,
		PackageDir:  strings.Join(append(append([]string{cleanDir(o.APIDir, "internal")}, sub...), k), "/"),
	}, nil
}

func render(name string, n Names, gofmt bool) (string, error) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, n); err != nil {
		return "", err
	}
	if !gofmt {
		return buf.String(), nil
	}
	src, err := format.Source(buf.Bytes())
	if err != nil {
		return "", fmt.Errorf("scaffold: %s tidak valid: %w", name, err)
	}
	return string(src), nil
}

// Plan menghasilkan daftar file untuk `mrcatz make` (tanpa menulis apa pun).
// Urutan: table.go, handler.go, lalu halaman Nuxt (kecuali NoWeb).
func Plan(o Options) ([]File, error) {
	n, err := Resolve(o)
	if err != nil {
		return nil, err
	}
	var files []File
	for _, f := range []struct{ tmpl, name string }{
		{"table.go.tmpl", "table.go"},
		{"handler.go.tmpl", "handler.go"},
	} {
		content, err := render(f.tmpl, n, true)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: n.PackageDir + "/" + f.name, Content: content})
	}
	if !o.NoWeb {
		content, err := render("page.vue.tmpl", n, false)
		if err != nil {
			return nil, err
		}
		segs := []string{cleanDir(o.WebDir, "apps/web/app/pages")}
		segs = append(segs, strings.Split(n.Route, "/")...)
		files = append(files, File{Path: strings.Join(segs, "/") + ".vue", Content: content})
	}
	return files, nil
}
