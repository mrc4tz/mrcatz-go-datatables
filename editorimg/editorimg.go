// Package editorimg adalah pipeline gambar editor rich text — port dari
// `editorImages()` versi JS (pengganti `config('mrcatz.editor_image')` mode
// `upload`, `EditorImageUploadController`, `processEditorImages()` dan
// `php artisan mrcatz:cleanup-editor-images`).
//
// Alurnya sama dengan Laravel:
//  1. Editor mengunggah gambar → Upload menyimpannya di `<path>/tmp/<ulid>.<ext>`.
//  2. Saat form disimpan → ProcessHTML(html, oldHTML) memindahkan gambar tmp
//     ke `<path>/`, mengganti URL di HTML, dan menghapus gambar yang sudah tidak
//     dipakai dibanding oldHTML.
//  3. Gambar tmp yang tidak pernah disimpan dihapus oleh CleanupTmp (dipanggil
//     otomatis di ProcessHTML, atau jadwalkan sendiri).
//
// Storage bisa diganti (S3, R2, MinIO, ...) dengan mengimplementasikan
// Storage; bawaannya LocalDisk.
//
// Contoh dengan Gin (endpoint upload untuk `field.upload` di Vue):
//
//	imgs := editorimg.New(editorimg.Options{
//		Storage: editorimg.LocalDisk("storage/public", "/storage"),
//	})
//	r.Static("/storage", "storage/public")
//	r.POST("/api/editor-images", gin.WrapF(imgs.Handler("image")))
//
//	// atau manual di handler sendiri:
//	fh, _ := c.FormFile("image")         // nil → ImageError "required"
//	file, err := editorimg.FromMultipart(fh)
//	res, err := imgs.Upload(c.Request.Context(), file) // *ImageError → 422
//
//	// saat form disimpan:
//	post.Body, err = imgs.ProcessHTML(ctx, input.Body, old.Body)
package editorimg

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime/multipart"
	"regexp"
	"slices"
	"strings"
	"time"

	dt "github.com/mrc4tz/mrcatz-go-datatables/datatable"
)

// FileInfo adalah satu file hasil Storage.List.
type FileInfo struct {
	Path     string
	Modified time.Time
}

// Storage adalah tempat penyimpanan gambar. Semua path relatif terhadap root
// storage dan memakai "/".
type Storage interface {
	Put(ctx context.Context, path string, data []byte, contentType string) error
	// Get mengembalikan error yang memenuhi errors.Is(err, fs.ErrNotExist)
	// bila file tidak ada.
	Get(ctx context.Context, path string) ([]byte, error)
	// Delete tidak error bila file sudah tidak ada.
	Delete(ctx context.Context, path string) error
	// List mendaftar file langsung di bawah dir (tidak rekursif) beserta
	// waktu ubahnya. Folder yang tidak ada → daftar kosong.
	List(ctx context.Context, dir string) ([]FileInfo, error)
	// URL mengembalikan URL publik untuk path relatif ("" = URL dasar).
	URL(path string) string
}

// Options mengatur pipeline.
type Options struct {
	Storage Storage
	// Path: folder gambar permanen. Default "editor-images"; tmp di "<Path>/tmp".
	Path string
	// MaxSizeKB: ukuran maksimum dalam KB. Default 2048.
	MaxSizeKB int
	// TmpLifetime: umur file tmp sebelum dihapus CleanupTmp. Default 24 jam.
	TmpLifetime time.Duration
	// MimeTypes yang diizinkan. Default jpeg/png/gif/webp/avif (SVG sengaja
	// tidak, rawan XSS).
	MimeTypes []string
}

// DefaultMimeTypes adalah MIME bawaan yang diizinkan.
var DefaultMimeTypes = []string{"image/jpeg", "image/png", "image/gif", "image/webp", "image/avif"}

var extByMime = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/gif":  "gif",
	"image/webp": "webp",
	"image/avif": "avif",
}

// File adalah file unggahan (lihat FromMultipart untuk *multipart.FileHeader
// dari net/http maupun Gin).
type File struct {
	Data []byte
	// MimeType dari klien (header Content-Type bagian multipart).
	MimeType string
	Filename string
	// Size dalam byte; 0 = len(Data).
	Size int64
}

// UploadResult dikembalikan ke editor: {"url": ..., "path": ...}.
type UploadResult struct {
	URL  string `json:"url"`
	Path string `json:"path"`
}

// Kode ImageError.
const (
	CodeRequired = "required"
	CodeNotImage = "not_image"
	CodeTooLarge = "too_large"
)

// ImageError adalah kesalahan validasi unggahan (HTTP 422).
type ImageError struct {
	Code string // CodeRequired | CodeNotImage | CodeTooLarge
	Msg  string
}

func (e *ImageError) Error() string { return e.Msg }

// Status selalu 422 (Unprocessable Entity).
func (e *ImageError) Status() int { return 422 }

// Config adalah opsi setelah default diterapkan.
type Config struct {
	Path, TmpPath string
	MaxSizeKB     int
}

// Images adalah pipeline gambar editor.
type Images struct {
	storage  Storage
	base     string
	tmp      string
	maxBytes int64
	lifetime time.Duration
	mimes    []string
	baseURL  string
}

// New membuat pipeline. Panic bila Storage nil.
func New(o Options) *Images {
	if o.Storage == nil {
		panic("editorimg: Options.Storage wajib diisi")
	}
	base := strings.Trim(o.Path, "/")
	if o.Path == "" {
		base = "editor-images"
	}
	maxKB := o.MaxSizeKB
	if maxKB == 0 {
		maxKB = 2048
	}
	life := o.TmpLifetime
	if life == 0 {
		life = 24 * time.Hour
	}
	mimes := o.MimeTypes
	if mimes == nil {
		mimes = DefaultMimeTypes
	}
	return &Images{
		storage:  o.Storage,
		base:     base,
		tmp:      base + "/tmp",
		maxBytes: int64(maxKB) * 1024,
		lifetime: life,
		mimes:    mimes,
		baseURL:  strings.TrimRight(o.Storage.URL(""), "/"),
	}
}

// Config mengembalikan opsi efektif (path, path tmp, ukuran maksimum).
func (m *Images) Config() Config {
	return Config{Path: m.base, TmpPath: m.tmp, MaxSizeKB: int(m.maxBytes / 1024)}
}

var schemeRe = regexp.MustCompile(`(?i)^[a-z]+://`)

// ToPath mengubah URL publik menjadi path relatif di storage; false bila URL
// bukan milik storage ini (mis. URL CDN luar).
func (m *Images) ToPath(u string) (string, bool) {
	rel := u
	if m.baseURL != "" && strings.HasPrefix(u, m.baseURL) {
		rel = u[len(m.baseURL):]
	} else if schemeRe.MatchString(u) {
		return "", false
	}
	rel = strings.TrimLeft(rel, "/")
	if i := strings.IndexAny(rel, "?#"); i >= 0 {
		rel = rel[:i]
	}
	return rel, rel != ""
}

// Upload memvalidasi lalu menyimpan gambar ke folder tmp. Error validasi
// berupa *ImageError (422).
func (m *Images) Upload(ctx context.Context, f *File) (UploadResult, error) {
	if f == nil || len(f.Data) == 0 {
		return UploadResult{}, &ImageError{Code: CodeRequired, Msg: "Gambar wajib diunggah."}
	}
	if !slices.Contains(m.mimes, f.MimeType) {
		return UploadResult{}, &ImageError{Code: CodeNotImage, Msg: "File harus berupa gambar."}
	}
	size := f.Size
	if size == 0 {
		size = int64(len(f.Data))
	}
	if size > m.maxBytes {
		return UploadResult{}, m.tooLarge()
	}
	ext, ok := extByMime[f.MimeType]
	if !ok {
		ext = "bin"
		if f.Filename != "" {
			parts := strings.Split(f.Filename, ".")
			ext = strings.ToLower(parts[len(parts)-1])
		}
	}
	p := m.tmp + "/" + NewULID() + "." + ext
	if err := m.storage.Put(ctx, p, f.Data, f.MimeType); err != nil {
		return UploadResult{}, err
	}
	return UploadResult{URL: m.storage.URL(p), Path: p}, nil
}

func (m *Images) tooLarge() *ImageError {
	mb := math.Round(float64(m.maxBytes)/1024/1024*10) / 10
	return &ImageError{Code: CodeTooLarge, Msg: fmt.Sprintf("Ukuran gambar maksimal %sMB.", dt.FormatNumber(mb))}
}

// CleanupTmp menghapus file tmp yang umurnya ≥ TmpLifetime relatif terhadap
// now (zero = time.Now()). Best-effort: mengembalikan jumlah yang terhapus.
func (m *Images) CleanupTmp(ctx context.Context, now time.Time) int {
	if now.IsZero() {
		now = time.Now()
	}
	items, err := m.storage.List(ctx, m.tmp)
	if err != nil {
		return 0
	}
	deleted := 0
	for _, it := range items {
		if now.Sub(it.Modified) < m.lifetime {
			continue
		}
		if m.storage.Delete(ctx, it.Path) == nil {
			deleted++
		}
		// gagal → dicoba lagi di pemanggilan berikutnya
	}
	return deleted
}

var srcRe = regexp.MustCompile(`src=["']([^"']+)["']`)

// ProcessHTML memproses HTML editor sebelum disimpan (pengganti
// `processEditorImages`): memindahkan gambar tmp ke folder permanen,
// mengganti URL-nya, dan menghapus gambar di oldHTML yang sudah tidak ada di
// html. Lalu CleanupTmp dijalankan.
func (m *Images) ProcessHTML(ctx context.Context, html, oldHTML string) (string, error) {
	if html == "" && oldHTML == "" {
		return html, nil
	}
	out := html
	seen := map[string]bool{}
	for _, match := range srcRe.FindAllStringSubmatch(out, -1) {
		u := match[1]
		from, ok := m.ToPath(u)
		if seen[u] || !ok || !strings.HasPrefix(from, m.tmp+"/") {
			continue
		}
		seen[u] = true
		data, err := m.storage.Get(ctx, from)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if len(data) == 0 {
			continue // sudah dipindah / hilang — biarkan URL apa adanya
		}
		to := m.base + "/" + from[len(m.tmp)+1:]
		if err := m.storage.Put(ctx, to, data, ""); err != nil {
			return "", err
		}
		_ = m.storage.Delete(ctx, from)
		out = strings.ReplaceAll(out, u, m.storage.URL(to))
	}

	if oldHTML != "" {
		for _, match := range srcRe.FindAllStringSubmatch(oldHTML, -1) {
			u := match[1]
			if strings.Contains(out, u) {
				continue
			}
			p, ok := m.ToPath(u)
			// Hanya hapus file di dalam folder editor (bukan tmp / URL luar).
			if !ok || !strings.HasPrefix(p, m.base+"/") || strings.HasPrefix(p, m.tmp+"/") {
				continue
			}
			_ = m.storage.Delete(ctx, p)
		}
	}

	m.CleanupTmp(ctx, time.Time{})
	return out, nil
}

// FromMultipart membaca *multipart.FileHeader (dari r.FormFile maupun
// c.FormFile Gin) menjadi *File. fh nil → (nil, nil), yang oleh Upload
// dilaporkan sebagai ImageError "required".
func FromMultipart(fh *multipart.FileHeader) (*File, error) {
	if fh == nil {
		return nil, nil
	}
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return &File{Data: data, MimeType: fh.Header.Get("Content-Type"), Filename: fh.Filename, Size: fh.Size}, nil
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID membuat ID 26 karakter ala ULID (10 karakter waktu ms + 16 acak,
// alfabet Crockford) — urut berdasarkan waktu seperti `Str::ulid()`.
func NewULID() string { return ulidAt(time.Now()) }

func ulidAt(t time.Time) string {
	var b [26]byte
	ms := t.UnixMilli()
	for i := 9; i >= 0; i-- {
		b[i] = crockford[ms%32]
		ms /= 32
	}
	var r [16]byte
	_, _ = rand.Read(r[:])
	for i, x := range r {
		b[10+i] = crockford[x%32]
	}
	return string(b[:])
}
