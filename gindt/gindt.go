// Package gindt adalah adapter Gin untuk MrCatz DataTable — padanan helper
// `/nest` versi JS (`DataTableParams`, `DataTableExceptionFilter`).
//
//	r := gin.Default()
//	api := r.Group("/api")
//	users := api.Group("/users", gindt.ErrorMiddleware())
//	gindt.Register(users, usersTable)
//	// GET /api/users          → {"data": [...], "meta": {...}}
//	// GET /api/users/schema   → {"filters": [...]}
//	// GET /api/users/export   → [...]
//
// Body JSON ditulis byte-per-byte sama dengan `@mrcatz/datatable-server`
// (lihat httpdt.Marshal), termasuk error 400 berbentuk BadRequestException
// Nest: {"message": "...", "error": "Bad Request", "statusCode": 400}.
package gindt

import (
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"github.com/mrc4tz/mrcatz-go-datatables/httpdt"
)

// Querier dan kawan-kawan sama dengan milik httpdt (dipenuhi oleh
// memtable.Table[T] dan gormtable.Table[T]).
type (
	Querier[T any]  = httpdt.Querier[T]
	Lister[T any]   = httpdt.Lister[T]
	Describer       = httpdt.Describer
	Exporter[T any] = httpdt.Exporter[T]
)

// middlewareKey menandai bahwa ErrorMiddleware aktif di rantai handler.
const middlewareKey = "mrcatz.gindt.errorMiddleware"

// Values mengambil query string mentah untuk engine DataTable (padanan
// `@DataTableParams()`).
func Values(c *gin.Context) url.Values {
	return httpdt.Values(c.Request)
}

// JSON menulis v sebagai JSON persis seperti `res.json()` Express
// (tanpa escape HTML, berbeda dengan c.JSON milik Gin).
func JSON(c *gin.Context, status int, v any) {
	body, err := httpdt.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		body, _ = httpdt.Marshal(httpdt.InternalErrorBody)
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

// Abort menghentikan request dengan error berbentuk HttpException Nest,
// mis. Abort(c, 404, "Data tidak ditemukan") →
// {"message":"Data tidak ditemukan","error":"Not Found","statusCode":404}.
func Abort(c *gin.Context, status int, message string) {
	c.Abort()
	JSON(c, status, httpdt.NewErrorBody(status, message))
}

// InternalError mencatat err di c.Errors (untuk logger) lalu menghentikan
// request dengan 500 ala Nest: {"statusCode":500,"message":"Internal server error"}.
// Pesan asli tidak dikirim ke klien.
func InternalError(c *gin.Context, err error) {
	if err != nil {
		_ = c.Error(err)
	}
	c.Abort()
	JSON(c, http.StatusInternalServerError, httpdt.InternalErrorBody)
}

// writeQueryError menulis 400 bila err adalah *datatable.QueryError.
func writeQueryError(c *gin.Context, err error) bool {
	qe, ok := httpdt.AsQueryError(err)
	if !ok {
		return false
	}
	Abort(c, qe.StatusCode(), qe.Error())
	return true
}

// ErrorMiddleware memeriksa c.Errors setelah handler selesai dan memetakan
// *datatable.QueryError ke HTTP 400 ala Nest (padanan
// DataTableExceptionFilter). Error lain dibiarkan untuk middleware error
// milik aplikasi (pasang middleware itu SEBELUM ErrorMiddleware agar
// berjalan sesudahnya).
//
// Tanpa middleware ini handler List/Schema/Export tetap aman: mereka
// menulis sendiri 400 (QueryError) atau 500 ala Nest (error lain).
func ErrorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middlewareKey, true)
		c.Next()
		if c.Writer.Written() {
			return
		}
		for _, e := range c.Errors {
			if writeQueryError(c, e.Err) {
				return
			}
		}
	}
}

// fail mencatat err di c.Errors; bila ErrorMiddleware tidak terpasang,
// respons ditulis langsung.
func fail(c *gin.Context, err error) {
	if _, ok := c.Get(middlewareKey); ok {
		_ = c.Error(err)
		c.Abort()
		return
	}
	if writeQueryError(c, err) {
		_ = c.Error(err)
		return
	}
	InternalError(c, err)
}

// List membuat handler endpoint list: `{"data": [...], "meta": {...}}`.
func List[T any](t Lister[T]) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, err := t.Query(c.Request.Context(), Values(c))
		if err != nil {
			fail(c, err)
			return
		}
		if page == nil {
			page = &dt.Page[T]{}
		}
		if page.Data == nil {
			page.Data = []T{}
		}
		JSON(c, http.StatusOK, page)
	}
}

// Schema membuat handler endpoint schema: `{"filters": [...]}`.
func Schema(t Describer) gin.HandlerFunc {
	return func(c *gin.Context) {
		s, err := t.Describe(c.Request.Context(), Values(c))
		if err != nil {
			fail(c, err)
			return
		}
		if s.Filters == nil {
			s.Filters = []dt.FilterSchema{}
		}
		JSON(c, http.StatusOK, s)
	}
}

// Export membuat handler endpoint export: array JSON semua baris yang cocok.
func Export[T any](t Exporter[T]) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := t.ExportRows(c.Request.Context(), Values(c))
		if err != nil {
			fail(c, err)
			return
		}
		if rows == nil {
			rows = []T{}
		}
		JSON(c, http.StatusOK, rows)
	}
}

// Register mendaftarkan GET "", "/schema", dan "/export" pada grup/route g
// (biasanya hasil r.Group("/api/users", gindt.ErrorMiddleware())).
func Register[T any](g gin.IRoutes, t Querier[T]) {
	g.GET("", List[T](t))
	g.GET("/schema", Schema(t))
	g.GET("/export", Export[T](t))
}
