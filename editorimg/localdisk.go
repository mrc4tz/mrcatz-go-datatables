package editorimg

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// LocalDisk adalah Storage di disk lokal, mis. folder yang disajikan statis
// (`r.Static("/storage", "storage/public")` di Gin atau http.FileServer) di
// publicURL.
//
//	editorimg.LocalDisk("storage/public", "/storage")
//
// Semua path dijaga agar tidak keluar dari root (`../`, path absolut).
func LocalDisk(root, publicURL string) Storage {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = filepath.Clean(root)
	}
	return &localDisk{root: abs, publicURL: strings.TrimRight(publicURL, "/")}
}

type localDisk struct {
	root      string
	publicURL string
}

// ErrOutsideRoot dikembalikan LocalDisk bila path keluar dari root.
var ErrOutsideRoot = errors.New("editorimg: path di luar root")

// safe mengubah path relatif menjadi path absolut di dalam root.
func (d *localDisk) safe(rel string) (string, error) {
	// Path absolut, backslash (Windows) dan NUL ditolak; sisanya dicek
	// setelah Join (yang sudah membersihkan "../").
	if strings.ContainsRune(rel, '\x00') || filepath.IsAbs(rel) || strings.Contains(rel, `\`) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoot, rel)
	}
	abs := filepath.Join(d.root, filepath.FromSlash(rel))
	if abs != d.root && !strings.HasPrefix(abs, d.root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoot, rel)
	}
	return abs, nil
}

func (d *localDisk) Put(_ context.Context, p string, data []byte, _ string) error {
	abs, err := d.safe(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

func (d *localDisk) Get(_ context.Context, p string) ([]byte, error) {
	abs, err := d.safe(p)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs) // file tidak ada → *fs.PathError (fs.ErrNotExist)
}

func (d *localDisk) Delete(_ context.Context, p string) error {
	abs, err := d.safe(p)
	if err != nil {
		return err
	}
	if abs == d.root {
		return fmt.Errorf("%w: tidak boleh menghapus root", ErrOutsideRoot)
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (d *localDisk) List(_ context.Context, dir string) ([]FileInfo, error) {
	abs, err := d.safe(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, nil // folder belum ada → kosong
	}
	prefix := strings.TrimRight(dir, "/")
	var out []FileInfo
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, FileInfo{Path: path.Join(prefix, e.Name()), Modified: info.ModTime()})
	}
	return out, nil
}

func (d *localDisk) URL(p string) string {
	if p == "" {
		return d.publicURL
	}
	return d.publicURL + "/" + strings.TrimLeft(p, "/")
}
