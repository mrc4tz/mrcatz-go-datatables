package datatable

import (
	"database/sql/driver"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// isJSSpace meniru himpunan whitespace JavaScript (`String.prototype.trim`).
func isJSSpace(r rune) bool { return r == '\uFEFF' || unicode.IsSpace(r) }

// trimJS memangkas whitespace seperti `String.prototype.trim` di JS.
func trimJS(s string) string { return strings.TrimFunc(s, isJSSpace) }

// ParseNumber meniru `Number(s)` JavaScript untuk string: whitespace di tepi
// diabaikan, string kosong = 0, mendukung desimal, eksponen, dan prefiks
// 0x/0o/0b. ok=false bila hasilnya NaN atau tak hingga.
func ParseNumber(s string) (float64, bool) {
	s = trimJS(s)
	if s == "" {
		return 0, true
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			digits := s[2:]
			if strings.ContainsAny(digits, "_+-") {
				return 0, false
			}
			n, err := strconv.ParseUint(digits, base, 64)
			if err != nil {
				return 0, false
			}
			return float64(n), true
		}
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !strings.ContainsRune(".eE+-", r) {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// FormatNumber meniru `String(n)` JavaScript untuk angka.
func FormatNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	abs := math.Abs(f)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'e', -1, 64) // mis. 1e+21, 1.5e-07
	mant, exp, _ := strings.Cut(s, "e")
	sign := exp[0]
	exp = strings.TrimLeft(exp[1:], "0")
	if exp == "" {
		exp = "0"
	}
	return mant + "e" + string(sign) + exp
}

// FormatValue mengubah nilai skalar menjadi string seperti `String(v ?? ”)`
// di JS: nil → "", angka ditulis gaya JS, time.Time → ISO 8601 UTC
// (`toISOString`), pointer diikuti, driver.Valuer dibaca nilainya.
func FormatValue(v any) string {
	v = Primitive(v)
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return FormatNumber(x)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		return ISOString(x)
	}
	return fmt.Sprint(v)
}

// ISOString meniru `Date.prototype.toISOString`: UTC, milidetik, akhiran Z.
func ISOString(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

var timeType = reflect.TypeOf(time.Time{})

// Primitive menyederhanakan nilai Go menjadi primitif bergaya JS: nil,
// string, float64 (semua jenis angka), bool, atau time.Time. Pointer nil →
// nil; tipe turunan (`type Status string`) dibuka ke jenis dasarnya;
// driver.Valuer (mis. sql.NullString) dibaca lewat Value(). Nilai lain
// dikembalikan apa adanya.
func Primitive(v any) any {
	for depth := 0; depth < 8; depth++ {
		if v == nil {
			return nil
		}
		if t, ok := v.(time.Time); ok {
			return t
		}
		if dv, ok := v.(driver.Valuer); ok {
			rv := reflect.ValueOf(v)
			if rv.Kind() == reflect.Pointer && rv.IsNil() {
				return nil
			}
			val, err := dv.Value()
			if err != nil {
				return nil
			}
			if _, same := val.(driver.Valuer); same {
				return val
			}
			v = val
			continue
		}
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Pointer, reflect.Interface:
			if rv.IsNil() {
				return nil
			}
			v = rv.Elem().Interface()
			continue
		case reflect.String:
			return rv.String()
		case reflect.Bool:
			return rv.Bool()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return float64(rv.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return float64(rv.Uint())
		case reflect.Float32, reflect.Float64:
			return rv.Float()
		case reflect.Struct:
			if rv.Type().ConvertibleTo(timeType) {
				return rv.Convert(timeType).Interface()
			}
		case reflect.Slice:
			if rv.Type().Elem().Kind() == reflect.Uint8 { // []byte → string
				return string(rv.Bytes())
			}
		}
		return v
	}
	return v
}
