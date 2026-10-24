package datatable

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrInvalidDate dibungkus oleh *QueryError bila nilai tanggal/jam/offset
// tidak valid, sehingga bisa dicek dengan errors.Is.
var ErrInvalidDate = errors.New("datatable: nilai tanggal tidak valid")

// QueryError adalah kesalahan yang berasal dari input pengguna (query string)
// — mis. kolom sort yang tidak diizinkan atau tanggal tidak valid. Adapter
// HTTP memetakannya ke 400, bukan 500 (padanan DataTableQueryError).
type QueryError struct {
	Msg string
	// Err adalah penyebab opsional (mis. ErrInvalidDate) untuk errors.Is.
	Err error
}

func (e *QueryError) Error() string { return e.Msg }

// Unwrap mengembalikan penyebab kesalahan (boleh nil).
func (e *QueryError) Unwrap() error { return e.Err }

// StatusCode selalu 400 (Bad Request).
func (e *QueryError) StatusCode() int { return http.StatusBadRequest }

// QueryErrorf membuat *QueryError dengan pesan terformat.
func QueryErrorf(format string, args ...any) *QueryError {
	return &QueryError{Msg: fmt.Sprintf(format, args...)}
}

// ConfigError adalah kesalahan konfigurasi oleh developer (terjadi saat
// tabel didefinisikan), padanan DataTableConfigError.
type ConfigError struct{ Msg string }

func (e *ConfigError) Error() string { return e.Msg }

// ConfigErrorf membuat *ConfigError dengan pesan terformat.
func ConfigErrorf(format string, args ...any) *ConfigError {
	return &ConfigError{Msg: fmt.Sprintf(format, args...)}
}

// FilterDefinitionError menandakan definisi filter yang tidak valid
// (mis. tanpa field/where/match).
type FilterDefinitionError struct{ Msg string }

func (e *FilterDefinitionError) Error() string { return e.Msg }

func invalidDate(format string, args ...any) *QueryError {
	return &QueryError{Msg: fmt.Sprintf(format, args...), Err: ErrInvalidDate}
}
