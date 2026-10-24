package datatable

import (
	"regexp"
	"strings"
)

// CSVOptions mengatur ToCSVWith.
type CSVOptions struct {
	// Delimiter: pemisah kolom. Default ","; pakai ";" untuk Excel berlokal
	// Indonesia.
	Delimiter string
	// NoBOM: jangan tulis BOM UTF-8 (default ditulis agar Excel membaca
	// karakter non-ASCII dengan benar).
	NoBOM bool
}

// Sel yang diawali = + - @ (atau tab/CR) dieksekusi sebagai rumus oleh
// Excel/Sheets (CSV injection). Prefiks apostrof membuatnya tampil sebagai
// teks biasa.
var formulaRE = regexp.MustCompile(`^[=+\-@\t\r]`)

func csvCell(value any, delimiter string) string {
	p := Primitive(value)
	if p == nil {
		return ""
	}
	text := FormatValue(p)
	if _, isNum := p.(float64); !isNum && formulaRE.MatchString(text) {
		text = "'" + text
	}
	if strings.ContainsAny(text, "\"\r\n") || strings.Contains(text, delimiter) {
		return `"` + strings.ReplaceAll(text, `"`, `""`) + `"`
	}
	return text
}

// ToCSV menyusun CSV (pemisah ",", baris CRLF, dengan BOM UTF-8) yang aman
// dari formula injection. Angka tidak dinetralkan (agar -5 tetap angka);
// time.Time ditulis ISO 8601 UTC.
func ToCSV(headers []string, rows [][]any) string {
	return ToCSVWith(headers, rows, CSVOptions{})
}

// ToCSVWith sama dengan ToCSV dengan opsi.
func ToCSVWith(headers []string, rows [][]any, opts CSVOptions) string {
	d := opts.Delimiter
	if d == "" {
		d = ","
	}
	lines := make([]string, 0, len(rows)+1)
	cells := make([]string, len(headers))
	for i, h := range headers {
		cells[i] = csvCell(h, d)
	}
	lines = append(lines, strings.Join(cells, d))
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, v := range r {
			cells[i] = csvCell(v, d)
		}
		lines = append(lines, strings.Join(cells, d))
	}
	bom := "\uFEFF"
	if opts.NoBOM {
		bom = ""
	}
	return bom + strings.Join(lines, "\r\n")
}
