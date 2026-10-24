package httpdt_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"github.com/mrc4tz/mrcatz-go-datatables/httpdt"
	"github.com/mrc4tz/mrcatz-go-datatables/memtable"
)

type orang struct {
	ID     int    `json:"id"`
	Nama   string `json:"nama"`
	Status string `json:"status"`
	At     string `json:"at"`
}

func table() *memtable.Table[orang] {
	return memtable.New([]orang{
		{1, "Budi <Santoso>", "aktif", "2026-01-05T02:00:00Z"},
		{2, "Siti Aminah", "nonaktif", "2026-02-10T02:00:00Z"},
		{3, "Budi Hartono", "aktif", "2026-03-15T02:00:00Z"},
	}, memtable.Options{
		Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}},
		Filters: []dt.Filter{
			dt.Select("status", "Status", dt.Field("status"), dt.Options(dt.Opt("aktif", "Aktif"))),
			dt.DateRange("at", "Tanggal", dt.Field("at")),
		},
		DefaultSort: []dt.SortRule{{Key: "id", Dir: dt.Asc}},
		UTCOffset:   "Z",
	})
}

func get(t *testing.T, h http.Handler, target string) (int, string, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body), rec.Header()
}

func TestMountEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	httpdt.Mount(mux, "/api/orang/", table())

	cases := []struct {
		name, target string
		status       int
		body         string
	}{
		{"list", "/api/orang?f.status=aktif&limit=1",
			200, `{"data":[{"id":1,"nama":"Budi <Santoso>","status":"aktif","at":"2026-01-05T02:00:00Z"}],"meta":{"total":2,"page":1,"limit":1,"totalPages":2,"hasMore":true}}`},
		{"list kosong → data []", "/api/orang?search=zzz",
			200, `{"data":[],"meta":{"total":0,"page":1,"limit":10,"totalPages":1,"hasMore":false}}`},
		{"schema", "/api/orang/schema",
			200, `{"filters":[{"id":"status","label":"Status","type":"select","visible":true,"options":[{"value":"aktif","label":"Aktif"}]},{"id":"at","label":"Tanggal","type":"dateRange","visible":true,"format":"date"}]}`},
		{"export", "/api/orang/export?search=budi&sort=id:desc",
			200, `[{"id":3,"nama":"Budi Hartono","status":"aktif","at":"2026-03-15T02:00:00Z"},{"id":1,"nama":"Budi <Santoso>","status":"aktif","at":"2026-01-05T02:00:00Z"}]`},
		{"export kosong → []", "/api/orang/export?search=zzz", 200, `[]`},
		{"tanggal tidak valid → 400 ala Nest", "/api/orang?f.at.from=bukan-tanggal", 400, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, body, hdr := get(t, mux, c.target)
			if status != c.status {
				t.Fatalf("status = %d, mau %d (%s)", status, c.status, body)
			}
			if ct := hdr.Get("Content-Type"); ct != "application/json; charset=utf-8" {
				t.Errorf("content-type = %q", ct)
			}
			if c.status == 400 {
				if !strings.HasPrefix(body, `{"message":"`) || !strings.HasSuffix(body, `","error":"Bad Request","statusCode":400}`) {
					t.Errorf("body 400 = %s", body)
				}
				return
			}
			if body != c.body {
				t.Errorf("body\n got %s\nwant %s", body, c.body)
			}
		})
	}

	if status, _, _ := get(t, mux, "/api/orang/lain"); status != 404 {
		t.Errorf("path lain = %d, mau 404", status)
	}
}

func TestMountRoot(t *testing.T) {
	mux := http.NewServeMux()
	httpdt.Mount(mux, "", table())
	for _, p := range []string{"/", "/schema", "/export"} {
		if status, body, _ := get(t, mux, p); status != 200 {
			t.Errorf("%s = %d %s", p, status, body)
		}
	}
	if status, _, _ := get(t, mux, "/x"); status != 404 {
		t.Errorf("/x = %d, mau 404", status)
	}
}

// gagal adalah tabel palsu yang selalu error.
type gagal struct{ err error }

func (g gagal) Query(context.Context, url.Values) (*dt.Page[orang], error) { return nil, g.err }
func (g gagal) Describe(context.Context, url.Values) (dt.Schema, error)    { return dt.Schema{}, g.err }
func (g gagal) ExportRows(context.Context, url.Values) ([]orang, error)    { return nil, g.err }

func TestErrors(t *testing.T) {
	// Error lain → 500 ala Nest, pesan asli tidak bocor.
	h := httpdt.List[orang](gagal{errors.New("koneksi db putus")})
	status, body, _ := get(t, h, "/")
	if status != 500 || body != `{"statusCode":500,"message":"Internal server error"}` {
		t.Errorf("500: %d %s", status, body)
	}

	// QueryError yang dibungkus tetap 400.
	wrapped := errors.Join(errors.New("ctx"), dt.QueryErrorf("Kolom sort tidak diizinkan: %q", "x"))
	status, body, _ = get(t, httpdt.Schema(gagal{wrapped}), "/")
	if status != 400 || body != `{"message":"Kolom sort tidak diizinkan: \"x\"","error":"Bad Request","statusCode":400}` {
		t.Errorf("400: %d %s", status, body)
	}

	// Error handler kustom.
	var seen error
	h = httpdt.Export[orang](gagal{io.ErrUnexpectedEOF}, httpdt.WithErrorHandler(func(w http.ResponseWriter, _ *http.Request, err error) {
		seen = err
		httpdt.WriteJSON(w, http.StatusServiceUnavailable, httpdt.NewErrorBody(503, "coba lagi"))
	}))
	status, body, _ = get(t, h, "/")
	if status != 503 || seen != io.ErrUnexpectedEOF || body != `{"message":"coba lagi","error":"Service Unavailable","statusCode":503}` {
		t.Errorf("kustom: %d %s %v", status, body, seen)
	}

	// WriteError tidak menyentuh error lain.
	rec := httptest.NewRecorder()
	if httpdt.WriteError(rec, errors.New("x")) || rec.Body.Len() != 0 {
		t.Error("WriteError harus false untuk error biasa")
	}
}

func TestMarshalLikeJSONStringify(t *testing.T) {
	got, err := httpdt.Marshal(map[string]any{"a": "<b>&\u2028", "c": `\u003c`, "p": dt.Page[orang]{Data: []orang{{Nama: "<x>"}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":"<b>&` + "\u2028" + `","c":"\\u003c","p":{"data":[{"id":0,"nama":"<x>","status":"","at":""}],"meta":{"total":0,"page":0,"limit":0,"totalPages":0,"hasMore":false}}}`
	if string(got) != want {
		t.Errorf("Marshal\n got %s\nwant %s", got, want)
	}
}

func TestValuesAndInference(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?f.kat=1&f.kat=3&f.kat.not=1", nil)
	v := httpdt.Values(r)
	if got := v["f.kat"]; len(got) != 2 || got[1] != "3" || v.Get("f.kat.not") != "1" {
		t.Errorf("Values = %v", v)
	}
	if len(httpdt.Values(nil)) != 0 {
		t.Error("Values(nil) harus kosong")
	}
	// Argumen tipe T diinferensi dari method set (Go ≥ 1.21).
	var _ http.HandlerFunc = httpdt.List(table())
	var _ http.HandlerFunc = httpdt.Export(table())
	var _ httpdt.Querier[orang] = table()
}
