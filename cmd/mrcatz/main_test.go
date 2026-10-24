package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cli(t *testing.T, cwd, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, cwd, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestMakeAndRemove(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"internal/admin/order-item/table.go",
		"internal/admin/order-item/handler.go",
		"apps/web/app/pages/admin/order-items.vue",
	}

	// Flag boleh sebelum maupun sesudah nama.
	code, out, errs := cli(t, dir, "", "make", "--path", "admin", "OrderItem")
	if code != 0 {
		t.Fatalf("make = %d\n%s%s", code, out, errs)
	}
	for _, f := range files {
		if !exists(filepath.Join(dir, f)) || !strings.Contains(out, "✓ "+f) {
			t.Errorf("%s tidak dibuat\n%s", f, out)
		}
	}
	if !strings.Contains(out, "orderitem.Register(api, db)") || !strings.Contains(out, `router.Group("/api")`) {
		t.Errorf("pesan selesai:\n%s", out)
	}

	// Tanpa --force: menolak menimpa, file tidak berubah.
	p := filepath.Join(dir, files[0])
	_ = os.WriteFile(p, []byte("ubahan saya"), 0o644)
	code, _, errs = cli(t, dir, "", "make", "OrderItem", "--path=admin")
	if code != 1 || !strings.Contains(errs, "--force") || !strings.Contains(errs, files[0]) {
		t.Errorf("clash = %d %s", code, errs)
	}
	if b, _ := os.ReadFile(p); string(b) != "ubahan saya" {
		t.Error("file tertimpa tanpa --force")
	}
	if code, _, _ = cli(t, dir, "", "make", "OrderItem", "--path", "admin", "--force"); code != 0 {
		t.Error("make --force gagal")
	}
	if b, _ := os.ReadFile(p); !strings.HasPrefix(string(b), "// Kode ini dibuat oleh `mrcatz make OrderItem`") {
		t.Errorf("file tidak tertimpa: %s", b)
	}

	// remove: jawaban "n" membatalkan.
	code, out, _ = cli(t, dir, "n\n", "remove", "OrderItem", "--path", "admin")
	if code != 1 || !strings.Contains(out, "Hapus 3 file di atas? (y/N)") || !exists(p) {
		t.Errorf("remove batal = %d %s", code, out)
	}
	// remove: "yes" → dihapus, folder paket kosong ikut dihapus.
	code, out, _ = cli(t, dir, "Yes\n", "remove", "OrderItem", "--path", "admin")
	if code != 0 {
		t.Fatalf("remove = %d %s", code, out)
	}
	for _, f := range files {
		if exists(filepath.Join(dir, f)) {
			t.Errorf("%s masih ada", f)
		}
	}
	if exists(filepath.Join(dir, "internal/admin/order-item")) {
		t.Error("folder paket kosong tidak dihapus")
	}
	if !exists(filepath.Join(dir, "internal/admin")) {
		t.Error("folder induk tidak boleh dihapus")
	}
	code, out, _ = cli(t, dir, "", "remove", "OrderItem", "--path", "admin")
	if code != 0 || !strings.Contains(out, "Tidak ada file untuk dihapus.") {
		t.Errorf("remove kosong = %d %s", code, out)
	}
}

func TestMakeOptions(t *testing.T) {
	dir := t.TempDir()
	code, out, errs := cli(t, dir, "", "make", "category", "--model", "Kategori", "--table", "kategori",
		"--api", "./pkg", "--no-web", "--api-base", "/v1")
	if code != 0 {
		t.Fatalf("make = %d %s%s", code, out, errs)
	}
	b, err := os.ReadFile(filepath.Join(dir, "pkg/category/table.go"))
	if err != nil || !strings.Contains(string(b), "type Kategori struct") || !strings.Contains(string(b), `return "kategori"`) {
		t.Errorf("table.go = %s %v", b, err)
	}
	if exists(filepath.Join(dir, "apps")) {
		t.Error("--no-web tetap membuat halaman")
	}
	if code, _, _ := cli(t, dir, "", "remove", "category", "--api", "pkg", "--no-web", "--force"); code != 0 || exists(filepath.Join(dir, "pkg/category")) {
		t.Error("remove --force gagal")
	}

	code, out, _ = cli(t, dir, "", "make", "Box", "--web", "web/pages", "--path", "toko")
	if code != 0 || !exists(filepath.Join(dir, "web/pages/toko/boxes.vue")) {
		t.Errorf("--web = %d %s", code, out)
	}
}

func TestUsageAndErrors(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		args      []string
		code      int
		out, errs string
	}{
		{nil, 0, "Usage:", ""},
		{[]string{"--help"}, 0, "", "Usage:"},
		{[]string{"make"}, 1, "Usage:", ""},
		{[]string{"hapus", "X"}, 1, "Usage:", ""},
		{[]string{"make", "../evil"}, 1, "", "Nama resource tidak valid"},
		{[]string{"make", "X", "--path", "a b"}, 1, "", "Path tidak valid"},
		{[]string{"make", "X", "--bogus"}, 1, "", "flag provided but not defined"},
		{[]string{"make", "X", "Y"}, 1, "", "Argumen berlebih"},
	}
	for _, c := range cases {
		code, out, errs := cli(t, dir, "", c.args...)
		if code != c.code || !strings.Contains(out, c.out) || !strings.Contains(errs, c.errs) {
			t.Errorf("%v = %d\nout: %s\nerr: %s", c.args, code, out, errs)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("tidak boleh ada file dibuat: %v", entries)
	}
}
