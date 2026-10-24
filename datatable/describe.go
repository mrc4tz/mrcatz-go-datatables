package datatable

import (
	"bytes"
	"encoding/json"
)

// FilterSchema adalah bentuk filter yang sudah diserialisasi (tanpa fungsi)
// — dikirim server ke client lewat endpoint schema. JSON-nya identik dengan
// `FilterSchema` versi JS (lihat MarshalJSON).
type FilterSchema struct {
	ID    string     `json:"id"`
	Label string     `json:"label"`
	Type  FilterType `json:"type"`
	// Visible: hasil predikat visibilitas untuk nilai filter saat ini.
	Visible bool `json:"visible"`
	// DependsOn: id filter lain yang memengaruhi filter ini.
	DependsOn []string `json:"dependsOn,omitempty"`
	// Options: select/multi (selalu ada, [] bila tersembunyi).
	Options []Option `json:"options,omitempty"`
	// AllowExclude: hanya multi.
	AllowExclude bool `json:"allowExclude,omitempty"`
	// SearchThreshold: hanya multi; nil ditulis sebagai null.
	SearchThreshold *int `json:"searchThreshold,omitempty"`
	// Format/Min/Max: hanya date/dateRange.
	Format DateFormat `json:"format,omitempty"`
	Min    string     `json:"min,omitempty"`
	Max    string     `json:"max,omitempty"`
}

// MarshalJSON menulis kunci sesuai tipe filter, dengan urutan yang sama
// seperti `JSON.stringify` di versi JS: select/multi selalu punya `options`;
// multi selalu punya `allowExclude` dan `searchThreshold` (boleh null);
// date/dateRange selalu punya `format`, `min`/`max` hanya bila ada.
func (s FilterSchema) MarshalJSON() ([]byte, error) {
	type kv struct {
		key string
		val any
	}
	fields := []kv{{"id", s.ID}, {"label", s.Label}, {"type", s.Type}, {"visible", s.Visible}}
	if s.DependsOn != nil {
		fields = append(fields, kv{"dependsOn", s.DependsOn})
	}
	switch s.Type {
	case FilterSelect, FilterMulti:
		opts := s.Options
		if opts == nil {
			opts = []Option{}
		}
		fields = append(fields, kv{"options", opts})
		if s.Type == FilterMulti {
			fields = append(fields, kv{"allowExclude", s.AllowExclude}, kv{"searchThreshold", s.SearchThreshold})
		}
	default:
		format := s.Format
		if format == "" {
			format = FormatDate
		}
		fields = append(fields, kv{"format", format})
		if s.Min != "" {
			fields = append(fields, kv{"min", s.Min})
		}
		if s.Max != "" {
			fields = append(fields, kv{"max", s.Max})
		}
	}

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range fields {
		b, err := json.Marshal(f.val)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(`"` + f.key + `":`)
		buf.Write(b)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Schema adalah respons endpoint schema: {"filters": [...]}.
type Schema struct {
	Filters []FilterSchema `json:"filters"`
}

// MarshalJSON menulis filters kosong sebagai [].
func (s Schema) MarshalJSON() ([]byte, error) {
	f := s.Filters
	if f == nil {
		f = []FilterSchema{}
	}
	return json.Marshal(struct {
		Filters []FilterSchema `json:"filters"`
	}{f})
}

// ResolveOptions mengembalikan opsi filter: hasil OptionsFunc bila ada,
// selain itu opsi statis.
func ResolveOptions(f Filter, fc FilterContext) ([]Option, error) {
	if f.OptionsFunc != nil {
		opts, err := f.OptionsFunc(fc)
		if err != nil {
			return nil, err
		}
		if opts == nil {
			opts = []Option{}
		}
		return opts, nil
	}
	if f.Options == nil {
		return []Option{}, nil
	}
	return f.Options, nil
}

// DescribeFilters menghitung schema filter (opsi + visibilitas + batas
// dinamis) untuk nilai filter saat ini. Filter tersembunyi tidak memuat
// opsi (hemat query). Loader dipanggil berurutan; error pertama dikembalikan.
func DescribeFilters(fc FilterContext, filters []Filter) (Schema, error) {
	out := Schema{Filters: make([]FilterSchema, 0, len(filters))}
	for _, f := range filters {
		visible := true
		if f.Visible != nil {
			visible = f.Visible(fc)
		}
		s := FilterSchema{ID: f.ID, Label: f.Label, Type: f.Type, Visible: visible, DependsOn: f.DependsOn}
		switch f.Type {
		case FilterSelect, FilterMulti:
			s.Options = []Option{}
			if visible {
				opts, err := ResolveOptions(f, fc)
				if err != nil {
					return Schema{}, err
				}
				s.Options = opts
			}
			if f.Type == FilterMulti {
				s.AllowExclude = f.AllowExclude
				if f.SearchThreshold != nil {
					n := *f.SearchThreshold
					s.SearchThreshold = &n
				}
			}
		default:
			s.Format = f.EffectiveFormat()
			s.Min, s.Max = f.Min, f.Max
			if f.MinFunc != nil {
				s.Min = f.MinFunc(fc)
			}
			if f.MaxFunc != nil {
				s.Max = f.MaxFunc(fc)
			}
		}
		out.Filters = append(out.Filters, s)
	}
	return out, nil
}
