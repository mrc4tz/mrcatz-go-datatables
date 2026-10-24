package datatable

// SearchMode adalah cara mencocokkan teks pada kolom pencarian.
type SearchMode string

const (
	ModeContains   SearchMode = "contains"
	ModeStartsWith SearchMode = "startsWith"
	ModeEquals     SearchMode = "equals"
)

// ColumnType adalah tipe kolom pencarian.
type ColumnType string

const (
	ColumnString ColumnType = "string"
	// ColumnNumber hanya dicocokkan bila kata kuncinya angka (pakai equals).
	ColumnNumber ColumnType = "number"
)

// SearchColumn adalah satu kolom yang dicari.
type SearchColumn struct {
	// Field: json key yang dicari; dot-path untuk relasi (`kategori.nama`).
	Field string
	// Column: nama kolom asli di database bila berbeda (khusus engine SQL
	// mode relevansi).
	Column string
	// Weight: bobot relevansi (default 1). Kolom berbobot tinggi naik ke atas.
	Weight int
	// Mode: default contains.
	Mode SearchMode
	// Type: default string.
	Type ColumnType
}

// Col membuat SearchColumn dengan nilai bawaan (bobot 1, contains, string).
func Col(field string) SearchColumn {
	return SearchColumn{Field: field, Weight: 1, Mode: ModeContains, Type: ColumnString}
}

// W mengatur bobot relevansi.
func (c SearchColumn) W(weight int) SearchColumn { c.Weight = weight; return c }

// StartsWith mengubah mode menjadi startsWith.
func (c SearchColumn) StartsWith() SearchColumn { c.Mode = ModeStartsWith; return c }

// Equals mengubah mode menjadi equals.
func (c SearchColumn) Equals() SearchColumn { c.Mode = ModeEquals; return c }

// Numeric menandai kolom angka.
func (c SearchColumn) Numeric() SearchColumn { c.Type = ColumnNumber; return c }

// As mengatur nama kolom database asli.
func (c SearchColumn) As(column string) SearchColumn { c.Column = column; return c }

// Driver toleransi salah ketik.
const (
	TypoNone    = "none"
	TypoTrigram = "trigram"
	// TypoPgTrgm memakai ekstensi PostgreSQL pg_trgm (operator %); hanya
	// engine SQL mode relevansi. Di memtable diperlakukan seperti trigram.
	TypoPgTrgm = "pg_trgm"
)

// TypoTolerance: pecah kata ≥ MinWordLength menjadi trigram lalu cocokkan
// sebagian.
type TypoTolerance struct {
	// Driver: "trigram" (default bila kosong) atau "pg_trgm".
	Driver string
	// MinWordLength: default 4; nilai negatif dianggap 1.
	MinWordLength int
}

// Pencocokan kata kunci.
const (
	// MatchAll: setiap kata kunci harus cocok di minimal satu kolom (default).
	MatchAll = "all"
	// MatchAny: cukup salah satu kata cocok (perilaku versi Laravel).
	MatchAny = "any"
)

// SearchConfig adalah konfigurasi pencarian tabel.
type SearchConfig struct {
	Columns []SearchColumn
	// Match: MatchAll (default) atau MatchAny.
	Match string
	// TypoTolerance: nil = mati.
	TypoTolerance *TypoTolerance
}

// NormalizedTypo adalah toleransi salah ketik setelah dinormalisasi.
type NormalizedTypo struct {
	Driver        string // none | trigram | pg_trgm
	MinWordLength int
}

// NormalizedSearch adalah SearchConfig dengan semua nilai bawaan terisi.
type NormalizedSearch struct {
	Columns []SearchColumn
	Match   string
	Typo    NormalizedTypo
}

// NormalizeSearch mengisi nilai bawaan SearchConfig (cfg boleh nil).
func NormalizeSearch(cfg *SearchConfig) NormalizedSearch {
	out := NormalizedSearch{Columns: []SearchColumn{}, Match: MatchAll, Typo: NormalizedTypo{Driver: TypoNone, MinWordLength: 4}}
	if cfg == nil {
		return out
	}
	if cfg.Match != "" {
		out.Match = cfg.Match
	}
	if t := cfg.TypoTolerance; t != nil {
		out.Typo.Driver = t.Driver
		if out.Typo.Driver == "" {
			out.Typo.Driver = TypoTrigram
		}
		switch {
		case t.MinWordLength == 0:
			out.Typo.MinWordLength = 4
		case t.MinWordLength < 1:
			out.Typo.MinWordLength = 1
		default:
			out.Typo.MinWordLength = t.MinWordLength
		}
	}
	for _, c := range cfg.Columns {
		if c.Weight == 0 {
			c.Weight = 1
		}
		if c.Mode == "" {
			c.Mode = ModeContains
		}
		if c.Type == "" {
			c.Type = ColumnString
		}
		out.Columns = append(out.Columns, c)
	}
	return out
}

// Validate memeriksa nilai Match, Mode, Type, dan Driver; kesalahan bertipe
// *ConfigError.
func (cfg *SearchConfig) Validate() error {
	if cfg == nil {
		return nil
	}
	switch cfg.Match {
	case "", MatchAll, MatchAny:
	default:
		return ConfigErrorf("search.match tidak valid: %q (pilih all atau any).", cfg.Match)
	}
	if t := cfg.TypoTolerance; t != nil {
		switch t.Driver {
		case "", TypoTrigram, TypoPgTrgm:
		default:
			return ConfigErrorf("typoTolerance.driver tidak valid: %q (pilih trigram atau pg_trgm).", t.Driver)
		}
	}
	for _, c := range cfg.Columns {
		if c.Field == "" {
			return ConfigErrorf("Kolom pencarian butuh field.")
		}
		switch c.Mode {
		case "", ModeContains, ModeStartsWith, ModeEquals:
		default:
			return ConfigErrorf("Mode pencarian kolom %q tidak valid: %q.", c.Field, c.Mode)
		}
		switch c.Type {
		case "", ColumnString, ColumnNumber:
		default:
			return ConfigErrorf("Tipe kolom pencarian %q tidak valid: %q.", c.Field, c.Type)
		}
	}
	return nil
}
