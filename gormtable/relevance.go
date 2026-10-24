package gormtable

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
)

// Peringkat relevansi lewat SQL mentah PostgreSQL — setara
// `buildRelevanceSubquery` versi Laravel. Engine:
//  1. menghitung skor kandidat dengan satu query mentah (identifier
//     divalidasi, nilai selalu lewat parameter — aman dari SQL injection),
//  2. lalu memfilter & mem-paginate lewat GORM memakai id kandidat.
//
// Hanya dipakai saat ada kata kunci dan pengguna tidak memilih sort; bila
// pengguna mengurutkan kolom, pencarian kembali ke SQL biasa (ILIKE/LIKE).

// Relevance mengaktifkan peringkat relevansi PostgreSQL.
type Relevance struct {
	// Table: nama tabel database (boleh `schema.tabel`). Default nama tabel
	// dari schema GORM.
	Table string
	// Joins: JOIN agar kolom relasi ikut diberi skor. Kolom pencarian lalu
	// boleh berupa `alias.kolom` (lewat SearchColumn.Column, atau Field
	// dot-path yang awalannya alias join).
	Joins []RelevanceJoin
	// PrimaryKeyColumn: kolom primary key di database. Default dari schema.
	PrimaryKeyColumn string
	// MaxCandidates: batas kandidat yang diberi skor. Default 2000.
	MaxCandidates int
}

// RelevanceJoin adalah JOIN tambahan untuk mode relevansi. Semua bagian
// divalidasi sebagai identifier — On hanya boleh kesetaraan dua kolom
// `a.b = c.d`, bukan SQL bebas.
type RelevanceJoin struct {
	// Table yang di-join (boleh `schema.tabel`).
	Table string
	// Alias tabel; default segmen terakhir Table.
	Alias string
	// On: kondisi `"c.id = orders.customer_id"`; beberapa digabung AND.
	On []string
	// Type: "left" (default) atau "inner".
	Type string
}

// QuoteIdent meng-quote identifier PostgreSQL (`a.b` → `"a"."b"`) setelah
// memvalidasi setiap segmen.
func QuoteIdent(name string) (string, error) {
	parts := strings.Split(name, ".")
	for _, p := range parts {
		if !identRE.MatchString(p) {
			return "", dt.ConfigErrorf("Identifier SQL tidak valid: %q", name)
		}
	}
	for i, p := range parts {
		parts[i] = `"` + p + `"`
	}
	return strings.Join(parts, "."), nil
}

func mustQuote(name string) string {
	q, err := QuoteIdent(name)
	if err != nil {
		panic(err) // hanya dipanggil untuk nama yang sudah divalidasi
	}
	return q
}

// escapeLikeBackslash: escape LIKE gaya PostgreSQL (`\`).
func escapeLikeBackslash(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\\' || r == '%' || r == '_' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func lastSegment(name string) string { return name[strings.LastIndexByte(name, '.')+1:] }

var joinOnRE = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)\s*$`)

type compiledJoins struct {
	names map[string]bool // tabel dasar + alias join
	order []string
	sql   string
}

func compileJoins(cfg Relevance) (compiledJoins, error) {
	base := lastSegment(cfg.Table)
	out := compiledJoins{names: map[string]bool{base: true}, order: []string{base}}
	var parts []string
	for _, j := range cfg.Joins {
		if _, err := QuoteIdent(j.Table); err != nil {
			return out, err
		}
		alias := j.Alias
		if alias == "" {
			alias = lastSegment(j.Table)
		}
		if !identRE.MatchString(alias) {
			return out, dt.ConfigErrorf("Alias join tidak valid: %q", alias)
		}
		if out.names[alias] {
			return out, dt.ConfigErrorf("Alias join duplikat: %q — isi `Alias` yang berbeda.", alias)
		}
		if j.Type != "" && j.Type != "left" && j.Type != "inner" {
			return out, dt.ConfigErrorf("Tipe join tidak valid: %q (pilih \"left\" atau \"inner\").", j.Type)
		}
		out.names[alias] = true
		out.order = append(out.order, alias)

		if len(j.On) == 0 {
			return out, dt.ConfigErrorf("Join %q butuh kondisi `On`.", j.Table)
		}
		conds := make([]string, 0, len(j.On))
		for _, on := range j.On {
			m := joinOnRE.FindStringSubmatch(on)
			if m == nil {
				return out, dt.ConfigErrorf("Kondisi join %q tidak valid — hanya boleh bentuk `a.kolom = b.kolom`.", on)
			}
			for _, q := range []string{m[1], m[3]} {
				if !out.names[q] {
					return out, dt.ConfigErrorf("Kondisi join %q merujuk tabel/alias tidak dikenal: %q.", on, q)
				}
			}
			conds = append(conds, mustQuote(m[1]+"."+m[2])+" = "+mustQuote(m[3]+"."+m[4]))
		}
		kw := "LEFT JOIN"
		if j.Type == "inner" {
			kw = "INNER JOIN"
		}
		as := ""
		if alias != j.Table {
			as = " AS " + mustQuote(alias)
		}
		parts = append(parts, kw+" "+mustQuote(j.Table)+as+" ON "+strings.Join(conds, " AND "))
	}
	out.sql = strings.Join(parts, " ")
	return out, nil
}

// columnRef: referensi kolom pencarian sebagai SQL (di-quote & divalidasi).
func columnRef(name string, cfg Relevance, joins compiledJoins) (string, error) {
	if len(cfg.Joins) == 0 {
		if strings.Contains(name, ".") {
			return "", dt.ConfigErrorf("Mode relevansi tanpa `Joins` hanya mendukung kolom langsung di tabel %q; %q adalah relasi. "+
				"Tambahkan `Relevance.Joins`, isi `Column` dengan nama kolom asli, atau keluarkan dari daftar pencarian.", cfg.Table, name)
		}
		return QuoteIdent(name)
	}
	parts := strings.Split(name, ".")
	if len(parts) == 1 {
		return QuoteIdent(lastSegment(cfg.Table) + "." + name)
	}
	if len(parts) != 2 || !joins.names[parts[0]] {
		return "", dt.ConfigErrorf("Kolom relevansi %q harus berbentuk `alias.kolom` dengan alias dari `Relevance.Joins` (%s). "+
			"Isi `Column` bila nama kolom berbeda.", name, strings.Join(joins.order, ", "))
	}
	return QuoteIdent(name)
}

// relevanceColumn: nama kolom yang dipakai mode relevansi untuk satu kolom
// pencarian — Column bila diisi, selain itu nama kolom DB hasil pemetaan
// schema (field langsung) atau Field apa adanya (dot-path `alias.kolom`).
func relevanceColumn(sc dt.SearchColumn, dbName string) string {
	if sc.Column != "" {
		return sc.Column
	}
	if !strings.Contains(sc.Field, ".") && dbName != "" {
		return dbName
	}
	return sc.Field
}

// ValidateRelevance memeriksa konfigurasi relevansi terhadap kolom
// pencarian (Column/Field apa adanya). Kesalahan bertipe *datatable.ConfigError.
func ValidateRelevance(cfg Relevance, s dt.NormalizedSearch) error {
	if _, err := QuoteIdent(cfg.Table); err != nil {
		return err
	}
	if _, err := QuoteIdent(pkColumn(cfg)); err != nil {
		return err
	}
	joins, err := compileJoins(cfg)
	if err != nil {
		return err
	}
	for _, c := range s.Columns {
		if _, err := columnRef(relevanceColumn(c, ""), cfg, joins); err != nil {
			return err
		}
	}
	return nil
}

func pkColumn(cfg Relevance) string {
	if cfg.PrimaryKeyColumn != "" {
		return cfg.PrimaryKeyColumn
	}
	return "id"
}

// BuildRelevanceSQL menyusun query skor relevansi (PostgreSQL). Placeholder
// memakai `$n` seperti versi JS — satu parameter dipakai ulang di kondisi
// WHERE dan skor CASE. ok=false bila tidak ada kata kunci/kolom.
// Kolom diambil dari SearchColumn.Column, lalu Field apa adanya.
func BuildRelevanceSQL(search string, cfg Relevance, s dt.NormalizedSearch) (sql string, args []any, ok bool, err error) {
	return buildRelevanceSQL(search, cfg, s, nil)
}

func buildRelevanceSQL(search string, cfg Relevance, s dt.NormalizedSearch, dbNames []string) (string, []any, bool, error) {
	words := dt.SplitKeywords(search)
	if len(words) == 0 || len(s.Columns) == 0 {
		return "", nil, false, nil
	}
	joins, err := compileJoins(cfg)
	if err != nil {
		return "", nil, false, err
	}
	refs := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		db := ""
		if dbNames != nil {
			db = dbNames[i]
		}
		ref, err := columnRef(relevanceColumn(c, db), cfg, joins)
		if err != nil {
			return "", nil, false, err
		}
		refs[i] = ref
	}

	var args []any
	p := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }

	fuzzyOn := s.Typo.Driver != dt.TypoNone
	exactMultiplier := 1
	if fuzzyOn {
		exactMultiplier = 100
	}
	var scoreParts, wordConds []string
	for _, word := range words {
		lower := strings.ToLower(word)
		fuzzy := fuzzyOn && utf8.RuneCountInString(word) >= s.Typo.MinWordLength
		var colConds []string
		for i, col := range s.Columns {
			c := "LOWER(CAST(" + refs[i] + " AS TEXT))"
			var pattern string
			switch col.Mode {
			case dt.ModeEquals:
				pattern = escapeLikeBackslash(lower)
			case dt.ModeStartsWith:
				pattern = escapeLikeBackslash(lower) + "%"
			default:
				pattern = "%" + escapeLikeBackslash(lower) + "%"
			}
			like := c + " LIKE " + p(pattern) + ` ESCAPE '\'`
			colConds = append(colConds, like)
			scoreParts = append(scoreParts, "CASE WHEN "+like+" THEN "+strconv.Itoa(col.Weight*exactMultiplier)+" ELSE 0 END")

			if !fuzzy {
				continue
			}
			if s.Typo.Driver == dt.TypoTrigram {
				for _, g := range dt.Trigrams(word) {
					gl := c + " LIKE " + p("%"+escapeLikeBackslash(g)+"%") + ` ESCAPE '\'`
					colConds = append(colConds, gl)
					scoreParts = append(scoreParts, "CASE WHEN "+gl+" THEN 1 ELSE 0 END")
				}
			} else {
				tg := c + " % " + p(lower)
				colConds = append(colConds, tg)
				scoreParts = append(scoreParts, "CASE WHEN "+tg+" THEN 1 ELSE 0 END")
			}
		}
		wordConds = append(wordConds, "("+strings.Join(colConds, " OR ")+")")
	}

	sep := " AND "
	if s.Match == dt.MatchAny {
		sep = " OR "
	}
	where := strings.Join(wordConds, sep)
	limit := cfg.MaxCandidates
	if limit <= 0 {
		limit = 2000
	}
	score := "(" + strings.Join(scoreParts, " + ") + ")"
	table := mustQuote(cfg.Table)
	pkName := pkColumn(cfg)
	if _, err := QuoteIdent(pkName); err != nil {
		return "", nil, false, err
	}

	if joins.sql == "" {
		sql := "SELECT " + mustQuote(pkName) + " AS id, " + score + " AS score " +
			"FROM " + table + " WHERE " + where + " ORDER BY score DESC LIMIT " + strconv.Itoa(limit)
		return sql, args, true, nil
	}
	// Join one-to-many bisa menggandakan baris → ambil skor tertinggi per id.
	pk := mustQuote(lastSegment(cfg.Table) + "." + pkName)
	sql := "SELECT id, MAX(score) AS score FROM (" +
		"SELECT " + pk + " AS id, " + score + " AS score FROM " + table + " " + joins.sql + " WHERE " + where +
		") AS mrc_scored GROUP BY id ORDER BY score DESC LIMIT " + strconv.Itoa(limit)
	return sql, args, true, nil
}
