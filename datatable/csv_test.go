package datatable

import (
	"database/sql/driver"
	"testing"
	"time"
)

func TestToCSV(t *testing.T) {
	type status string
	n := 3
	var nilPtr *int
	tests := []struct {
		name    string
		headers []string
		rows    [][]any
		opts    CSVOptions
		want    string
	}{
		{"menetralkan rumus dan meng-escape", []string{"a"}, [][]any{{"=1+1"}, {`x,"y"`}}, CSVOptions{NoBOM: true}, "a\r\n'=1+1\r\n\"x,\"\"y\"\"\""},
		{"BOM default", []string{"a"}, nil, CSVOptions{}, "\uFEFFa"},
		{"angka negatif tidak dinetralkan", []string{"n"}, [][]any{{-5}, {"-5"}, {1.5}, {int64(10)}}, CSVOptions{NoBOM: true}, "n\r\n-5\r\n'-5\r\n1.5\r\n10"},
		{"semua awalan berbahaya", []string{"x"}, [][]any{{"+a"}, {"@b"}, {"\tc"}, {"\rd"}}, CSVOptions{NoBOM: true}, "x\r\n'+a\r\n'@b\r\n'\tc\r\n\"'\rd\""},
		{"nil, pointer, bool, tipe turunan", []string{"a", "b", "c", "d", "e"}, [][]any{{nil, &n, true, status("aktif"), nilPtr}}, CSVOptions{NoBOM: true}, "a,b,c,d,e\r\n,3,true,aktif,"},
		{"waktu ISO UTC", []string{"t"}, [][]any{{time.Date(2026, 1, 5, 10, 0, 0, 0, time.FixedZone("WITA", 8*3600))}}, CSVOptions{NoBOM: true}, "t\r\n2026-01-05T02:00:00.000Z"},
		{"delimiter titik koma", []string{"a", "b"}, [][]any{{"x;y", "z,w"}, {"baris\nbaru", ""}}, CSVOptions{Delimiter: ";", NoBOM: true}, "a;b\r\n\"x;y\";z,w\r\n\"baris\nbaru\";"},
		{"header juga dinetralkan", []string{"=h"}, nil, CSVOptions{NoBOM: true}, "'=h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToCSVWith(tt.headers, tt.rows, tt.opts); got != tt.want {
				t.Fatalf("got %q\nwant %q", got, tt.want)
			}
		})
	}
	if ToCSV([]string{"a"}, nil) != "\uFEFFa" {
		t.Error("ToCSV harus menulis BOM")
	}
}

func TestFormatNumber(t *testing.T) {
	tests := map[float64]string{
		0: "0", 1: "1", -5: "-5", 1.5: "1.5", 0.1: "0.1", 1e21: "1e+21", 1.5e-7: "1.5e-7",
		0.000001: "0.000001", 123456789012: "123456789012",
	}
	for in, want := range tests {
		if got := FormatNumber(in); got != want {
			t.Errorf("FormatNumber(%v) = %q want %q", in, got, want)
		}
	}
}

func TestParseNumber(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"", 0, true}, {"  ", 0, true}, {"12", 12, true}, {"-1.5", -1.5, true}, {".5", 0.5, true},
		{"1e3", 1000, true}, {"0x1F", 31, true}, {"0b11", 3, true}, {"0o17", 15, true},
		{"-0x10", 0, false}, {"1_000", 0, false}, {"abc", 0, false}, {"NaN", 0, false},
		{"Infinity", 0, false}, {"0x1p3", 0, false}, {"12px", 0, false},
	}
	for _, tt := range tests {
		got, ok := ParseNumber(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("ParseNumber(%q) = %v,%v", tt.in, got, ok)
		}
	}
}

type valuer struct{ s string }

func (v valuer) Value() (driver.Value, error) { return v.s, nil }

func TestPrimitive(t *testing.T) {
	type myTime time.Time
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var nilValuer *valuer
	s := "x"
	tests := []struct {
		in   any
		want any
	}{
		{nil, nil}, {3, 3.0}, {uint8(2), 2.0}, {float32(1.5), 1.5}, {"a", "a"}, {&s, "x"},
		{true, true}, {now, now}, {myTime(now), now}, {valuer{"v"}, "v"}, {nilValuer, nil},
		{[]byte("b"), "b"},
	}
	for i, tt := range tests {
		if got := Primitive(tt.in); got != tt.want {
			t.Errorf("#%d Primitive(%#v) = %#v", i, tt.in, got)
		}
	}
	if FormatValue(nil) != "" || FormatValue(12.0) != "12" || FormatValue(now) != "2026-01-01T00:00:00.000Z" {
		t.Error("FormatValue")
	}
}

func TestErrors(t *testing.T) {
	qe := QueryErrorf("Kolom %q tidak bisa diurutkan.", "password")
	if qe.Error() != `Kolom "password" tidak bisa diurutkan.` || qe.StatusCode() != 400 || qe.Unwrap() != nil {
		t.Errorf("QueryError: %v", qe)
	}
	if ConfigErrorf("x %d", 1).Error() != "x 1" {
		t.Error("ConfigError")
	}
	if (&FilterDefinitionError{Msg: "m"}).Error() != "m" {
		t.Error("FilterDefinitionError")
	}
}
