package memtable

import (
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// fieldIndex memetakan nama json → index field (termasuk field dari struct
// embedded yang dipromosikan, seperti encoding/json).
type fieldIndex map[string][]int

var indexCache sync.Map // reflect.Type → fieldIndex

func jsonName(f reflect.StructField) (name string, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", true
	}
	name, _, _ = strings.Cut(tag, ",")
	return name, false
}

func buildIndex(t reflect.Type) fieldIndex {
	if cached, ok := indexCache.Load(t); ok {
		return cached.(fieldIndex)
	}
	idx := fieldIndex{}
	byGoName := map[string][]int{}
	var walk func(t reflect.Type, prefix []int, depth int)
	walk = func(t reflect.Type, prefix []int, depth int) {
		if depth > 8 {
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, skip := jsonName(f)
			if skip {
				continue
			}
			path := append(append([]int{}, prefix...), i)
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
				walk(ft, path, depth+1)
				continue
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			// Field yang lebih dangkal menang (aturan dominasi encoding/json).
			if _, exists := idx[name]; !exists {
				idx[name] = path
			}
			if _, exists := byGoName[f.Name]; !exists {
				byGoName[f.Name] = path
			}
		}
	}
	walk(t, nil, 0)
	// Cadangan: nama field Go juga bisa dipakai bila tidak bentrok.
	for name, path := range byGoName {
		if _, exists := idx[name]; !exists {
			idx[name] = path
		}
	}
	indexCache.Store(t, idx)
	return idx
}

func indirect(v reflect.Value) (reflect.Value, bool) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}, false
		}
		v = v.Elem()
	}
	return v, v.IsValid()
}

// fieldByPath mengikuti index field; berhenti (false) pada pointer embedded nil.
func fieldByPath(v reflect.Value, path []int) (reflect.Value, bool) {
	for i, n := range path {
		if i > 0 {
			var ok bool
			if v, ok = indirect(v); !ok {
				return reflect.Value{}, false
			}
		}
		v = v.Field(n)
	}
	return v, true
}

// Lookup mengambil nilai bersarang dengan dot-path berdasarkan nama json
// (fallback nama field Go): Lookup(row, "kategori.nama"). Mendukung struct
// (termasuk embedded), map berkunci string, dan index slice/array
// ("items.0"). Pointer diikuti; path yang tidak ada atau melewati nil → nil.
func Lookup(row any, path string) any {
	cur := reflect.ValueOf(row)
	for _, part := range strings.Split(path, ".") {
		v, ok := indirect(cur)
		if !ok {
			return nil
		}
		switch v.Kind() {
		case reflect.Struct:
			p, found := buildIndex(v.Type())[part]
			if !found {
				return nil
			}
			if cur, ok = fieldByPath(v, p); !ok {
				return nil
			}
		case reflect.Map:
			if v.Type().Key().Kind() != reflect.String {
				return nil
			}
			cur = v.MapIndex(reflect.ValueOf(part).Convert(v.Type().Key()))
			if !cur.IsValid() {
				return nil
			}
		case reflect.Slice, reflect.Array:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= v.Len() {
				return nil
			}
			cur = v.Index(i)
		default:
			return nil
		}
	}
	v, ok := indirect(cur)
	if !ok || !v.CanInterface() {
		return nil
	}
	return v.Interface()
}
