// Package httpdt adalah adapter net/http untuk MrCatz DataTable — padanan
// helper `/nest` versi JS (`DataTableParams`, `DataTableExceptionFilter`).
//
// Protokol kabelnya identik dengan `@mrcatz/datatable-server`, sehingga
// paket Vue (`httpSource`) bekerja tanpa perubahan:
//
//	mux := http.NewServeMux()
//	httpdt.Mount(mux, "/api/users", usersTable)
//	// GET /api/users          → {"data": [...], "meta": {...}}
//	// GET /api/users/schema   → {"filters": [...]}
//	// GET /api/users/export   → [...]
//
// Tabel apa pun yang punya method Query/Describe/ExportRows bisa dipakai
// (memtable.Table, gormtable.Table, atau buatan sendiri).
package httpdt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
)

// Lister mengembalikan satu halaman data (endpoint list).
type Lister[T any] interface {
	Query(ctx context.Context, v url.Values) (*dt.Page[T], error)
}

// Describer mengembalikan skema filter (endpoint schema).
type Describer interface {
	Describe(ctx context.Context, v url.Values) (dt.Schema, error)
}

// Exporter mengembalikan semua baris yang cocok (endpoint export).
type Exporter[T any] interface {
	ExportRows(ctx context.Context, v url.Values) ([]T, error)
}

// Querier adalah gabungan ketiganya — dipenuhi oleh memtable.Table[T] dan
// gormtable.Table[T].
type Querier[T any] interface {
	Lister[T]
	Describer
	Exporter[T]
}

// ErrorBody adalah bentuk JSON error ala NestJS (`HttpException`), dengan
// urutan kunci yang sama: {"message": ..., "error": ..., "statusCode": ...}.
type ErrorBody struct {
	Message    string `json:"message"`
	Error      string `json:"error"`
	StatusCode int    `json:"statusCode"`
}

// NewErrorBody membuat ErrorBody dengan `error` = teks status HTTP
// (mis. 400 → "Bad Request"), sama seperti `new BadRequestException(msg)`.
func NewErrorBody(status int, message string) ErrorBody {
	return ErrorBody{Message: message, Error: http.StatusText(status), StatusCode: status}
}

// internalError adalah respons bawaan Nest untuk error yang tidak dikenal.
type internalError struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
}

// InternalErrorBody = `{"statusCode":500,"message":"Internal server error"}`
// (persis BaseExceptionFilter NestJS).
var InternalErrorBody any = internalError{StatusCode: http.StatusInternalServerError, Message: "Internal server error"}

// Values mengambil query string mentah untuk engine DataTable (padanan
// `@DataTableParams()`). Validasi dilakukan oleh engine sendiri.
func Values(r *http.Request) url.Values {
	if r == nil || r.URL == nil {
		return url.Values{}
	}
	return r.URL.Query()
}

// Marshal meng-encode v seperti `res.json()` Express (JSON.stringify):
// `<`, `>`, `&`, U+2028 dan U+2029 ditulis apa adanya (encoding/json
// meng-escape-nya, termasuk di dalam MarshalJSON kustom) dan tanpa newline.
func Marshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return unescapeHTML(b), nil
}

// unescapeHTML mengembalikan escape \u003c \u003e \u0026 \u2028 \u2029
// buatan encoding/json ke karakter aslinya. Escape diproses berpasangan
// sehingga `\\u003c` (backslash literal) tidak ikut terganti.
func unescapeHTML(b []byte) []byte {
	if !bytes.Contains(b, []byte(`\u`)) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c != '\\' || i+1 >= len(b) {
			out = append(out, c)
			continue
		}
		if b[i+1] == 'u' && i+6 <= len(b) {
			if r, ok := htmlEscapes[string(b[i+2:i+6])]; ok {
				out = append(out, r...)
				i += 5
				continue
			}
		}
		out = append(out, c, b[i+1])
		i++
	}
	return out
}

var htmlEscapes = map[string]string{"003c": "<", "003e": ">", "0026": "&", "2028": "\u2028", "2029": "\u2029"}

// WriteJSON menulis v sebagai JSON dengan status tertentu
// (`Content-Type: application/json; charset=utf-8`). Bila encode gagal,
// ditulis 500 ala Nest.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	body, err := Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		body, _ = Marshal(InternalErrorBody)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// AsQueryError mengembalikan *datatable.QueryError di dalam rantai err.
func AsQueryError(err error) (*dt.QueryError, bool) {
	var qe *dt.QueryError
	if errors.As(err, &qe) {
		return qe, true
	}
	return nil, false
}

// WriteError memetakan *datatable.QueryError (sort tak diizinkan, tanggal
// tidak valid, ...) ke HTTP 400 berbentuk BadRequestException Nest dan
// mengembalikan true. Error lain tidak disentuh (false) — tangani sendiri.
func WriteError(w http.ResponseWriter, err error) bool {
	qe, ok := AsQueryError(err)
	if !ok {
		return false
	}
	WriteJSON(w, qe.StatusCode(), NewErrorBody(qe.StatusCode(), qe.Error()))
	return true
}

// ErrorHandler dipanggil untuk error yang bukan *datatable.QueryError.
type ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// DefaultErrorHandler menulis 500 ala Nest (pesan asli tidak dibocorkan).
func DefaultErrorHandler(w http.ResponseWriter, _ *http.Request, _ error) {
	WriteJSON(w, http.StatusInternalServerError, InternalErrorBody)
}

// Option mengatur handler.
type Option func(*config)

type config struct{ onError ErrorHandler }

// WithErrorHandler mengganti penanganan error non-QueryError (bawaan:
// DefaultErrorHandler). QueryError selalu menjadi 400.
func WithErrorHandler(h ErrorHandler) Option {
	return func(c *config) {
		if h != nil {
			c.onError = h
		}
	}
}

func newConfig(opts []Option) config {
	c := config{onError: DefaultErrorHandler}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func (c config) fail(w http.ResponseWriter, r *http.Request, err error) {
	if !WriteError(w, err) {
		c.onError(w, r, err)
	}
}

// List membuat handler endpoint list: `{"data": [...], "meta": {...}}`.
func List[T any](t Lister[T], opts ...Option) http.HandlerFunc {
	c := newConfig(opts)
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := t.Query(r.Context(), Values(r))
		if err != nil {
			c.fail(w, r, err)
			return
		}
		if page == nil {
			page = &dt.Page[T]{}
		}
		if page.Data == nil {
			page.Data = []T{}
		}
		WriteJSON(w, http.StatusOK, page)
	}
}

// Schema membuat handler endpoint schema: `{"filters": [...]}`.
func Schema(t Describer, opts ...Option) http.HandlerFunc {
	c := newConfig(opts)
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := t.Describe(r.Context(), Values(r))
		if err != nil {
			c.fail(w, r, err)
			return
		}
		if s.Filters == nil {
			s.Filters = []dt.FilterSchema{}
		}
		WriteJSON(w, http.StatusOK, s)
	}
}

// Export membuat handler endpoint export: array JSON semua baris yang cocok
// (tanpa paginasi; batas diatur engine, mis. gormtable ExportLimit).
func Export[T any](t Exporter[T], opts ...Option) http.HandlerFunc {
	c := newConfig(opts)
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := t.ExportRows(r.Context(), Values(r))
		if err != nil {
			c.fail(w, r, err)
			return
		}
		if rows == nil {
			rows = []T{}
		}
		WriteJSON(w, http.StatusOK, rows)
	}
}

// Mount mendaftarkan tiga endpoint GET di mux memakai pola ServeMux Go 1.22:
//
//	GET {prefix}          → List
//	GET {prefix}/schema   → Schema
//	GET {prefix}/export   → Export
//
// prefix tanpa slash di akhir, mis. "/api/admin/users" ("" atau "/" = root).
func Mount[T any](mux *http.ServeMux, prefix string, t Querier[T], opts ...Option) {
	p := "/" + strings.Trim(prefix, "/")
	base := p
	if p == "/" {
		p = ""
		base = "/{$}"
	}
	mux.Handle("GET "+base, List[T](t, opts...))
	mux.Handle("GET "+p+"/schema", Schema(t, opts...))
	mux.Handle("GET "+p+"/export", Export[T](t, opts...))
}
