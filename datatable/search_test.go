package datatable

import (
	"reflect"
	"strings"
	"testing"
)

func TestHighlight(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		search string
		want   []Segment
	}{
		{"mempertahankan huruf asli", "Coba banget", "cob", []Segment{{"Cob", true}, {"a banget", false}}},
		{"teks kosong", "", "x", []Segment{}},
		{"tanpa kata kunci", "Budi", "  ", []Segment{{"Budi", false}}},
		{"kata dipisah per spasi", "budi santoso", "budi budi santoso", []Segment{{"budi", true}, {" ", false}, {"santoso", true}}},
		{"kecocokan berdempetan digabung", "abcd", "ab cd", []Segment{{"abcd", true}}},
		{"banyak kecocokan", "Budi dan budi", "BUDI", []Segment{{"Budi", true}, {" dan ", false}, {"budi", true}}},
		{"karakter regex di-escape", "a.b (c)", "(c) .", []Segment{{"a", false}, {".", true}, {"b ", false}, {"(c)", true}}},
		{"unicode", "Ärger über", "är", []Segment{{"Är", true}, {"ger über", false}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Highlight(tt.text, tt.search); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v", got)
			}
		})
	}
}

func TestHighlightNoHTML(t *testing.T) {
	segs := Highlight("<script>", "script")
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	if b.String() != "<script>" {
		t.Fatalf("got %q", b.String())
	}
	if got := HighlightWords("budi santoso!", []string{"budi", "budi santoso"}); !reflect.DeepEqual(got, []Segment{{"budi santoso", true}, {"!", false}}) {
		t.Fatalf("kata terpanjang menang: %#v", got)
	}
	if got := HighlightWords("abc", []string{"", "b"}); !reflect.DeepEqual(got, []Segment{{"a", false}, {"b", true}, {"c", false}}) {
		t.Fatalf("HighlightWords: %#v", got)
	}
}

func TestSplitKeywords(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"  budi   santoso ", []string{"budi", "santoso"}},
		{"Budi budi BUDI x", []string{"Budi", "x"}},
		{"a\tb\nc", []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		if got := SplitKeywords(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitKeywords(%q) = %#v", tt.in, got)
		}
	}
}

func TestTrigrams(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"budi", []string{"bud", "udi"}},
		{"BUDI", []string{"bud", "udi"}},
		{"ab", []string{}},
		{"aaaa", []string{"aaa"}},
		{"çaña", []string{"çañ", "aña"}},
	}
	for _, tt := range tests {
		if got := Trigrams(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Trigrams(%q) = %#v", tt.in, got)
		}
	}
	if got := NGrams("abc", 2); !reflect.DeepEqual(got, []string{"ab", "bc"}) {
		t.Errorf("NGrams: %#v", got)
	}
}

func TestNormalizeSearch(t *testing.T) {
	n := NormalizeSearch(nil)
	if n.Match != MatchAll || n.Typo.Driver != TypoNone || n.Typo.MinWordLength != 4 || len(n.Columns) != 0 {
		t.Errorf("nil: %+v", n)
	}
	n = NormalizeSearch(&SearchConfig{
		Columns:       []SearchColumn{{Field: "nama"}, Col("kode").W(3).Equals().As("kode_brg"), Col("umur").Numeric().StartsWith()},
		Match:         MatchAny,
		TypoTolerance: &TypoTolerance{},
	})
	want := []SearchColumn{
		{Field: "nama", Weight: 1, Mode: ModeContains, Type: ColumnString},
		{Field: "kode", Column: "kode_brg", Weight: 3, Mode: ModeEquals, Type: ColumnString},
		{Field: "umur", Weight: 1, Mode: ModeStartsWith, Type: ColumnNumber},
	}
	if !reflect.DeepEqual(n.Columns, want) || n.Match != MatchAny || n.Typo != (NormalizedTypo{TypoTrigram, 4}) {
		t.Errorf("normalized: %+v", n)
	}
	if n := NormalizeSearch(&SearchConfig{TypoTolerance: &TypoTolerance{Driver: TypoPgTrgm, MinWordLength: -3}}); n.Typo != (NormalizedTypo{TypoPgTrgm, 1}) {
		t.Errorf("pg_trgm: %+v", n.Typo)
	}
}

func TestSearchConfigValidate(t *testing.T) {
	tests := []struct {
		cfg *SearchConfig
		ok  bool
	}{
		{nil, true},
		{&SearchConfig{Columns: []SearchColumn{Col("a")}}, true},
		{&SearchConfig{Match: "some"}, false},
		{&SearchConfig{TypoTolerance: &TypoTolerance{Driver: "levenshtein"}}, false},
		{&SearchConfig{Columns: []SearchColumn{{}}}, false},
		{&SearchConfig{Columns: []SearchColumn{{Field: "a", Mode: "regex"}}}, false},
		{&SearchConfig{Columns: []SearchColumn{{Field: "a", Type: "date"}}}, false},
	}
	for i, tt := range tests {
		err := tt.cfg.Validate()
		if (err == nil) != tt.ok {
			t.Errorf("#%d: err=%v", i, err)
		}
		if err != nil {
			if _, isCfg := err.(*ConfigError); !isCfg {
				t.Errorf("#%d: bukan ConfigError: %T", i, err)
			}
		}
	}
}
