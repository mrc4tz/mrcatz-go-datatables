package datatable

import (
	"regexp"
	"sort"
	"strings"
)

// SplitKeywords memecah kata kunci pencarian menjadi kata-kata unik (tidak
// peka huruf besar/kecil; spasi ganda diabaikan). Ejaan kemunculan pertama
// dipertahankan.
func SplitKeywords(search string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, word := range strings.FieldsFunc(search, isJSSpace) {
		lower := strings.ToLower(word)
		if !seen[lower] {
			seen[lower] = true
			out = append(out, word)
		}
	}
	return out
}

// Trigrams mengembalikan trigram karakter unik (huruf kecil) untuk toleransi
// salah ketik. Kata yang lebih pendek dari 3 karakter menghasilkan slice
// kosong.
func Trigrams(word string) []string { return NGrams(word, 3) }

// NGrams mengembalikan n-gram karakter unik (per rune, huruf kecil) sesuai
// urutan kemunculan.
func NGrams(word string, size int) []string {
	chars := []rune(strings.ToLower(word))
	out := []string{}
	if size < 1 || len(chars) < size {
		return out
	}
	seen := map[string]bool{}
	for i := 0; i <= len(chars)-size; i++ {
		g := string(chars[i : i+size])
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	return out
}

// Segment adalah potongan teks hasil Highlight.
type Segment struct {
	Text  string `json:"text"`
	Match bool   `json:"match"`
}

// Highlight menandai bagian teks yang cocok dengan kata kunci — tanpa HTML,
// jadi aman dari XSS; komponen yang merender segmen menjadi `<mark>`.
//
// Pencocokan tidak peka huruf besar/kecil, tapi teks asli dipertahankan:
// mencari "cob" pada "Coba" menghasilkan [{Cob, match}, {a}]. Kecocokan yang
// berdempetan digabung (tidak ada highlight ganda).
func Highlight(text, search string) []Segment {
	return HighlightWords(text, SplitKeywords(search))
}

// HighlightWords sama dengan Highlight tetapi menerima daftar kata langsung.
func HighlightWords(text string, words []string) []Segment {
	list := make([]string, 0, len(words))
	for _, w := range words {
		if w != "" {
			list = append(list, w)
		}
	}
	if text == "" {
		return []Segment{}
	}
	if len(list) == 0 {
		return []Segment{{Text: text}}
	}

	// Kata terpanjang dulu agar "budi santoso" menang atas "budi".
	sort.SliceStable(list, func(i, j int) bool { return utf16Len(list[i]) > utf16Len(list[j]) })
	quoted := make([]string, len(list))
	for i, w := range list {
		quoted[i] = regexp.QuoteMeta(w)
	}
	re := regexp.MustCompile("(?i)" + strings.Join(quoted, "|"))

	segments := []Segment{}
	last := 0
	for _, m := range re.FindAllStringIndex(text, -1) {
		start, end := m[0], m[1]
		if end == start {
			continue
		}
		if start > last {
			segments = append(segments, Segment{Text: text[last:start]})
		}
		if n := len(segments); n > 0 && segments[n-1].Match && start == last {
			segments[n-1].Text += text[start:end]
		} else {
			segments = append(segments, Segment{Text: text[start:end], Match: true})
		}
		last = end
	}
	if last < len(text) {
		segments = append(segments, Segment{Text: text[last:]})
	}
	return segments
}

// utf16Len meniru `String.prototype.length` (jumlah unit UTF-16).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}
