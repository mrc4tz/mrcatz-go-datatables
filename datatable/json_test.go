package datatable

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFilterValueJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want FilterValue
	}{
		{"string", `"aktif"`, StringValue("aktif")},
		{"angka jadi string", `12`, StringValue("12")},
		{"bool jadi string", `true`, StringValue("true")},
		{"multi", `{"values":["1",2,true],"exclude":true}`, MultiValue{Values: []string{"1", "2", "true"}, Exclude: true}},
		{"multi tanpa exclude", `{"values":[]}`, MultiValue{Values: []string{}}},
		{"range", `{"from":"2026-01-01","to":null}`, RangeValue{From: Str("2026-01-01")}},
		{"range kosong", `{}`, RangeValue{}},
		{"null", `null`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UnmarshalFilterValue([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v want %#v", got, tt.want)
			}
		})
	}
	for _, bad := range []string{`[1]`, `{"values":"x"}`, `{"values":[{}]}`, `{"from":[1]}`, `{"values":[],"exclude":"ya"}`} {
		if _, err := UnmarshalFilterValue([]byte(bad)); err == nil {
			t.Errorf("UnmarshalFilterValue(%s) tidak gagal", bad)
		}
	}
}

func TestTableQueryJSONRoundTrip(t *testing.T) {
	q := TableQuery{
		Page: 2, Limit: 5, Search: "budi",
		Sort: []SortRule{{"nama", Desc}},
		Filters: map[string]FilterValue{
			"status": StringValue("aktif"),
			"kat":    NotIn("1"),
			"tgl":    RangeValue{From: Str("2026-01-01")},
		},
	}
	b, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"page":2,"limit":5,"search":"budi","sort":[{"key":"nama","dir":"desc"}],"filters":{"kat":{"values":["1"],"exclude":true},"status":"aktif","tgl":{"from":"2026-01-01"}}}`
	if string(b) != want {
		t.Fatalf("marshal:\n got %s\nwant %s", b, want)
	}
	var back TableQuery
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, q) {
		t.Fatalf("unmarshal: %#v", back)
	}

	empty, _ := json.Marshal(TableQuery{})
	if string(empty) != `{"page":0,"limit":0,"search":"","sort":[],"filters":{}}` {
		t.Fatalf("empty: %s", empty)
	}
	if err := json.Unmarshal([]byte(`{"filters":{"x":[1]}}`), &back); err == nil {
		t.Fatal("filter array harus ditolak")
	}
}

func TestPageJSON(t *testing.T) {
	b, _ := json.Marshal(Page[int]{Meta: NewMeta(0, 1, 10)})
	want := `{"data":[],"meta":{"total":0,"page":1,"limit":10,"totalPages":1,"hasMore":false}}`
	if string(b) != want {
		t.Fatalf("got %s", b)
	}
	b, _ = json.Marshal(MultiValue{})
	if string(b) != `{"values":[],"exclude":false}` {
		t.Fatalf("multi: %s", b)
	}
}

func TestSchemaJSON(t *testing.T) {
	five := 5
	s := Schema{Filters: []FilterSchema{
		{ID: "s", Label: "S", Type: FilterSelect, Visible: true},
		{ID: "m", Label: "M", Type: FilterMulti, Visible: false, DependsOn: []string{"s"}, Options: []Option{{1, "A"}}, SearchThreshold: &five},
		{ID: "n", Label: "N", Type: FilterMulti, Visible: true, AllowExclude: true},
		{ID: "d", Label: "D", Type: FilterDate, Visible: true, Min: "2026-01-01"},
		{ID: "r", Label: "R", Type: FilterDateRange, Visible: true, Format: FormatMonth, Max: "2026-12"},
	}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"filters":[` +
		`{"id":"s","label":"S","type":"select","visible":true,"options":[]},` +
		`{"id":"m","label":"M","type":"multi","visible":false,"dependsOn":["s"],"options":[{"value":1,"label":"A"}],"allowExclude":false,"searchThreshold":5},` +
		`{"id":"n","label":"N","type":"multi","visible":true,"options":[],"allowExclude":true,"searchThreshold":null},` +
		`{"id":"d","label":"D","type":"date","visible":true,"format":"date","min":"2026-01-01"},` +
		`{"id":"r","label":"R","type":"dateRange","visible":true,"format":"month","max":"2026-12"}]}`
	if string(b) != want {
		t.Fatalf("\n got %s\nwant %s", b, want)
	}
	var back Schema
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Filters[1].SearchThreshold == nil || *back.Filters[1].SearchThreshold != 5 || back.Filters[2].SearchThreshold != nil {
		t.Fatalf("unmarshal searchThreshold: %+v", back.Filters)
	}
	empty, _ := json.Marshal(Schema{})
	if string(empty) != `{"filters":[]}` {
		t.Fatalf("empty schema: %s", empty)
	}
}
