// Package datatable adalah inti protokol MrCatz DataTable untuk Go — padanan
// paket JS `@mrcatz/datatable-core`.
//
// Alur: client menyimpan TableQuery (state tabel) → diserialisasi ke query
// string (FormatQuery) → server mem-parse (ParseQuery) → engine (memtable,
// gormtable, …) mengembalikan Page[T].
//
// Paket ini hanya memakai pustaka standar dan tidak tahu apa-apa soal
// database: definisi filter, normalisasi nilai, rentang tanggal setengah-
// terbuka, konfigurasi pencarian, highlight, dan CSV dipakai bersama oleh
// semua engine.
package datatable
