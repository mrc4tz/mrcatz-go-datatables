package gormtable

import (
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// identRE: satu segmen identifier SQL yang aman.
var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// keyRE: kunci json (boleh dot-path) yang boleh dipetakan ke kolom.
var keyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// column adalah hasil pemetaan json key → kolom database.
type column struct {
	key   string
	table string // clause.CurrentTable atau alias join GORM (mis. "Kategori", "Kategori__Induk")
	name  string // nama kolom DB
	field *schema.Field
}

func (c column) expr() clause.Column { return clause.Column{Table: c.table, Name: c.name} }

// resolver memetakan json key ke kolom lewat schema GORM.
type resolver struct {
	sch   *schema.Schema
	joins map[string]bool // path relasi yang di-join: "Kategori", "Kategori.Induk"
}

// jsonName mengembalikan nama json sebuah field struct ("" bila `json:"-"`).
func jsonName(tag string, goName string) string {
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return ""
	}
	if name == "" {
		return goName
	}
	return name
}

func findField(s *schema.Schema, seg string) *schema.Field {
	// Urutan prioritas: nama json → nama kolom DB → nama field Go.
	for _, f := range s.Fields {
		if f.DBName != "" && jsonName(f.Tag.Get("json"), f.Name) == seg {
			return f
		}
	}
	for _, f := range s.Fields {
		if f.DBName == seg {
			return f
		}
	}
	for _, f := range s.Fields {
		if f.DBName != "" && strings.EqualFold(f.Name, seg) {
			return f
		}
	}
	return nil
}

func findRelation(s *schema.Schema, seg string) *schema.Relationship {
	for _, r := range s.Relationships.Relations {
		if r.Field != nil && jsonName(r.Field.Tag.Get("json"), r.Name) == seg {
			return r
		}
	}
	for _, r := range s.Relationships.Relations {
		if strings.EqualFold(r.Name, seg) {
			return r
		}
	}
	return nil
}

func joinable(r *schema.Relationship) bool {
	return r.Type == schema.BelongsTo || r.Type == schema.HasOne
}

// validateJoins memeriksa daftar Definition.Joins terhadap schema.
func validateJoins(s *schema.Schema, joins []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, j := range joins {
		cur := s
		parts := strings.Split(j, ".")
		for i, p := range parts {
			if !identRE.MatchString(p) {
				return nil, fmt.Errorf("nama join tidak valid: %q", j)
			}
			r, ok := cur.Relationships.Relations[p]
			if !ok {
				return nil, fmt.Errorf("join %q: asosiasi %q tidak ada di %s", j, p, cur.Name)
			}
			if !joinable(r) {
				return nil, fmt.Errorf("join %q: asosiasi %q bertipe %s — Joins GORM hanya untuk belongs_to/has_one", j, p, r.Type)
			}
			out[strings.Join(parts[:i+1], ".")] = true
			cur = r.FieldSchema
		}
	}
	return out, nil
}

// resolve memetakan json key (boleh dot-path) ke kolom.
func (r *resolver) resolve(key string) (column, error) {
	if !keyRE.MatchString(key) {
		return column{}, fmt.Errorf("kunci %q bukan identifier yang valid", key)
	}
	segs := strings.Split(key, ".")
	cur := r.sch
	var path []string
	for _, seg := range segs[:len(segs)-1] {
		rel := findRelation(cur, seg)
		if rel == nil {
			return column{}, fmt.Errorf("kunci %q: relasi %q tidak ditemukan di %s", key, seg, cur.Name)
		}
		path = append(path, rel.Name)
		name := strings.Join(path, ".")
		if !joinable(rel) {
			return column{}, fmt.Errorf("kunci %q: relasi %q bertipe %s, tidak bisa di-join", key, rel.Name, rel.Type)
		}
		if !r.joins[name] {
			return column{}, fmt.Errorf("kunci %q butuh join asosiasi — tambahkan %q ke Joins", key, name)
		}
		cur = rel.FieldSchema
	}
	last := segs[len(segs)-1]
	f := findField(cur, last)
	if f == nil {
		return column{}, fmt.Errorf("kunci %q: kolom %q tidak ditemukan di %s", key, last, cur.Name)
	}
	if !identRE.MatchString(f.DBName) {
		return column{}, fmt.Errorf("kunci %q: nama kolom %q tidak valid", key, f.DBName)
	}
	table := clause.CurrentTable
	if len(path) > 0 {
		table = strings.Join(path, "__")
	}
	return column{key: key, table: table, name: f.DBName, field: f}, nil
}
