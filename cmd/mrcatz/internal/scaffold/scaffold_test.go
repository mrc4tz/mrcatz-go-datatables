package scaffold

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func paths(files []File) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func mustContain(t *testing.T, name, content string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(content, s) {
			t.Errorf("%s tidak memuat %q\n---\n%s", name, s, content)
		}
	}
}

func TestPlanDefault(t *testing.T) {
	files, err := Plan(Options{Name: "OrderItem", Path: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"internal/admin/order-item/table.go",
		"internal/admin/order-item/handler.go",
		"apps/web/app/pages/admin/order-items.vue",
	}
	if got := paths(files); !slices.Equal(got, want) {
		t.Fatalf("paths = %v", got)
	}
	table, handler, page := files[0].Content, files[1].Content, files[2].Content

	mustContain(t, "table.go", table,
		"package orderitem\n",
		"type OrderItem struct {",
		"func (OrderItem) TableName() string { return \"order_items\" }",
		"func NewTable(db *gorm.DB) (*gormtable.Table[OrderItem], error) {",
		"return gormtable.Define(gormtable.Definition[OrderItem]{",
		"Columns: []datatable.SearchColumn{datatable.Col(\"name\").W(3)},",
		"// Relevance: &gormtable.Relevance{Table: \"order_items\"},",
		"Sortable:    []string{\"id\", \"name\", \"createdAt\"},",
		"DefaultSort: []datatable.SortRule{{Key: \"createdAt\", Dir: datatable.Desc}},",
		`"github.com/mrc4tz/mrcatz-go-datatables/gormtable"`,
	)
	mustContain(t, "handler.go", handler,
		"package orderitem\n",
		`const Route = "/admin/order-items"`,
		"//	orderitem.Register(api, db) // → /api/admin/order-items",
		"func Register(r gin.IRouter, db *gorm.DB) error {",
		"g := r.Group(Route, gindt.ErrorMiddleware())",
		"gindt.Register(g, table)",
		`g.PATCH("/:id", h.update)`,
		`g.POST("/bulk-delete", h.removeMany)`,
		"Delete(&OrderItem{}, body.IDs)",
		`"Order item tidak ditemukan"`,
	)
	mustContain(t, "page", page,
		"url: '/api/admin/order-items',",
		"schemaUrl: '/api/admin/order-items/schema',",
		"exportUrl: '/api/admin/order-items/export',",
		`name="order-item"`,
		`entity="Order item"`,
		`title="Order items"`,
		"$fetch(`/api/admin/order-items/${row.id}`, { method: 'PATCH', body: v })",
		"$fetch('/api/admin/order-items/bulk-delete', { method: 'POST', body: { ids: rows.map((r) => r.id) } })",
	)

	// File Go valid & sudah gofmt (Plan menjalankan go/format).
	for _, f := range files[:2] {
		if _, err := parser.ParseFile(token.NewFileSet(), f.Path, f.Content, parser.ParseComments); err != nil {
			t.Errorf("%s tidak bisa di-parse: %v", f.Path, err)
		}
	}
}

func TestPlanOptions(t *testing.T) {
	files, err := Plan(Options{Name: "category", Model: "kategori", Table: "kategori", NoWeb: true, APIDir: "./pkg/"})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(files); !slices.Equal(got, []string{"pkg/category/table.go", "pkg/category/handler.go"}) {
		t.Fatalf("paths = %v", got)
	}
	mustContain(t, "table.go", files[0].Content,
		"package category\n", "type Kategori struct {", `return "kategori"`, `Relevance{Table: "kategori"}`)
	mustContain(t, "handler.go", files[1].Content, `const Route = "/categories"`, "var m Kategori")

	files, err = Plan(Options{Name: "box", Path: "/Back_Office/stok/", WebDir: "web/pages/", APIBase: "/v1/"})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(files); !slices.Equal(got, []string{
		"internal/back-office/stok/box/table.go", "internal/back-office/stok/box/handler.go", "web/pages/back-office/stok/boxes.vue",
	}) {
		t.Fatalf("paths = %v", got)
	}
	mustContain(t, "page", files[2].Content, "url: '/v1/back-office/stok/boxes',")

	// Nama paket yang bentrok dengan keyword Go.
	n, err := Resolve(Options{Name: "Type"})
	if err != nil || n.Package != "typepkg" || n.Model != "Type" || n.Route != "types" {
		t.Errorf("keyword = %+v %v", n, err)
	}
}

func TestPlanInvalid(t *testing.T) {
	for _, o := range []Options{
		{Name: "../evil"},
		{Name: ""},
		{Name: "1abc"},
		{Name: "ok", Path: "../x y"},
		{Name: "ok", Table: "drop table;"},
		{Name: "ok", Model: "-"},
	} {
		if _, err := Plan(o); err == nil {
			t.Errorf("Plan(%+v) harus error", o)
		}
	}
}

func TestNameHelpers(t *testing.T) {
	cases := []struct{ in, pascal, camel, kebab, snake, plural string }{
		{"OrderItem", "OrderItem", "orderItem", "order-item", "order_item", "OrderItems"},
		{"order-item", "OrderItem", "orderItem", "order-item", "order_item", "order-items"},
		{"category", "Category", "category", "category", "category", "categories"},
		{"box", "Box", "box", "box", "box", "boxes"},
		{"day", "Day", "day", "day", "day", "days"},
		{"HTTPLog", "Httplog", "httplog", "httplog", "httplog", "HTTPLogs"},
	}
	for _, c := range cases {
		if Pascal(c.in) != c.pascal || Camel(c.in) != c.camel || Kebab(c.in) != c.kebab || Snake(c.in) != c.snake || Plural(c.in) != c.plural {
			t.Errorf("%s → %s %s %s %s %s", c.in, Pascal(c.in), Camel(c.in), Kebab(c.in), Snake(c.in), Plural(c.in))
		}
	}
}

// TestGeneratedCodeBuilds mengompilasi kode hasil generator terhadap paket
// di repo ini (butuh gormtable + gorm). Aktifkan dengan MRCATZ_SCAFFOLD_BUILD=1.
func TestGeneratedCodeBuilds(t *testing.T) {
	if os.Getenv("MRCATZ_SCAFFOLD_BUILD") == "" {
		t.Skip("set MRCATZ_SCAFFOLD_BUILD=1 untuk mengompilasi kode hasil generator")
	}
	_, here, _, _ := runtime.Caller(0)
	repo := filepath.Join(filepath.Dir(here), "..", "..", "..", "..")
	// Kompilasi di dalam modul repo ini agar go.mod/go.sum dipakai apa adanya.
	dir, err := os.MkdirTemp(repo, ".scaffold-build-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	files, err := Plan(Options{Name: "OrderItem", Path: "admin", NoWeb: true, APIDir: "."})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Path eksplisit: pola "..." melewati folder berawalan titik.
	cmd := exec.Command("go", "vet", "./"+filepath.Base(dir)+"/admin/order-item")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kode hasil generator gagal dikompilasi:\n%s", out)
	}
}
