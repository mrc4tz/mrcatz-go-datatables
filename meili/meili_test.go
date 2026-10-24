package meili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
	"github.com/mrc4tz/mrcatz-go-datatables/gormtable"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Item struct {
	ID         uint      `json:"id"`
	Nama       string    `json:"nama"`
	Status     string    `json:"status"`
	Stok       int       `json:"stok"`
	KategoriID uint      `json:"kategoriId"`
	CreatedAt  time.Time `json:"createdAt"`
	ShippedAt  time.Time `json:"shippedAt"`
	PaidAt     time.Time `json:"paidAt"`
}

var ctx = context.Background()

// --- client palsu ---

type searchCall struct {
	query  string
	params SearchParams
}

type fakeIndex struct {
	mu       sync.Mutex
	hits     []map[string]any
	err      error
	calls    []searchCall
	settings []IndexSettings
}

func (f *fakeIndex) Search(_ context.Context, q string, p SearchParams) (*SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, searchCall{q, p})
	if f.err != nil {
		return nil, f.err
	}
	return &SearchResult{Hits: f.hits}, nil
}

func (f *fakeIndex) UpdateSettings(_ context.Context, s IndexSettings) error {
	f.settings = append(f.settings, s)
	return nil
}

type fakeClient struct {
	idx  *fakeIndex
	uids []string
}

func (c *fakeClient) Index(uid string) Index { c.uids = append(c.uids, uid); return c.idx }

func fakeMeili(hits ...map[string]any) *fakeClient {
	return &fakeClient{idx: &fakeIndex{hits: hits}}
}

// searchOnly: index tanpa UpdateSettings.
type searchOnly struct{}

func (searchOnly) Search(context.Context, string, SearchParams) (*SearchResult, error) {
	return &SearchResult{}, nil
}

type searchOnlyClient struct{}

func (searchOnlyClient) Index(string) Index { return searchOnly{} }

// --- database ---

func openDB(t *testing.T) (*gorm.DB, *[]string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := db.DB()
	s.SetMaxOpenConns(1)
	t.Cleanup(func() { s.Close() })
	if err := db.AutoMigrate(&Item{}); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	rows := []Item{
		{ID: 1, Nama: `Ko"pi satu`, Status: "aktif", Stok: 5, KategoriID: 3, CreatedAt: ts, ShippedAt: ts, PaidAt: ts},
		{ID: 2, Nama: `Ko"pi dua`, Status: "aktif", Stok: 9, KategoriID: 3, CreatedAt: ts, ShippedAt: ts, PaidAt: ts},
		{ID: 3, Nama: "Teh", Status: "aktif", Stok: 0, KategoriID: 1, CreatedAt: ts, ShippedAt: ts, PaidAt: ts},
		{ID: 4, Nama: "Roti", Status: "arsip", Stok: 1, KategoriID: 2, CreatedAt: ts, ShippedAt: ts, PaidAt: ts},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	var sqls []string
	var mu sync.Mutex
	_ = db.Callback().Query().After("gorm:query").Register("test:record", func(d *gorm.DB) {
		mu.Lock()
		sqls = append(sqls, d.Dialector.Explain(d.Statement.SQL.String(), d.Statement.Vars...))
		mu.Unlock()
	})
	return db, &sqls
}

func itemIDs(rows []Item) []uint {
	out := make([]uint, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func q(pairs ...string) map[string][]string {
	v := map[string][]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		v[pairs[i]] = append(v[pairs[i]], pairs[i+1])
	}
	return v
}

func filters() []dt.Filter {
	return []dt.Filter{
		dt.Select("status", "Status", dt.Field("status"), dt.Options(dt.Opt("aktif", "Aktif"))),
		dt.Select("stok", "Stok", dt.Field("stok"), dt.Op(dt.OpGte), dt.Options(dt.Opt(1, "1"))),
		dt.Select("nama", "Nama", dt.Field("nama"), dt.Op(dt.OpContains), dt.Options()),
		dt.Multi("kat", "Kategori", dt.Field("kategoriId"), dt.Options(dt.Opt(1, "A")), dt.AllowExclude()),
		dt.Date("dibuat", "Dibuat", dt.Field("createdAt")),
		dt.DateRange("kirim", "Dikirim", dt.Field("shippedAt"), dt.Format(dt.FormatMonth)),
		dt.Date("tanpaMap", "Tanpa map", dt.Field("paidAt"), dt.Op(dt.OpNe)),
		dt.Select("lampiran", "Lampiran", dt.Options(dt.Opt(true, "Ya")),
			gormtable.Where(func(db *gorm.DB, v dt.Resolved, _ dt.FilterContext) *gorm.DB {
				if v.Scalar == true {
					return db.Where("stok > 0")
				}
				return db
			})),
	}
}

func TestAutoPushdown(t *testing.T) {
	db, sqls := openDB(t)
	meili := fakeMeili(map[string]any{"id": float64(2)}, map[string]any{"id": float64(1)}, map[string]any{"id": float64(3)})
	var fallbacks []string
	drv, err := Driver(Options{
		Client:       meili,
		Index:        "products",
		MaxResults:   50,
		DateFields:   map[string]string{"createdAt": "createdAtTs", "kirim": "shippedAtTs"},
		SearchParams: map[string]any{"matchingStrategy": "all"},
		OnFallback:   func(id, reason string) { fallbacks = append(fallbacks, id) },
	})
	if err != nil {
		t.Fatal(err)
	}
	tb := gormtable.MustDefine(gormtable.Definition[Item]{
		DB:        db,
		UTCOffset: "+08:00",
		Filters:   filters(),
		Search:    &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}},
		Driver:    drv,
	})
	page, err := tb.Query(ctx, q(
		"search", "kopi",
		"f.status", "aktif",
		"f.stok", "5",
		"f.nama", `ko"pi`,
		"f.kat", "1", "f.kat", "2", "f.kat.not", "1",
		"f.dibuat", "2026-10-19",
		"f.kirim.from", "2026-01",
		"f.tanpaMap", "2026-01-01",
		"f.lampiran", "true",
	))
	if err != nil {
		t.Fatal(err)
	}
	// 3 dibuang filter sisa (nama contains) di SQL
	if got := itemIDs(page.Data); !reflect.DeepEqual(got, []uint{2, 1}) {
		t.Fatalf("ids = %v", got)
	}
	if !reflect.DeepEqual(meili.uids, []string{"products"}) {
		t.Fatalf("uids = %v", meili.uids)
	}
	call := meili.idx.calls[0]
	start := time.Date(2026, 10, 18, 16, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC).Unix()
	shipped := time.Date(2025, 12, 31, 16, 0, 0, 0, time.UTC).Unix()
	wantFilter := strings.Join([]string{
		`status = "aktif"`,
		`stok >= 5`,
		`kategoriId NOT IN [1, 2]`,
		fmt.Sprintf(`(createdAtTs >= %d AND createdAtTs < %d)`, start, end),
		fmt.Sprintf(`(shippedAtTs >= %d)`, shipped),
	}, " AND ")
	want := SearchParams{Filter: wantFilter, Limit: 50, AttributesToRetrieve: []string{"id"}, Extra: map[string]any{"matchingStrategy": "all"}}
	if call.query != "kopi" || !reflect.DeepEqual(call.params, want) {
		t.Fatalf("call = %#v\nwant %#v", call, want)
	}
	if !reflect.DeepEqual(fallbacks, []string{"nama", "tanpaMap", "lampiran"}) {
		t.Fatalf("fallbacks = %v", fallbacks)
	}
	s := strings.Join(*sqls, "\n")
	for _, want := range []string{"LOWER(`items`.`nama`) LIKE", "NOT (`items`.`paid_at` >=", "stok > 0", "`items`.`id` IN (2,1,3)"} {
		if !strings.Contains(s, want) {
			t.Errorf("SQL tidak memuat %q:\n%s", want, s)
		}
	}
	for _, notWant := range []string{"`status` =", "kategori_id", "created_at", "shipped_at"} {
		if strings.Contains(s, notWant) {
			t.Errorf("filter pushdown %q tidak boleh ada di SQL:\n%s", notWant, s)
		}
	}
}

func TestNeverPushdown(t *testing.T) {
	db, sqls := openDB(t)
	meili := fakeMeili(map[string]any{"id": float64(1)}, map[string]any{"id": float64(4)})
	tb := gormtable.MustDefine(gormtable.Definition[Item]{
		DB:      db,
		Filters: filters()[:1],
		Search:  &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}},
		Driver:  MustDriver(Options{Client: meili, Index: "p", FilterPushdown: PushdownNever}),
	})
	page, err := tb.Query(ctx, q("search", "x", "f.status", "aktif"))
	if err != nil {
		t.Fatal(err)
	}
	if meili.idx.calls[0].params.Filter != "" {
		t.Fatalf("filter = %q", meili.idx.calls[0].params.Filter)
	}
	if got := itemIDs(page.Data); !reflect.DeepEqual(got, []uint{1}) {
		t.Fatalf("ids = %v", got)
	}
	if s := strings.Join(*sqls, "\n"); !strings.Contains(s, "`items`.`status` = \"aktif\"") {
		t.Fatalf("SQL:\n%s", s)
	}
}

func TestAlwaysPushdown(t *testing.T) {
	db, _ := openDB(t)
	fs := filters()
	make := func(f ...dt.Filter) error {
		_, err := gormtable.Define(gormtable.Definition[Item]{
			DB:      db,
			Filters: f,
			Search:  &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}},
			Driver: MustDriver(Options{Client: fakeMeili(), Index: "p", FilterPushdown: PushdownAlways,
				DateFields: map[string]string{"createdAt": "createdAtTs"}}),
		})
		return err
	}
	if err := make(fs[0], fs[3], fs[4]); err != nil {
		t.Fatal(err)
	}
	for i, want := range map[int]string{2: "contains", 6: "DateFields", 7: "Where"} {
		err := make(fs[i])
		var ce *dt.ConfigError
		if !errors.As(err, &ce) || !strings.Contains(err.Error(), want) {
			t.Errorf("filter %d: err = %v", i, err)
		}
	}
	jam := dt.Date("jam", "Jam", dt.Field("at"), dt.Format(dt.FormatTimeHm))
	if r := PushdownBlocker(jam, map[string]string{"at": "atTs"}); !strings.Contains(r, "format jam") {
		t.Fatalf("reason = %q", r)
	}
	if r := PushdownBlocker(fs[4], map[string]string{"createdAt": "createdAtTs"}); r != "" {
		t.Fatalf("reason = %q", r)
	}
}

func TestOptionsCastIDInvalidDate(t *testing.T) {
	c := fakeMeili()
	for _, o := range []Options{
		{Client: c, Index: "p", FilterPushdown: "kadang"},
		{Client: c, Index: "p", DateFields: map[string]string{"a": "x; DROP"}},
		{Client: c, Index: ""},
		{Index: "p"},
		{Client: c, Index: "p", MaxResults: -1},
		{Client: c, Index: "p", PrimaryKey: "a b"},
	} {
		var ce *dt.ConfigError
		if _, err := Driver(o); !errors.As(err, &ce) {
			t.Errorf("%+v: err = %v", o, err)
		}
	}

	db, _ := openDB(t)
	meili := fakeMeili(map[string]any{"uid": "3"}, map[string]any{"uid": "1"}, map[string]any{"other": 1}, map[string]any{"uid": nil})
	tb := gormtable.MustDefine(gormtable.Definition[Item]{
		DB:      db,
		Filters: []dt.Filter{filters()[4]},
		Search:  &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}},
		Driver: MustDriver(Options{Client: meili, Index: "p", PrimaryKey: "uid",
			CastID: func(id any) any {
				var n uint
				fmt.Sscan(id.(string), &n)
				return n
			},
			DateFields: map[string]string{"dibuat": "createdAtTs"}}),
	})
	page, err := tb.Query(ctx, q("search", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(page.Data); !reflect.DeepEqual(got, []uint{3, 1}) {
		t.Fatalf("ids = %v", got)
	}
	if got := meili.idx.calls[0].params.AttributesToRetrieve; !reflect.DeepEqual(got, []string{"uid"}) {
		t.Fatalf("attrs = %v", got)
	}
	_, err = tb.Query(ctx, q("search", "x", "f.dibuat", "kemarin"))
	var qe *dt.QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("err = %v", err)
	}

	// error client diteruskan
	meili.idx.err = errors.New("down")
	if _, err := tb.Query(ctx, q("search", "x")); err == nil || err.Error() != "down" {
		t.Fatalf("err = %v", err)
	}
}

func TestDefaultCastAndValue(t *testing.T) {
	if defaultCastID(float64(3)) != int64(3) || defaultCastID(1.5) != 1.5 || defaultCastID(json.Number("7")) != int64(7) ||
		defaultCastID(json.Number("x")) != "x" || defaultCastID("a") != "a" {
		t.Fatal("defaultCastID")
	}
	cases := map[any]string{5: "5", 2.5: "2.5", false: "false", `a"b\c`: `"a\"b\\c"`, "x": `"x"`}
	for in, want := range cases {
		if got := Value(in); got != want {
			t.Errorf("Value(%v) = %s, want %s", in, got, want)
		}
	}
	if got := boundsExpr("a", dt.Bounds{}); got != "" {
		t.Fatalf("bounds kosong = %q", got)
	}
	p := dt.Period{Start: time.Unix(10, 0), End: time.Unix(20, 0)}
	if got := boundsExpr("a", dt.Bounds{Not: &p}); got != "(a < 10 OR a >= 20)" {
		t.Fatalf("bounds not = %q", got)
	}
}

func TestSettingsAndSync(t *testing.T) {
	db, _ := openDB(t)
	meili := fakeMeili()
	tb := gormtable.MustDefine(gormtable.Definition[Item]{
		DB:       db,
		Sortable: []string{"nama", "createdAt"},
		Filters: []dt.Filter{
			dt.Select("status", "Status", dt.Field("status"), dt.Options()),
			dt.Multi("kat", "Kategori", dt.Field("kategoriId"), dt.Options()),
			dt.DateRange("dibuat", "Dibuat", dt.Field("createdAt")),
			dt.Select("cb", "Callback", dt.Options(), gormtable.Where(func(db *gorm.DB, _ dt.Resolved, _ dt.FilterContext) *gorm.DB { return db })),
		},
		Search: &dt.SearchConfig{
			Columns:       []dt.SearchColumn{dt.Col("status"), dt.Col("nama").W(5), dt.Col("kategoriId").W(2)},
			TypoTolerance: &dt.TypoTolerance{Driver: dt.TypoTrigram, MinWordLength: 5},
		},
		Driver: MustDriver(Options{Client: meili, Index: "products", DateFields: map[string]string{"createdAt": "createdAtTs", "x": "zTs"}}),
	})
	want := IndexSettings{
		SearchableAttributes: []string{"nama", "kategoriId", "status"},
		FilterableAttributes: []string{"status", "kategoriId", "createdAtTs", "zTs"},
		SortableAttributes:   []string{"nama", "createdAt"},
		TypoTolerance:        &TypoTolerance{Enabled: true, MinWordSizeForTypos: MinWordSizeForTypos{OneTypo: 5, TwoTypos: 10}},
	}
	if got := Settings(tb); !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %+v", got)
	}
	b, _ := json.Marshal(want)
	if string(b) != `{"searchableAttributes":["nama","kategoriId","status"],"filterableAttributes":["status","kategoriId","createdAtTs","zTs"],`+
		`"sortableAttributes":["nama","createdAt"],"typoTolerance":{"enabled":true,"minWordSizeForTypos":{"oneTypo":5,"twoTypos":10}}}` {
		t.Fatalf("json = %s", b)
	}

	// client & index default dari driver tabel
	got, err := SyncSettings(ctx, tb, nil, "", false)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
	if meili.uids[len(meili.uids)-1] != "products" || !reflect.DeepEqual(meili.idx.settings, []IndexSettings{want}) {
		t.Fatalf("uids = %v, settings = %+v", meili.uids, meili.idx.settings)
	}

	// client/index eksplisit + dryRun
	other := fakeMeili()
	if _, err := SyncSettings(ctx, tb, other, "lain", true); err != nil || len(other.idx.settings) != 0 || len(other.uids) != 0 {
		t.Fatalf("dryRun mengirim: %v %v", other.idx.settings, err)
	}
	// index tanpa UpdateSettings
	if _, err := SyncSettings(ctx, tb, searchOnlyClient{}, "x", false); err == nil {
		t.Fatal("harus error")
	}
}

func TestSyncWithoutDriver(t *testing.T) {
	db, _ := openDB(t)
	tb := gormtable.MustDefine(gormtable.Definition[Item]{DB: db, Search: &dt.SearchConfig{Columns: []dt.SearchColumn{dt.Col("nama")}}})
	var ce *dt.ConfigError
	if _, err := SyncSettings(ctx, tb, nil, "", false); !errors.As(err, &ce) {
		t.Fatalf("err = %v", err)
	}
	meili := fakeMeili()
	s, err := SyncSettings(ctx, tb, meili, "x", false)
	if err != nil || !reflect.DeepEqual(s, IndexSettings{SearchableAttributes: []string{"nama"}}) {
		t.Fatalf("s = %+v, err = %v", s, err)
	}
	if got := SettingsWith(tb, map[string]string{"a": "aTs"}); !reflect.DeepEqual(got.FilterableAttributes, []string{"aTs"}) {
		t.Fatalf("got = %+v", got)
	}
}
