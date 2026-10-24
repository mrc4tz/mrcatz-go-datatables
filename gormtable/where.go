package gormtable

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WhereFunc adalah kondisi filter kustom (pengganti `createWithCallback`).
// Terima chain *gorm.DB yang sedang dibangun (sudah berisi Joins & BaseWhere)
// lalu kembalikan chain dengan kondisi tambahan, mis.
//
//	gormtable.Where(func(db *gorm.DB, v datatable.Resolved, fc datatable.FilterContext) *gorm.DB {
//		if v.Scalar == true {
//			return db.Where("EXISTS (SELECT 1 FROM lampiran l WHERE l.laporan_id = laporan.id)")
//		}
//		return db
//	})
type WhereFunc func(db *gorm.DB, v dt.Resolved, fc dt.FilterContext) *gorm.DB

// Where memasang WhereFunc pada definisi filter (opsi datatable.FilterOption).
func Where(fn WhereFunc) dt.FilterOption { return dt.WithWhere(fn) }

// whereFuncOf menerima WhereFunc atau fungsi bertanda tangan sama.
func whereFuncOf(v any) (WhereFunc, bool) {
	switch fn := v.(type) {
	case WhereFunc:
		return fn, fn != nil
	case func(*gorm.DB, dt.Resolved, dt.FilterContext) *gorm.DB:
		return fn, fn != nil
	}
	return nil, false
}

// sqlOpts adalah opsi pembangun kondisi.
type sqlOpts struct {
	insensitive bool
	postgres    bool
	off         dt.UTCOffset
}

// likeEscape: karakter escape LIKE yang portabel (PostgreSQL, MySQL,
// SQLite, SQL Server) — backslash bermasalah di literal string MySQL.
const likeEscape = "!"

func escapeLike(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '!' || r == '%' || r == '_' {
			b.WriteByte('!')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// textExpr membangun kondisi teks (contains/startsWith/equals) yang setara
// `{ contains, mode: 'insensitive' }` Prisma.
func textExpr(col clause.Column, mode dt.SearchMode, value string, o sqlOpts) clause.Expression {
	pattern := escapeLike(value)
	switch mode {
	case dt.ModeStartsWith:
		pattern += "%"
	case dt.ModeEquals:
	default:
		pattern = "%" + pattern + "%"
	}
	switch {
	case o.insensitive && o.postgres:
		return clause.Expr{SQL: "? ILIKE ? ESCAPE '" + likeEscape + "'", Vars: []any{col, pattern}}
	case o.insensitive:
		return clause.Expr{SQL: "LOWER(?) LIKE ? ESCAPE '" + likeEscape + "'", Vars: []any{col, strings.ToLower(pattern)}}
	case mode == dt.ModeEquals:
		return clause.Eq{Column: col, Value: value}
	default:
		return clause.Expr{SQL: "? LIKE ? ESCAPE '" + likeEscape + "'", Vars: []any{col, pattern}}
	}
}

// sqlValue menormalkan nilai sebelum dikirim ke driver: float64 bulat → int64
// (agar aman dibandingkan dengan kolom integer di semua driver).
func sqlValue(v any) any {
	if f, ok := v.(float64); ok && f == math.Trunc(f) && math.Abs(f) < 1<<53 {
		return int64(f)
	}
	return v
}

// filterExpr menerjemahkan filter berbasis field ke kondisi SQL. nil = tidak
// ada kondisi (mis. rentang tanpa batas).
func filterExpr(f dt.Filter, col clause.Column, v dt.Resolved, fc dt.FilterContext, o sqlOpts) (clause.Expression, error) {
	switch f.Type {
	case dt.FilterSelect:
		val := sqlValue(v.Scalar)
		switch f.EffectiveOperator(fc) {
		case dt.OpContains:
			return textExpr(col, dt.ModeContains, dt.FormatValue(v.Scalar), o), nil
		case dt.OpNe:
			return clause.Neq{Column: col, Value: val}, nil
		case dt.OpGt:
			return clause.Gt{Column: col, Value: val}, nil
		case dt.OpGte:
			return clause.Gte{Column: col, Value: val}, nil
		case dt.OpLt:
			return clause.Lt{Column: col, Value: val}, nil
		case dt.OpLte:
			return clause.Lte{Column: col, Value: val}, nil
		default:
			return clause.Eq{Column: col, Value: val}, nil
		}
	case dt.FilterMulti:
		vals := make([]any, len(v.Values))
		for i, x := range v.Values {
			vals[i] = sqlValue(x)
		}
		in := clause.IN{Column: col, Values: vals}
		if v.Exclude {
			return clause.Not(in), nil
		}
		return in, nil
	case dt.FilterDate, dt.FilterDateRange:
		b, err := dt.FilterBounds(f, v, fc, o.off)
		if err != nil {
			return nil, err
		}
		return boundsExpr(col, b), nil
	}
	return nil, nil
}

// boundsExpr: [gte, lt) atau NOT (col >= start AND col < end).
func boundsExpr(col clause.Column, b dt.Bounds) clause.Expression {
	if b.Not != nil {
		// Sengaja Expr mentah: clause.Not(And(..)) milik GORM menegasikan per
		// bagian (bukan NOT dari gabungannya).
		return clause.Expr{SQL: "NOT (? >= ? AND ? < ?)", Vars: []any{col, b.Not.Start, col, b.Not.End}}
	}
	var parts []clause.Expression
	if b.GTE != nil {
		parts = append(parts, clause.Gte{Column: col, Value: *b.GTE})
	}
	if b.LT != nil {
		parts = append(parts, clause.Lt{Column: col, Value: *b.LT})
	}
	if len(parts) == 0 {
		return nil
	}
	return clause.And(parts...)
}

var numericWordRE = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// numericValue: kata kunci angka → int64 bila bulat, selain itu float64.
func numericValue(word string) any {
	if n, err := strconv.ParseInt(word, 10, 64); err == nil {
		return n
	}
	f, _ := strconv.ParseFloat(word, 64)
	return f
}

// noRows: kata kunci tak bisa dicocokkan ke kolom mana pun (setara `OR: []`).
var noRows = clause.Expr{SQL: "1 = 0"}

// searchExpr: kondisi pencarian versi SQL biasa (tanpa skor). nil = tanpa
// pencarian.
func searchExpr(search string, cfg dt.NormalizedSearch, cols []column, o sqlOpts) clause.Expression {
	words := dt.SplitKeywords(search)
	if len(words) == 0 || len(cfg.Columns) == 0 {
		return nil
	}
	var perWord []clause.Expression
	for _, word := range words {
		fuzzy := cfg.Typo.Driver == dt.TypoTrigram && utf8.RuneCountInString(word) >= cfg.Typo.MinWordLength
		var or []clause.Expression
		for i, sc := range cfg.Columns {
			if cols[i].name == "" {
				continue // kolom khusus relevansi
			}
			col := cols[i].expr()
			if sc.Type == dt.ColumnNumber {
				if numericWordRE.MatchString(word) {
					or = append(or, clause.Eq{Column: col, Value: numericValue(word)})
				}
				continue
			}
			or = append(or, textExpr(col, sc.Mode, word, o))
			if fuzzy {
				for _, g := range dt.Trigrams(word) {
					or = append(or, textExpr(col, dt.ModeContains, g, o))
				}
			}
		}
		if len(or) > 0 {
			perWord = append(perWord, anyOf(or))
		}
	}
	switch {
	case len(perWord) == 0:
		return noRows
	case len(perWord) == 1:
		return perWord[0]
	case cfg.Match == dt.MatchAny:
		return anyOf(perWord)
	default:
		return clause.AndConditions{Exprs: perWord}
	}
}

// whereClause membungkus ekspresi menjadi scope chain.
func whereClause(e clause.Expression) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Clauses(clause.Where{Exprs: []clause.Expression{e}})
	}
}

// anyOf menggabungkan dengan OR. Satu elemen dikembalikan apa adanya: GORM
// menyambung OrConditions berisi satu elemen dengan " OR " ke kondisi
// sebelumnya, yang akan merusak AND antar-filter.
func anyOf(exprs []clause.Expression) clause.Expression {
	if len(exprs) == 1 {
		return exprs[0]
	}
	return clause.OrConditions{Exprs: exprs}
}
