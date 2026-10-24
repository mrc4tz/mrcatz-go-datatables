package gindt_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"github.com/mrc4tz/mrcatz-go-datatables/gindt"
	"github.com/mrc4tz/mrcatz-go-datatables/httpdt"
	"github.com/mrc4tz/mrcatz-go-datatables/memtable"
)

func init() { gin.SetMode(gin.TestMode) }

type orang struct {
	ID     int    `json:"id"`
	Nama   string `json:"nama"`
	Status string `json:"status"`
	At     string `json:"at"`
}

func table() *memtable.Table[orang] {
	return memtable.New([]orang{
		{1, "Budi & <Santoso>", "aktif", "2026-01-05T02:00:00Z"},
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

func do(t *testing.T, h http.Handler, target string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	b, _ := io.ReadAll(rec.Body)
	if rec.Code != 404 && rec.Code != 204 {
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s content-type = %q", target, ct)
		}
	}
	return rec.Code, string(b)
}

func TestRegister(t *testing.T) {
	for _, withMW := range []bool{true, false} {
		r := gin.New()
		var g *gin.RouterGroup
		if withMW {
			g = r.Group("/api/orang", gindt.ErrorMiddleware())
		} else {
			g = r.Group("/api/orang")
		}
		gindt.Register(g, table())

		cases := []struct {
			target string
			status int
			body   string
		}{
			{"/api/orang?f.status=aktif&limit=1", 200,
				`{"data":[{"id":1,"nama":"Budi & <Santoso>","status":"aktif","at":"2026-01-05T02:00:00Z"}],"meta":{"total":2,"page":1,"limit":1,"totalPages":2,"hasMore":true}}`},
			{"/api/orang?search=zzz", 200,
				`{"data":[],"meta":{"total":0,"page":1,"limit":10,"totalPages":1,"hasMore":false}}`},
			{"/api/orang/schema", 200,
				`{"filters":[{"id":"status","label":"Status","type":"select","visible":true,"options":[{"value":"aktif","label":"Aktif"}]},{"id":"at","label":"Tanggal","type":"dateRange","visible":true,"format":"date"}]}`},
			{"/api/orang/export?search=zzz", 200, `[]`},
			{"/api/orang/export?f.status=nonaktif", 200, `[{"id":2,"nama":"Siti Aminah","status":"nonaktif","at":"2026-02-10T02:00:00Z"}]`},
			{"/api/orang?f.at.from=bukan", 400, ""},
			{"/api/orang/export?f.at.from=bukan", 400, ""},
		}
		for _, c := range cases {
			status, body := do(t, r, c.target)
			if status != c.status {
				t.Errorf("mw=%v %s: status %d, mau %d (%s)", withMW, c.target, status, c.status, body)
				continue
			}
			if c.status == 400 {
				var qe *dt.QueryError
				_, err := table().Query(context.Background(), url.Values{"f.at.from": {"bukan"}})
				if !errors.As(err, &qe) {
					t.Fatalf("memtable harus QueryError: %v", err)
				}
				c.body = `{"message":` + quote(qe.Msg) + `,"error":"Bad Request","statusCode":400}`
			}
			if body != c.body {
				t.Errorf("mw=%v %s\n got %s\nwant %s", withMW, c.target, body, c.body)
			}
		}
	}
}

func quote(s string) string {
	b, _ := httpdt.Marshal(s)
	return string(b)
}

// gagal adalah tabel palsu yang selalu error.
type gagal struct{ err error }

func (g gagal) Query(context.Context, url.Values) (*dt.Page[orang], error) { return nil, g.err }
func (g gagal) Describe(context.Context, url.Values) (dt.Schema, error)    { return dt.Schema{}, g.err }
func (g gagal) ExportRows(context.Context, url.Values) ([]orang, error)    { return nil, g.err }

func TestErrorPaths(t *testing.T) {
	boom := errors.New("db putus")

	// Tanpa middleware: handler menulis 500 ala Nest sendiri.
	r := gin.New()
	gindt.Register(r.Group("/x"), gagal{boom})
	if status, body := do(t, r, "/x"); status != 500 || body != `{"statusCode":500,"message":"Internal server error"}` {
		t.Errorf("tanpa mw: %d %s", status, body)
	}

	// Dengan middleware: error lain diteruskan ke middleware aplikasi (outer).
	var seen []error
	r = gin.New()
	r.Use(func(c *gin.Context) {
		c.Next()
		for _, e := range c.Errors {
			seen = append(seen, e.Err)
		}
		if !c.Writer.Written() {
			gindt.Abort(c, http.StatusServiceUnavailable, "coba lagi")
		}
	})
	g := r.Group("/x", gindt.ErrorMiddleware())
	gindt.Register(g, gagal{boom})
	status, body := do(t, r, "/x/schema")
	if status != 503 || body != `{"message":"coba lagi","error":"Service Unavailable","statusCode":503}` {
		t.Errorf("mw app: %d %s", status, body)
	}
	if len(seen) != 1 || seen[0] != boom {
		t.Errorf("c.Errors = %v", seen)
	}

	// QueryError terbungkus di handler lain (bukan bawaan gindt) tetap 400.
	r = gin.New()
	r.GET("/y", gindt.ErrorMiddleware(), func(c *gin.Context) {
		_ = c.Error(errors.Join(errors.New("ctx"), dt.QueryErrorf("Kolom sort tidak diizinkan: %q", "x")))
	})
	if status, body := do(t, r, "/y"); status != 400 || body != `{"message":"Kolom sort tidak diizinkan: \"x\"","error":"Bad Request","statusCode":400}` {
		t.Errorf("handler kustom: %d %s", status, body)
	}
}

func TestValues(t *testing.T) {
	r := gin.New()
	var got url.Values
	r.GET("/v", func(c *gin.Context) { got = gindt.Values(c); c.Status(204) })
	do(t, r, "/v?f.kat=1&f.kat=3&page=2")
	if len(got["f.kat"]) != 2 || got.Get("page") != "2" {
		t.Errorf("Values = %v", got)
	}
}
