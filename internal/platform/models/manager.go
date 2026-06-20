package models

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logf is an optional progress sink (human-readable log lines).
type Logf func(format string, args ...any)

// ProgressFunc receives byte-level download progress so a JobDownloadModel can
// stream "1.2/2.0 GB" to the TUI. total is -1 when the server sends no
// Content-Length.
type ProgressFunc func(asset string, downloaded, total int64)

// Manager resolves, verifies, and lazily fetches models into <dataDir>/models.
type Manager struct {
	dataDir  string
	manifest Manifest
	client   *http.Client

	mu       sync.Mutex // serializes downloads (one fetch at a time per manager)
	progress ProgressFunc
}

// New builds a manager over the default pinned manifest.
func New(dataDir string) *Manager { return NewWithManifest(dataDir, DefaultManifest()) }

// NewWithManifest builds a manager over a custom manifest (used by tests).
func NewWithManifest(dataDir string, m Manifest) *Manager {
	return &Manager{
		dataDir:  dataDir,
		manifest: m,
		client:   &http.Client{Timeout: 30 * time.Minute},
	}
}

// Manifest returns the manager's catalog (read-only use).
func (m *Manager) Manifest() Manifest { return m.manifest }

// OnProgress installs a byte-progress callback for subsequent fetches.
func (m *Manager) OnProgress(fn ProgressFunc) { m.progress = fn }

// RuntimeLib resolves the onnxruntime/sherpa shared library for a backend,
// falling back to the "any" runtime. Returns ("", false) if not installed.
func (m *Manager) RuntimeLib(b Backend) (string, bool) {
	for _, cand := range []Backend{b, BackendAny} {
		assets := m.manifest.Runtime[cand]
		for _, a := range assets {
			p := filepath.Join(m.modelsRoot(), "runtime", string(cand), a.Filename)
			if exists(p) {
				return p, true
			}
		}
	}
	return "", false
}

// FfmpegPath returns the installed static ffmpeg, or "ffmpeg" to use PATH.
func (m *Manager) FfmpegPath() string {
	p := filepath.Join(m.modelsRoot(), "bin", "ffmpeg")
	if exists(p) {
		return p
	}
	return "ffmpeg"
}

// ResolvePaths returns the on-disk layout for a model variant (without
// fetching). Errors only if the id/variant is unknown to the manifest.
func (m *Manager) ResolvePaths(id string, b Backend, t Tier) (ModelPaths, error) {
	mdl, ok := m.manifest.Find(id)
	if !ok {
		return ModelPaths{}, fmt.Errorf("models: unknown model %q", id)
	}
	v, ok := selectVariant(mdl, b, t)
	if !ok {
		return ModelPaths{}, fmt.Errorf("models: %q has no variant for backend=%s tier=%s", id, b, t)
	}
	dir := filepath.Join(m.modelsRoot(), id, variantDirName(v))
	mp := ModelPaths{
		ModelID: id,
		Dir:     dir,
		Files:   make(map[string]string, len(v.Assets)),
		assets:  v.Assets,
	}
	for _, a := range v.Assets {
		mp.Files[a.Filename] = filepath.Join(dir, a.Filename)
	}
	if pa, ok := v.primaryAsset(); ok {
		mp.File = filepath.Join(dir, pa.Filename)
	}
	return mp, nil
}

// Available reports whether every non-optional asset of the variant is present
// and non-empty on disk.
func (m *Manager) Available(id string, b Backend, t Tier) bool {
	mp, err := m.ResolvePaths(id, b, t)
	if err != nil {
		return false
	}
	for _, a := range mp.assets {
		if a.Optional {
			continue
		}
		if !fileBigger(mp.Files[a.Filename], 1) {
			return false
		}
	}
	return true
}

// Ensure fetches the variant if missing (lazy fetch-if-missing on first use).
func (m *Manager) Ensure(ctx context.Context, id string, b Backend, t Tier, log Logf) error {
	if m.Available(id, b, t) {
		return nil
	}
	return m.Download(ctx, id, b, t, log)
}

// Download fetches a model variant into the data dir. Idempotent: present,
// correctly-sized files are skipped; archives are extracted; SHA256 is verified
// when present.
func (m *Manager) Download(ctx context.Context, id string, b Backend, t Tier, log Logf) error {
	if log == nil {
		log = func(string, ...any) {}
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	mdl, ok := m.manifest.Find(id)
	if !ok {
		return fmt.Errorf("models: unknown model %q", id)
	}
	v, ok := selectVariant(mdl, b, t)
	if !ok {
		return fmt.Errorf("models: %q has no variant for backend=%s tier=%s", id, b, t)
	}
	dir := filepath.Join(m.modelsRoot(), id, variantDirName(v))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if mdl.License != "" {
		log("models: %s (license: %s)", id, mdl.License)
	}
	for _, a := range v.Assets {
		if err := m.fetchAsset(ctx, dir, a, log); err != nil {
			if a.Optional {
				log("models: optional asset %s failed (%v); continuing", a.Filename, err)
				continue
			}
			return fmt.Errorf("models: %s/%s: %w", id, a.Filename, err)
		}
	}
	return nil
}

// EnsureRuntime fetches the onnxruntime/sherpa shared lib for a backend if missing.
func (m *Manager) EnsureRuntime(ctx context.Context, b Backend, log Logf) error {
	if log == nil {
		log = func(string, ...any) {}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cand := range []Backend{b, BackendAny} {
		assets := m.manifest.Runtime[cand]
		if len(assets) == 0 {
			continue
		}
		dir := filepath.Join(m.modelsRoot(), "runtime", string(cand))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for _, a := range assets {
			if err := m.fetchAsset(ctx, dir, a, log); err != nil && !a.Optional {
				return fmt.Errorf("models: runtime %s/%s: %w", cand, a.Filename, err)
			}
		}
		return nil
	}
	return fmt.Errorf("models: no runtime asset for backend %s", b)
}

// Verify re-checks SHA256 for every asset of a resolved variant that declares one.
func (m *Manager) Verify(p ModelPaths) error {
	for _, a := range p.assets {
		if a.SHA256 == "" {
			continue
		}
		path := p.Files[a.Filename]
		sum, err := sha256File(path)
		if err != nil {
			return fmt.Errorf("models: verify %s: %w", a.Filename, err)
		}
		if !strings.EqualFold(sum, a.SHA256) {
			return fmt.Errorf("models: checksum mismatch for %s: got %s want %s", a.Filename, sum, a.SHA256)
		}
	}
	return nil
}

// fetchAsset downloads (and extracts/verifies) one asset into dir, skipping it
// if a correctly-sized copy is already present.
func (m *Manager) fetchAsset(ctx context.Context, dir string, a Asset, log Logf) error {
	dest := filepath.Join(dir, a.Filename)
	if a.Archive == "" && a.Size > 0 && fileBigger(dest, a.Size) {
		log("models: present: %s", a.Filename)
		return nil
	}
	if a.Archive == "" && a.Size <= 0 && fileBigger(dest, 1) {
		log("models: present: %s", a.Filename)
		return nil
	}
	if a.Archive != "" && exists(dest) {
		log("models: present: %s", a.Filename)
		return nil
	}

	switch a.Archive {
	case "":
		log("models: downloading %s …", a.Filename)
		if err := m.download(ctx, a.URL, dest, a.Filename); err != nil {
			return err
		}
		if a.SHA256 != "" {
			sum, err := sha256File(dest)
			if err != nil {
				return err
			}
			if !strings.EqualFold(sum, a.SHA256) {
				_ = os.Remove(dest)
				return fmt.Errorf("checksum mismatch: got %s want %s", sum, a.SHA256)
			}
		} else {
			log("models: WARNING %s has no pinned checksum (integrity unverified)", a.Filename)
		}
		log("models: -> %s", dest)
	case "tgz":
		tmp := dest + ".tgz"
		log("models: downloading %s …", a.Filename)
		if err := m.download(ctx, a.URL, tmp, a.Filename); err != nil {
			return err
		}
		defer os.Remove(tmp)
		if err := extractTgz(tmp, dir, func(name string) bool {
			return a.Extract == "" || strings.Contains(name, a.Extract)
		}); err != nil {
			return err
		}
		log("models: extracted -> %s", dir)
	case "tar.xz":
		log("models: downloading %s …", a.Filename)
		if err := installTarXz(ctx, m.client, a.URL, dest, a.Extract); err != nil {
			return err
		}
		log("models: -> %s", dest)
	default:
		return fmt.Errorf("unknown archive type %q", a.Archive)
	}
	return nil
}

// download streams url to dest atomically (.part rename), reporting progress.
func (m *Manager) download(ctx context.Context, url, dest, label string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var src io.Reader = resp.Body
	if m.progress != nil {
		src = &countingReader{r: resp.Body, total: resp.ContentLength, label: label, fn: m.progress}
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// countingReader reports cumulative bytes read through fn.
type countingReader struct {
	r        io.Reader
	total    int64
	read     int64
	label    string
	fn       ProgressFunc
	lastEmit int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	// Throttle callbacks to ~1 MiB granularity to avoid event spam.
	if c.fn != nil && (c.read-c.lastEmit >= 1<<20 || err != nil) {
		c.lastEmit = c.read
		c.fn(c.label, c.read, c.total)
	}
	return n, err
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func fileBigger(path string, min int64) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() >= min
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractTgz pulls regular files whose names match want from a .tgz into destDir
// (flattened to basename). Lifted from speaker/install.go.
func extractTgz(tgzPath, destDir string, want func(name string) bool) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || !want(hdr.Name) {
			continue
		}
		out := filepath.Join(destDir, filepath.Base(hdr.Name))
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, tr); err != nil {
			w.Close()
			return err
		}
		w.Close()
	}
}

// installTarXz downloads a .tar.xz and extracts a single member (by basename
// match in extract) to dest, relying on system tar for xz. Lifted from
// speaker/install.go's installFfmpeg.
func installTarXz(ctx context.Context, client *http.Client, url, dest, member string) error {
	tmp, err := os.CreateTemp("", "noto-dl-*.tar.xz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	tmp.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	out, err := os.Create(tmp.Name())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return err
	}
	out.Close()

	work, err := os.MkdirTemp("", "noto-xz-extract")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if o, err := exec.CommandContext(ctx, "tar", "xJf", tmp.Name(), "-C", work).CombinedOutput(); err != nil {
		return fmt.Errorf("tar xJf: %v: %s", err, o)
	}
	if member == "" {
		member = filepath.Base(dest)
	}
	var src string
	_ = filepath.Walk(work, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && filepath.Base(path) == member {
			src = path
		}
		return nil
	})
	if src == "" {
		return fmt.Errorf("%s not found in archive", member)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = io.Copy(w, in)
	return err
}
