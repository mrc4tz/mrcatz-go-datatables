package editorimg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

// memStorage adalah Storage di memori untuk tes (padanan memoryStorage JS).
type memStorage struct {
	mu    sync.Mutex
	files map[string]memFile
	now   func() time.Time
}

type memFile struct {
	data     []byte
	modified time.Time
}

func newMem() *memStorage {
	return &memStorage{files: map[string]memFile{}, now: time.Now}
}

func (s *memStorage) Put(_ context.Context, p string, data []byte, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[p] = memFile{data, s.now()}
	return nil
}

func (s *memStorage) Get(_ context.Context, p string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[p]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return f.data, nil
}

func (s *memStorage) Delete(_ context.Context, p string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, p)
	return nil
}

func (s *memStorage) List(_ context.Context, dir string) ([]FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []FileInfo
	for p, f := range s.files {
		if rest, ok := strings.CutPrefix(p, dir+"/"); ok && !strings.Contains(rest, "/") {
			out = append(out, FileInfo{p, f.modified})
		}
	}
	return out, nil
}

func (s *memStorage) URL(p string) string {
	if p == "" {
		return "/storage"
	}
	return "/storage/" + p
}

func (s *memStorage) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var k []string
	for p := range s.files {
		k = append(k, p)
	}
	slices.Sort(k)
	return k
}

func png(n int) *File {
	return &File{Data: bytes.Repeat([]byte{1}, n), MimeType: "image/png", Filename: "a.png"}
}

var ulidURL = regexp.MustCompile(`^/storage/editor-images/tmp/[0-9A-HJKMNP-TV-Z]{26}\.png$`)

func TestUploadThenProcessMovesToPermanent(t *testing.T) {
	st := newMem()
	ei := New(Options{Storage: st})
	res, err := ei.Upload(ctx, png(10))
	if err != nil {
		t.Fatal(err)
	}
	if !ulidURL.MatchString(res.URL) || res.Path != strings.TrimPrefix(res.URL, "/storage/") {
		t.Fatalf("upload = %+v", res)
	}

	html, err := ei.ProcessHTML(ctx, `<p><img src="`+res.URL+`"></p><img src='`+res.URL+`'>`, "")
	if err != nil {
		t.Fatal(err)
	}
	final := strings.Replace(res.URL, "/tmp/", "/", 1)
	if html != `<p><img src="`+final+`"></p><img src='`+final+`'>` {
		t.Errorf("html = %s", html)
	}
	if got := st.keys(); !slices.Equal(got, []string{strings.TrimPrefix(final, "/storage/")}) {
		t.Errorf("files = %v", got)
	}

	// Diproses ulang (file tmp sudah dipindah) → URL dibiarkan apa adanya.
	again, err := ei.ProcessHTML(ctx, `<img src="`+res.URL+`">`, "")
	if err != nil || again != `<img src="`+res.URL+`">` {
		t.Errorf("ulang = %q %v", again, err)
	}
}

func TestProcessDeletesRemovedButNotForeign(t *testing.T) {
	st := newMem()
	ei := New(Options{Storage: st})
	_ = st.Put(ctx, "editor-images/old.png", []byte{1}, "")
	_ = st.Put(ctx, "editor-images/keep.png", []byte{1}, "")
	_ = st.Put(ctx, "avatars/x.png", []byte{1}, "")
	_ = st.Put(ctx, "editor-images/tmp/orang-lain.png", []byte{1}, "")
	old := `<img src="/storage/editor-images/old.png"><img src="/storage/editor-images/keep.png">` +
		`<img src="/storage/avatars/x.png"><img src="https://cdn.x/y.png"><img src="/storage/editor-images/tmp/orang-lain.png">`
	if _, err := ei.ProcessHTML(ctx, `<p>kosong</p><img src="/storage/editor-images/keep.png?v=2">`, old); err != nil {
		t.Fatal(err)
	}
	// keep.png tetap ada: HTML baru masih memuat URL-nya (sebagai substring,
	// sama dengan `out.includes(url)` di JS). Tmp milik orang lain & URL luar
	// tidak disentuh.
	want := []string{"avatars/x.png", "editor-images/keep.png", "editor-images/tmp/orang-lain.png"}
	if got := st.keys(); !slices.Equal(got, want) {
		t.Errorf("files = %v, mau %v", got, want)
	}

	// html & oldHTML kosong → tidak melakukan apa-apa.
	if out, err := ei.ProcessHTML(ctx, "", ""); out != "" || err != nil {
		t.Errorf("kosong = %q %v", out, err)
	}
}

func TestValidationAndCleanup(t *testing.T) {
	st := newMem()
	ei := New(Options{Storage: st, MaxSizeKB: 1, TmpLifetime: time.Hour})

	var ie *ImageError
	if _, err := ei.Upload(ctx, &File{Data: []byte("x"), MimeType: "text/plain"}); !errors.As(err, &ie) || ie.Code != CodeNotImage || ie.Status() != 422 {
		t.Errorf("not_image: %v", err)
	}
	if _, err := ei.Upload(ctx, png(2048)); !errors.As(err, &ie) || ie.Code != CodeTooLarge || ie.Msg != "Ukuran gambar maksimal 0MB." {
		t.Errorf("too_large: %v", err)
	}
	if _, err := ei.Upload(ctx, nil); !errors.As(err, &ie) || ie.Code != CodeRequired || ie.Msg != "Gambar wajib diunggah." {
		t.Errorf("required: %v", err)
	}
	if _, err := ei.Upload(ctx, &File{MimeType: "image/png"}); !errors.As(err, &ie) || ie.Code != CodeRequired {
		t.Errorf("required (kosong): %v", err)
	}
	// Size eksplisit menang atas len(Data).
	if _, err := ei.Upload(ctx, &File{Data: []byte{1}, MimeType: "image/png", Size: 5000}); !errors.As(err, &ie) || ie.Code != CodeTooLarge {
		t.Errorf("too_large (Size): %v", err)
	}

	res, err := ei.Upload(ctx, png(10))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if n := ei.CleanupTmp(ctx, now); n != 0 {
		t.Errorf("cleanup sekarang = %d", n)
	}
	if n := ei.CleanupTmp(ctx, now.Add(2*time.Hour)); n != 1 {
		t.Errorf("cleanup +2j = %d", n)
	}
	if slices.Contains(st.keys(), res.Path) {
		t.Error("file tmp masih ada")
	}

	if got := New(Options{Storage: st}).Config(); got != (Config{Path: "editor-images", TmpPath: "editor-images/tmp", MaxSizeKB: 2048}) {
		t.Errorf("config = %+v", got)
	}
	if got := New(Options{Storage: st, Path: "/konten/gambar/"}).Config(); got.TmpPath != "konten/gambar/tmp" {
		t.Errorf("config path = %+v", got)
	}
	if New(Options{Storage: st, MaxSizeKB: 2048}).tooLarge().Msg != "Ukuran gambar maksimal 2MB." ||
		New(Options{Storage: st, MaxSizeKB: 1536}).tooLarge().Msg != "Ukuran gambar maksimal 1.5MB." {
		t.Error("pesan ukuran")
	}
}

func TestExtension(t *testing.T) {
	st := newMem()
	ei := New(Options{Storage: st, MimeTypes: []string{"image/png", "image/x-icon", "image/bmp"}})
	r1, _ := ei.Upload(ctx, &File{Data: []byte{1}, MimeType: "image/x-icon", Filename: "Fav.ICO"})
	r2, _ := ei.Upload(ctx, &File{Data: []byte{1}, MimeType: "image/bmp"})
	if !strings.HasSuffix(r1.Path, ".ico") || !strings.HasSuffix(r2.Path, ".bin") {
		t.Errorf("ext = %s %s", r1.Path, r2.Path)
	}
}

func TestToPath(t *testing.T) {
	ei := New(Options{Storage: LocalDisk(t.TempDir(), "https://cdn.app.test/files/")})
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://cdn.app.test/files/editor-images/a.png?x=1#y", "editor-images/a.png", true},
		{"/editor-images/a.png", "editor-images/a.png", true},
		{"https://lain.test/editor-images/a.png", "", false},
		{"HTTP://lain.test/a.png", "", false},
		{"https://cdn.app.test/files", "", false},
	}
	for _, c := range cases {
		got, ok := ei.ToPath(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ToPath(%q) = %q %v", c.in, got, ok)
		}
	}
}

func TestULID(t *testing.T) {
	a := ulidAt(time.UnixMilli(1))
	b := ulidAt(time.UnixMilli(1 << 40))
	if len(a) != 26 || a[:10] != "0000000001" || !(a[:10] < b[:10]) {
		t.Errorf("ulid = %s %s", a, b)
	}
	if NewULID() == NewULID() {
		t.Error("ulid harus acak")
	}
}

func TestLocalDisk(t *testing.T) {
	root := t.TempDir()
	st := LocalDisk(root, "/uploads/")
	if st.URL("") != "/uploads" || st.URL("/a/b.png") != "/uploads/a/b.png" {
		t.Errorf("URL = %q %q", st.URL(""), st.URL("/a/b.png"))
	}
	if err := st.Put(ctx, "a/b/c.png", []byte("isi"), "image/png"); err != nil {
		t.Fatal(err)
	}
	if data, err := st.Get(ctx, "a/b/c.png"); err != nil || string(data) != "isi" {
		t.Errorf("Get = %q %v", data, err)
	}
	if _, err := st.Get(ctx, "a/tidak-ada.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get tidak ada = %v", err)
	}
	_ = os.MkdirAll(filepath.Join(root, "a", "b", "sub"), 0o755)
	list, err := st.List(ctx, "a/b/")
	if err != nil || len(list) != 1 || list[0].Path != "a/b/c.png" || list[0].Modified.IsZero() {
		t.Errorf("List = %+v %v", list, err)
	}
	if list, err := st.List(ctx, "belum/ada"); err != nil || len(list) != 0 {
		t.Errorf("List kosong = %+v %v", list, err)
	}
	if err := st.Delete(ctx, "a/b/c.png"); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, "a/b/c.png"); err != nil {
		t.Errorf("Delete ulang harus nil: %v", err)
	}

	// Penjaga path traversal.
	outside := filepath.Join(filepath.Dir(root), "luar.txt")
	for _, p := range []string{"../luar.txt", "a/../../luar.txt", outside, `..\luar.txt`, "a\x00b"} {
		if err := st.Put(ctx, p, []byte("x"), ""); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("Put(%q) = %v, mau ErrOutsideRoot", p, err)
		}
		if _, err := st.Get(ctx, p); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("Get(%q) = %v", p, err)
		}
	}
	if _, err := os.Stat(outside); err == nil {
		t.Error("file tertulis di luar root")
	}
	if err := st.Delete(ctx, ""); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("Delete root = %v", err)
	}
}

func TestPipelineOnLocalDisk(t *testing.T) {
	root := t.TempDir()
	ei := New(Options{Storage: LocalDisk(root, "/storage"), TmpLifetime: time.Minute})
	res, err := ei.Upload(ctx, png(3))
	if err != nil {
		t.Fatal(err)
	}
	// File tmp lama (tak pernah disimpan) harus dibersihkan oleh ProcessHTML.
	stale := filepath.Join(root, "editor-images", "tmp", "basi.png")
	_ = os.WriteFile(stale, []byte{1}, 0o644)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(stale, old, old)

	html, err := ei.ProcessHTML(ctx, `<img src="`+res.URL+`">`, "")
	if err != nil {
		t.Fatal(err)
	}
	final := strings.Replace(res.Path, "/tmp/", "/", 1)
	if html != `<img src="/storage/`+final+`">` {
		t.Errorf("html = %s", html)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(final))); err != nil {
		t.Errorf("file permanen: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "editor-images", "tmp")); len(entries) != 0 {
		t.Errorf("tmp tersisa: %v", entries)
	}
	// Gambar dibuang dari HTML → dihapus dari disk.
	if _, err := ei.ProcessHTML(ctx, "<p></p>", html); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(final))); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("file lama masih ada: %v", err)
	}
}

func multipartReq(t *testing.T, field, filename, mime string, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if field != "" {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
		h.Set("Content-Type", mime)
		w, _ := mw.CreatePart(h)
		_, _ = w.Write(data)
	}
	_ = mw.Close()
	r := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

func TestHandler(t *testing.T) {
	st := newMem()
	ei := New(Options{Storage: st, MaxSizeKB: 1})
	h := ei.Handler("image")
	serve := func(r *http.Request) (int, string) {
		rec := httptest.NewRecorder()
		h(rec, r)
		b, _ := io.ReadAll(rec.Body)
		return rec.Code, string(b)
	}

	code, body := serve(multipartReq(t, "image", "a.png", "image/png", []byte{1, 2, 3}))
	if code != 201 || !regexp.MustCompile(`^\{"url":"/storage/editor-images/tmp/\w{26}\.png","path":"editor-images/tmp/\w{26}\.png"\}$`).MatchString(body) {
		t.Errorf("sukses: %d %s", code, body)
	}
	cases := []struct {
		r    *http.Request
		body string
	}{
		{multipartReq(t, "image", "a.txt", "text/plain", []byte("x")), `{"message":"File harus berupa gambar.","error":"Unprocessable Entity","statusCode":422}`},
		{multipartReq(t, "lain", "a.png", "image/png", []byte{1}), `{"message":"Gambar wajib diunggah.","error":"Unprocessable Entity","statusCode":422}`},
		{httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("x")), `{"message":"Gambar wajib diunggah.","error":"Unprocessable Entity","statusCode":422}`},
		{multipartReq(t, "image", "a.png", "image/png", make([]byte, 2048)), `{"message":"Ukuran gambar maksimal 0MB.","error":"Unprocessable Entity","statusCode":422}`},
		{multipartReq(t, "image", "a.png", "image/png", make([]byte, 2<<20)), `{"message":"Ukuran gambar maksimal 0MB.","error":"Unprocessable Entity","statusCode":422}`},
	}
	for i, c := range cases {
		if code, body := serve(c.r); code != 422 || body != c.body {
			t.Errorf("kasus %d: %d %s", i, code, body)
		}
	}
}
