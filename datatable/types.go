package datatable

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// SortDir adalah arah urutan: "asc" atau "desc".
type SortDir string

const (
	Asc  SortDir = "asc"
	Desc SortDir = "desc"
)

// SortRule adalah satu aturan urutan; urutan slice = prioritas.
type SortRule struct {
	Key string  `json:"key"`
	Dir SortDir `json:"dir"`
}

// FilterValue adalah nilai filter mentah: StringValue, RangeValue, atau
// MultiValue (sama dengan union `string | RangeValue | MultiValue` di JS).
type FilterValue interface{ isFilterValue() }

// StringValue adalah nilai filter `select` / `date`.
type StringValue string

// RangeValue adalah nilai filter rentang tanggal; salah satu sisi boleh
// kosong (rentang terbuka). JSON: {"from": .., "to": ..}.
type RangeValue struct {
	From *string `json:"from,omitempty"`
	To   *string `json:"to,omitempty"`
}

// MultiValue adalah nilai filter multi-pilihan beserta mode include/exclude.
// JSON: {"values": [..], "exclude": bool}.
type MultiValue struct {
	Values  []string `json:"values"`
	Exclude bool     `json:"exclude"`
}

func (StringValue) isFilterValue() {}
func (RangeValue) isFilterValue()  {}
func (MultiValue) isFilterValue()  {}

// Str adalah pembantu untuk mengisi RangeValue: Range(Str("2026-01-01"), nil).
func Str(s string) *string { return &s }

// Range membuat RangeValue dari dua string; string kosong = sisi terbuka.
func Range(from, to string) RangeValue {
	var r RangeValue
	if from != "" {
		r.From = &from
	}
	if to != "" {
		r.To = &to
	}
	return r
}

// In membuat MultiValue mode Termasuk (include).
func In(values ...string) MultiValue {
	if values == nil {
		values = []string{}
	}
	return MultiValue{Values: values}
}

// NotIn membuat MultiValue mode Kecuali (exclude).
func NotIn(values ...string) MultiValue {
	m := In(values...)
	m.Exclude = true
	return m
}

// MarshalJSON menulis Values kosong sebagai [] (bukan null), seperti JS.
func (m MultiValue) MarshalJSON() ([]byte, error) {
	type plain MultiValue
	if m.Values == nil {
		m.Values = []string{}
	}
	return json.Marshal(plain(m))
}

// UnmarshalFilterValue membaca satu nilai filter dari JSON dengan bentuk yang
// sama seperti versi JS: string → StringValue, objek dengan "values" →
// MultiValue, objek lain → RangeValue. Angka/boolean diubah ke StringValue;
// null menghasilkan (nil, nil).
func UnmarshalFilterValue(data []byte) (FilterValue, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil, nil
	}
	switch data[0] {
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, err
		}
		return StringValue(s), nil
	case '{':
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(data, &probe); err != nil {
			return nil, err
		}
		if raw, ok := probe["values"]; ok {
			mv := MultiValue{Values: []string{}}
			var list []json.RawMessage
			if err := json.Unmarshal(raw, &list); err != nil {
				return nil, fmt.Errorf("datatable: \"values\" harus berupa array: %w", err)
			}
			for _, item := range list {
				s, err := scalarString(item)
				if err != nil {
					return nil, err
				}
				mv.Values = append(mv.Values, s)
			}
			if ex, ok := probe["exclude"]; ok {
				var b bool
				if err := json.Unmarshal(ex, &b); err == nil {
					mv.Exclude = b
				} else if !bytes.Equal(bytes.TrimSpace(ex), []byte("null")) {
					return nil, fmt.Errorf("datatable: \"exclude\" harus boolean: %w", err)
				}
			}
			return mv, nil
		}
		var rv RangeValue
		for key, dst := range map[string]**string{"from": &rv.From, "to": &rv.To} {
			raw, ok := probe[key]
			if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				continue
			}
			s, err := scalarString(raw)
			if err != nil {
				return nil, err
			}
			*dst = &s
		}
		return rv, nil
	case '[':
		return nil, fmt.Errorf("datatable: nilai filter tidak boleh berupa array: %s", data)
	default:
		s, err := scalarString(data)
		if err != nil {
			return nil, err
		}
		return StringValue(s), nil
	}
}

// scalarString mengubah string/angka/boolean JSON menjadi string.
func scalarString(raw json.RawMessage) (string, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	case bool:
		return strconv.FormatBool(x), nil
	case nil:
		return "", nil
	}
	return "", fmt.Errorf("datatable: nilai filter harus skalar: %s", raw)
}

// UnmarshalFilters membaca objek JSON `{id: FilterValue}`; entri null dilewati.
func UnmarshalFilters(data []byte) (map[string]FilterValue, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]FilterValue, len(raw))
	for id, item := range raw {
		v, err := UnmarshalFilterValue(item)
		if err != nil {
			return nil, fmt.Errorf("datatable: filter %q: %w", id, err)
		}
		if v != nil {
			out[id] = v
		}
	}
	return out, nil
}

// TableQuery adalah state query tabel — satu sumber kebenaran untuk URL, API,
// dan engine.
type TableQuery struct {
	Page    int                    `json:"page"`
	Limit   int                    `json:"limit"`
	Search  string                 `json:"search"`
	Sort    []SortRule             `json:"sort"`
	Filters map[string]FilterValue `json:"filters"`
}

// MarshalJSON menulis sort/filters kosong sebagai []/{} seperti versi JS.
func (q TableQuery) MarshalJSON() ([]byte, error) {
	type plain TableQuery
	if q.Sort == nil {
		q.Sort = []SortRule{}
	}
	if q.Filters == nil {
		q.Filters = map[string]FilterValue{}
	}
	return json.Marshal(plain(q))
}

// UnmarshalJSON membaca TableQuery termasuk nilai filter polimorfik.
func (q *TableQuery) UnmarshalJSON(data []byte) error {
	var raw struct {
		Page    int             `json:"page"`
		Limit   int             `json:"limit"`
		Search  string          `json:"search"`
		Sort    []SortRule      `json:"sort"`
		Filters json.RawMessage `json:"filters"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*q = TableQuery{Page: raw.Page, Limit: raw.Limit, Search: raw.Search, Sort: raw.Sort, Filters: map[string]FilterValue{}}
	if len(raw.Filters) > 0 && !bytes.Equal(bytes.TrimSpace(raw.Filters), []byte("null")) {
		f, err := UnmarshalFilters(raw.Filters)
		if err != nil {
			return err
		}
		q.Filters = f
	}
	return nil
}

// PageMeta adalah meta pagination.
type PageMeta struct {
	Total      int64 `json:"total"`
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	TotalPages int   `json:"totalPages"`
	// HasMore: mode load-more — masih ada baris setelah halaman ini.
	HasMore bool `json:"hasMore"`
}

// NewMeta menghitung meta pagination: totalPages minimal 1, hasMore bila
// page*limit < total.
func NewMeta(total int64, page, limit int) PageMeta {
	if limit < 1 {
		limit = 1
	}
	pages := (total + int64(limit) - 1) / int64(limit)
	if pages < 1 {
		pages = 1
	}
	return PageMeta{
		Total:      total,
		Page:       page,
		Limit:      limit,
		TotalPages: int(pages),
		HasMore:    int64(page)*int64(limit) < total,
	}
}

// Page adalah satu halaman hasil query.
type Page[T any] struct {
	Data []T      `json:"data"`
	Meta PageMeta `json:"meta"`
}

// MarshalJSON menulis data kosong sebagai [] (bukan null).
func (p Page[T]) MarshalJSON() ([]byte, error) {
	data := p.Data
	if data == nil {
		data = []T{}
	}
	return json.Marshal(struct {
		Data []T      `json:"data"`
		Meta PageMeta `json:"meta"`
	}{data, p.Meta})
}
