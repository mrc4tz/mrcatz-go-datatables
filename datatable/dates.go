package datatable

import (
	"regexp"
	"strconv"
	"time"
)

// Konversi nilai filter tanggal menjadi rentang waktu setengah-terbuka
// [start, end) agar bisa dipakai oleh database mana pun tanpa fungsi SQL
// khusus (pengganti whereDate/whereMonth/whereYear di Laravel).
//
//	date     → 2026-10-19           → satu hari penuh
//	month    → 2026-10              → satu bulan penuh
//	year     → 2026                 → satu tahun penuh
//	datetime → 2026-10-19T08:30     → satu menit penuh
//	time     → 08:30:15             → satu detik (atau satu menit bila tanpa detik)
//	timeHm   → 08:30                → satu menit penuh
//
// Format jam (time / timeHm) tidak terikat tanggal: periodenya berada di
// 1970-01-01 UTC, sehingga bisa langsung dibandingkan dengan kolom jam.

// UTCOffset adalah offset zona waktu: "+08:00" (WITA), "+07:00" (WIB), "Z",
// atau "" = zona waktu lokal proses (time.Local).
type UTCOffset string

// Period adalah rentang waktu setengah-terbuka [Start, End).
type Period struct {
	Start time.Time
	End   time.Time
}

var offsetRE = regexp.MustCompile(`^([+-])(\d{2}):?(\d{2})$`)

// Location mengubah offset menjadi *time.Location. "" → time.Local.
// Offset tidak valid → *QueryError (membungkus ErrInvalidDate).
func (o UTCOffset) Location() (*time.Location, error) {
	switch o {
	case "":
		return time.Local, nil
	case "Z", "+00:00":
		return time.UTC, nil
	}
	m := offsetRE.FindStringSubmatch(string(o))
	if m == nil {
		return nil, invalidDate("Offset zona waktu tidak valid: %q", string(o))
	}
	h, _ := strconv.Atoi(m[2])
	mi, _ := strconv.Atoi(m[3])
	secs := (h*60 + mi) * 60
	if m[1] == "-" {
		secs = -secs
	}
	return time.FixedZone(string(o), secs), nil
}

// IsTimeFormat bernilai true untuk format jam dalam sehari (time / timeHm).
func IsTimeFormat(f DateFormat) bool { return f == FormatTime || f == FormatTimeHm }

var (
	timeRE       = regexp.MustCompile(`^(\d{2}):(\d{2})(?::(\d{2}))?$`)
	withSecondRE = regexp.MustCompile(`:\d{2}:\d{2}$`)
	yearRE       = regexp.MustCompile(`^(\d{4})$`)
	monthRE      = regexp.MustCompile(`^(\d{4})-(\d{2})$`)
	dateRE       = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)
	datetimeRE   = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})`)
)

// ParseTimeOfDay mengubah "HH:mm" / "HH:mm:ss" menjadi jumlah detik sejak
// tengah malam — berguna untuk Where kustom pada filter jam.
//
//	ParseTimeOfDay("14:30") // 52200
func ParseTimeOfDay(value string) (int, error) {
	m := timeRE.FindStringSubmatch(trimJS(value))
	if m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		se := 0
		if m[3] != "" {
			se, _ = strconv.Atoi(m[3])
		}
		if h <= 23 && mi <= 59 && se <= 59 {
			return h*3600 + mi*60 + se, nil
		}
	}
	return 0, invalidDate("Nilai %q bukan jam yang valid (HH:mm atau HH:mm:ss).", value)
}

// TimeOfDay mengembalikan jam dalam sehari dari t (di zona off), dalam
// bentuk waktu 1970-01-01 UTC agar bisa dibandingkan dengan periode format
// jam. Bagian sub-detik dipertahankan.
func TimeOfDay(t time.Time, off UTCOffset) (time.Time, error) {
	loc, err := off.Location()
	if err != nil {
		return time.Time{}, err
	}
	l := t.In(loc)
	secs := l.Hour()*3600 + l.Minute()*60 + l.Second()
	return time.Unix(int64(secs), int64(l.Nanosecond())).UTC(), nil
}

func epoch(seconds int) time.Time { return time.Unix(int64(seconds), 0).UTC() }

func timePeriod(value string, f DateFormat) (Period, error) {
	seconds, err := ParseTimeOfDay(value)
	if err != nil {
		return Period{}, err
	}
	// timeHm selalu satu menit; time tanpa detik juga dianggap satu menit.
	perMinute := f == FormatTimeHm || !withSecondRE.MatchString(trimJS(value))
	start, step := seconds, 1
	if perMinute {
		start, step = seconds-seconds%60, 60
	}
	return Period{Start: epoch(start), End: epoch(start + step)}, nil
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// PeriodOf mengubah nilai filter tanggal menjadi periode setengah-terbuka
// sesuai format, di zona waktu off. Nilai yang tidak sesuai format →
// *QueryError (membungkus ErrInvalidDate). Tanggal di luar kalender
// dinormalisasi seperti Date JS (2026-02-31 → 3 Maret).
func PeriodOf(value string, f DateFormat, off UTCOffset) (Period, error) {
	v := trimJS(value)
	if IsTimeFormat(f) {
		return timePeriod(v, f)
	}
	loc, err := off.Location()
	if err != nil {
		return Period{}, err
	}
	mk := func(y, mo, d, h, mi int) time.Time {
		return time.Date(y, time.Month(mo+1), d, h, mi, 0, 0, loc).UTC()
	}
	switch f {
	case FormatYear:
		if m := yearRE.FindStringSubmatch(v); m != nil {
			y := atoi(m[1])
			return Period{mk(y, 0, 1, 0, 0), mk(y+1, 0, 1, 0, 0)}, nil
		}
	case FormatMonth:
		if m := monthRE.FindStringSubmatch(v); m != nil {
			y, mo := atoi(m[1]), atoi(m[2])-1
			if mo >= 0 && mo < 12 {
				return Period{mk(y, mo, 1, 0, 0), mk(y, mo+1, 1, 0, 0)}, nil
			}
		}
	case FormatDate, "":
		if m := dateRE.FindStringSubmatch(v); m != nil {
			y, mo, d := atoi(m[1]), atoi(m[2])-1, atoi(m[3])
			return Period{mk(y, mo, d, 0, 0), mk(y, mo, d+1, 0, 0)}, nil
		}
	case FormatDatetime:
		if m := datetimeRE.FindStringSubmatch(v); m != nil {
			y, mo, d, h, mi := atoi(m[1]), atoi(m[2])-1, atoi(m[3]), atoi(m[4]), atoi(m[5])
			return Period{mk(y, mo, d, h, mi), mk(y, mo, d, h, mi+1)}, nil
		}
	}
	return Period{}, invalidDate("Nilai %q tidak sesuai format tanggal %q.", value, string(f))
}

// Bounds adalah batas perbandingan dalam bentuk netral: GTE (≥) dan/atau LT
// (<), atau Not = di luar periode. Engine menerjemahkannya ke kondisi SQL,
// memtable ke predikat (Contains).
type Bounds struct {
	GTE *time.Time
	LT  *time.Time
	Not *Period
}

// IsZero bernilai true bila tidak ada batas sama sekali.
func (b Bounds) IsZero() bool { return b.GTE == nil && b.LT == nil && b.Not == nil }

// Contains memeriksa apakah t memenuhi batas.
func (b Bounds) Contains(t time.Time) bool {
	if b.Not != nil {
		return t.Before(b.Not.Start) || !t.Before(b.Not.End)
	}
	if b.GTE != nil && t.Before(*b.GTE) {
		return false
	}
	if b.LT != nil && !t.Before(*b.LT) {
		return false
	}
	return true
}

func tp(t time.Time) *time.Time { return &t }

// CompareBounds menerjemahkan operator terhadap sebuah periode:
//
//	=  → [start, end)     != → di luar periode
//	>  → ≥ end            >= → ≥ start
//	<  → < start          <= → < end
//
// Operator lain diperlakukan sebagai "=".
func CompareBounds(p Period, op Operator) Bounds {
	switch op {
	case OpNe:
		pp := p
		return Bounds{Not: &pp}
	case OpGt:
		return Bounds{GTE: tp(p.End)}
	case OpGte:
		return Bounds{GTE: tp(p.Start)}
	case OpLt:
		return Bounds{LT: tp(p.Start)}
	case OpLte:
		return Bounds{LT: tp(p.End)}
	}
	return Bounds{GTE: tp(p.Start), LT: tp(p.End)}
}

// RangeBounds menghitung batas rentang: from → ≥ awal periode from,
// to → < akhir periode to (inklusif satu periode penuh). nil/"" = terbuka.
func RangeBounds(from, to *string, f DateFormat, off UTCOffset) (Bounds, error) {
	var b Bounds
	if from != nil && *from != "" {
		p, err := PeriodOf(*from, f, off)
		if err != nil {
			return Bounds{}, err
		}
		b.GTE = tp(p.Start)
	}
	if to != nil && *to != "" {
		p, err := PeriodOf(*to, f, off)
		if err != nil {
			return Bounds{}, err
		}
		b.LT = tp(p.End)
	}
	return b, nil
}

// FilterBounds menghitung batas untuk filter date/dateRange dari nilai yang
// sudah di-resolve (memakai format & operator efektif). Tipe filter lain
// menghasilkan Bounds kosong.
func FilterBounds(f Filter, v Resolved, fc FilterContext, off UTCOffset) (Bounds, error) {
	switch f.Type {
	case FilterDate:
		s, _ := v.Scalar.(string)
		p, err := PeriodOf(s, f.EffectiveFormat(), off)
		if err != nil {
			return Bounds{}, err
		}
		return CompareBounds(p, f.EffectiveOperator(fc)), nil
	case FilterDateRange:
		return RangeBounds(v.From, v.To, f.EffectiveFormat(), off)
	}
	return Bounds{}, nil
}
