package datatable

import (
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Serialisasi TableQuery ⇄ query string. Skema kunci (semua opsional):
//
//	search=budi
//	page=2&limit=20
//	sort=nama:asc,createdAt:desc        (multi-sort, urutan = prioritas)
//	f.status=aktif                      (select / date)
//	f.kategori=1&f.kategori=3           (multi)  +  f.kategori.not=1 (mode Kecuali)
//	f.dibuat.from=2026-01-01&f.dibuat.to=2026-01-31   (dateRange)
//
// Prefix memberi namespace bila beberapa tabel berada di satu halaman
// (setara `urlPrefix()` di versi Laravel), mis. `pengguna_` → `pengguna_search`.

const filterPrefix = "f."

// Nilai bawaan parsing.
const (
	DefaultLimit = 10
	MaxLimit     = 100
)

// maxPage membatasi page agar (page-1)*limit tidak overflow.
const maxPage = math.MaxInt32

// ParseOptions mengatur ParseQuery.
type ParseOptions struct {
	Prefix string
	// DefaultLimit dipakai bila `limit` kosong/tidak valid. 0 = 10.
	DefaultLimit int
	// MaxLimit adalah batas atas `limit` agar client tidak bisa meminta
	// jutaan baris. 0 = 100.
	MaxLimit    int
	DefaultSort []SortRule
	// FilterTypes: tipe tiap filter — membuat parsing presisi (mis. multi
	// dengan satu nilai). Lihat FilterTypes().
	FilterTypes map[string]FilterType
}

// ParseSort mem-parse `nama:asc,createdAt:desc`. Arah selain `desc` dianggap
// `asc`; bagian kosong dan kunci kosong dibuang. Tidak pernah gagal.
func ParseSort(raw string) []SortRule {
	out := []SortRule{}
	for _, part := range strings.Split(raw, ",") {
		part = trimJS(part)
		if part == "" {
			continue
		}
		pieces := strings.Split(part, ":")
		dir := Asc
		if len(pieces) > 1 && pieces[1] == "desc" {
			dir = Desc
		}
		if pieces[0] == "" {
			continue
		}
		out = append(out, SortRule{Key: pieces[0], Dir: dir})
	}
	return out
}

// FormatSort kebalikan ParseSort.
func FormatSort(rules []SortRule) string {
	parts := make([]string, len(rules))
	for i, r := range rules {
		parts[i] = r.Key + ":" + string(r.Dir)
	}
	return strings.Join(parts, ",")
}

// parseIntJS meniru `parseInt(raw, 10)`: whitespace di depan diabaikan,
// tanda opsional, lalu digit sebanyak mungkin (sisanya diabaikan).
func parseIntJS(raw string) (int, bool) {
	s := strings.TrimLeftFunc(raw, isJSSpace)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(s[:end], 10, 64)
	if err != nil { // terlalu besar → jenuh
		n = math.MaxInt64
	}
	if neg {
		n = -n
	}
	if n > math.MaxInt32 {
		n = math.MaxInt32
	}
	return int(n), true
}

func toPositiveInt(raw string, has bool, fallback int) int {
	if !has {
		return fallback
	}
	if n, ok := parseIntJS(raw); ok && n > 0 {
		return n
	}
	return fallback
}

// splitFilterKey memecah `kategori.not` → ("kategori", "not").
func splitFilterKey(rest string) (id, part string) {
	i := strings.LastIndexByte(rest, '.')
	if i > 0 {
		switch p := rest[i+1:]; p {
		case "from", "to", "not":
			return rest[:i], p
		}
	}
	return rest, ""
}

// ParseQuery mem-parse query string menjadi TableQuery. Kunci asing
// diabaikan; page < 1 → 1; limit kosong → DefaultLimit; limit > MaxLimit
// dipangkas. Kunci filter diproses berurutan secara leksikografis sehingga
// `f.x` selalu diproses sebelum `f.x.not` / `f.x.from` / `f.x.to`.
func ParseQuery(v url.Values, opts ParseOptions) TableQuery {
	maxLimit := opts.MaxLimit
	if maxLimit <= 0 {
		maxLimit = MaxLimit
	}
	defLimit := opts.DefaultLimit
	if defLimit <= 0 {
		defLimit = DefaultLimit
	}

	single := map[string]string{}
	multi := map[string][]string{}
	for fullKey, values := range v {
		if !strings.HasPrefix(fullKey, opts.Prefix) || len(values) == 0 {
			continue
		}
		key := fullKey[len(opts.Prefix):]
		single[key] = values[len(values)-1]
		multi[key] = append(multi[key], values...)
	}

	keys := make([]string, 0, len(multi))
	for k := range multi {
		if strings.HasPrefix(k, filterPrefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	filters := map[string]FilterValue{}
	for _, key := range keys {
		values := multi[key]
		id, part := splitFilterKey(key[len(filterPrefix):])
		typ, known := opts.FilterTypes[id]

		switch {
		case part == "from" || part == "to":
			cur, _ := filters[id].(RangeValue)
			val := values[0]
			if part == "from" {
				cur.From = &val
			} else {
				cur.To = &val
			}
			filters[id] = cur
		case part == "not":
			list := []string{}
			if cur, ok := filters[id].(MultiValue); ok {
				list = cur.Values
			}
			filters[id] = MultiValue{Values: list, Exclude: values[0] == "1" || values[0] == "true"}
		case typ == FilterMulti || (!known && len(values) > 1):
			exclude := false
			if cur, ok := filters[id].(MultiValue); ok {
				exclude = cur.Exclude
			}
			list := []string{}
			for _, x := range values {
				if x != "" {
					list = append(list, x)
				}
			}
			filters[id] = MultiValue{Values: list, Exclude: exclude}
		default:
			filters[id] = StringValue(values[0])
		}
	}

	page, hasPage := single["page"]
	limitRaw, hasLimit := single["limit"]
	sortRules := ParseSort(single["sort"])
	if len(sortRules) == 0 {
		sortRules = opts.DefaultSort
		if sortRules == nil {
			sortRules = []SortRule{}
		}
	}
	limit := toPositiveInt(limitRaw, hasLimit, defLimit)
	if limit > maxLimit {
		limit = maxLimit
	}
	return TableQuery{
		Search:  trimJS(single["search"]),
		Page:    min(toPositiveInt(page, hasPage, 1), maxPage),
		Limit:   limit,
		Sort:    sortRules,
		Filters: filters,
	}
}

// FormatQuery kebalikan ParseQuery. Page 1 dan nilai filter kosong tidak
// ditulis agar URL tetap ringkas; limit ditulis bila > 0.
func FormatQuery(q TableQuery, prefix string) url.Values {
	out := url.Values{}
	if q.Search != "" {
		out.Set(prefix+"search", q.Search)
	}
	if q.Page != 0 && q.Page != 1 {
		out.Set(prefix+"page", strconv.Itoa(q.Page))
	}
	if q.Limit != 0 {
		out.Set(prefix+"limit", strconv.Itoa(q.Limit))
	}
	if len(q.Sort) > 0 {
		out.Set(prefix+"sort", FormatSort(q.Sort))
	}
	for id, value := range q.Filters {
		if IsEmpty(value) {
			continue
		}
		k := prefix + filterPrefix + id
		switch x := value.(type) {
		case StringValue:
			out.Set(k, string(x))
		case MultiValue:
			for _, item := range x.Values {
				out.Add(k, item)
			}
			if x.Exclude {
				out.Set(k+".not", "1")
			}
		case RangeValue:
			if x.From != nil && *x.From != "" {
				out.Set(k+".from", *x.From)
			}
			if x.To != nil && *x.To != "" {
				out.Set(k+".to", *x.To)
			}
		}
	}
	return out
}

// IsTableParam: kunci query milik tabel ini — dipakai untuk membersihkan URL
// tanpa menyentuh param lain.
func IsTableParam(key, prefix string) bool {
	if !strings.HasPrefix(key, prefix) {
		return false
	}
	switch k := key[len(prefix):]; k {
	case "search", "page", "limit", "sort":
		return true
	default:
		return strings.HasPrefix(k, filterPrefix)
	}
}

// IsEmpty bernilai true bila nilai filter tidak berisi apa-apa ("0" tetap
// dianggap terisi).
func IsEmpty(v FilterValue) bool {
	switch x := v.(type) {
	case nil:
		return true
	case StringValue:
		return x == ""
	case MultiValue:
		return len(x.Values) == 0
	case RangeValue:
		return (x.From == nil || *x.From == "") && (x.To == nil || *x.To == "")
	}
	return true
}
