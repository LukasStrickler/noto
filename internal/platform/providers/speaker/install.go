package speaker

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Downloadable assets for the local provider. These live in the data dir, never the repo.
const (
	ortVersion = "1.26.0"
	modelURL   = "https://huggingface.co/Wespeaker/wespeaker-voxceleb-ecapa-tdnn512/resolve/main/voxceleb_ECAPA512.onnx"
	ortURL     = "https://github.com/microsoft/onnxruntime/releases/download/v" + ortVersion + "/onnxruntime-linux-x64-" + ortVersion + ".tgz"
	ffmpegURL  = "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz"
)

// Paths locates the provider's assets under <dataDir>/voiceprint.
type Paths struct {
	Home   string
	Model  string // ecapa512.onnx
	Lib    string // libonnxruntime.so
	Ffmpeg string // static ffmpeg binary
}

// ResolvePaths returns the on-disk layout for a given noto data dir.
func ResolvePaths(dataDir string) Paths {
	home := filepath.Join(dataDir, "voiceprint")
	return Paths{
		Home:   home,
		Model:  filepath.Join(home, "ecapa512.onnx"),
		Lib:    filepath.Join(home, "lib", "libonnxruntime.so"),
		Ffmpeg: filepath.Join(home, "bin", "ffmpeg"),
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Available reports whether the model + ORT lib are installed (ffmpeg may fall back to PATH).
func Available(dataDir string) bool {
	p := ResolvePaths(dataDir)
	return exists(p.Model) && exists(p.Lib)
}

// FfmpegPath returns the installed static ffmpeg, or "ffmpeg" to use the one on PATH.
func (p Paths) FfmpegPath() string {
	if exists(p.Ffmpeg) {
		return p.Ffmpeg
	}
	return "ffmpeg"
}

// Open loads the local embedder from a data dir (model + lib must be installed).
func Open(dataDir string) (*LocalEmbedder, error) {
	p := ResolvePaths(dataDir)
	if !exists(p.Model) || !exists(p.Lib) {
		return nil, fmt.Errorf("speaker: model not installed in %s (run: noto speaker-model download)", p.Home)
	}
	eng, err := NewECAPA(p.Model, p.Lib)
	if err != nil {
		return nil, err
	}
	return NewLocalEmbedder(eng, p.FfmpegPath()), nil
}

// Logf is an optional progress sink for Download.
type Logf func(format string, args ...any)

// Download fetches the ECAPA model, the onnxruntime shared library, and a static ffmpeg
// into the data dir. Idempotent: present, non-empty files are skipped.
func Download(dataDir string, log Logf) error {
	if log == nil {
		log = func(string, ...any) {}
	}
	p := ResolvePaths(dataDir)
	for _, d := range []string{p.Home, filepath.Dir(p.Lib), filepath.Dir(p.Ffmpeg)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	// 1) model (raw .onnx)
	if fileBigger(p.Model, 1<<20) {
		log("model present: %s", p.Model)
	} else {
		log("downloading ECAPA model …")
		if err := download(modelURL, p.Model); err != nil {
			return fmt.Errorf("model: %w", err)
		}
		log("model -> %s", p.Model)
	}

	// 2) onnxruntime shared lib (extract lib/libonnxruntime.so* from the .tgz)
	if fileBigger(p.Lib, 1<<20) {
		log("onnxruntime present: %s", p.Lib)
	} else {
		log("downloading onnxruntime %s …", ortVersion)
		tgz := filepath.Join(p.Home, "ort.tgz")
		if err := download(ortURL, tgz); err != nil {
			return fmt.Errorf("onnxruntime: %w", err)
		}
		if err := extractTgz(tgz, filepath.Dir(p.Lib), func(name string) bool {
			return strings.Contains(name, "/lib/libonnxruntime.so")
		}); err != nil {
			return fmt.Errorf("onnxruntime extract: %w", err)
		}
		os.Remove(tgz)
		if err := linkSo(filepath.Dir(p.Lib)); err != nil {
			return err
		}
		log("onnxruntime -> %s", p.Lib)
	}

	// 3) static ffmpeg (tar.xz -> system tar; falls back to PATH ffmpeg if unavailable)
	if exists(p.Ffmpeg) {
		log("ffmpeg present: %s", p.Ffmpeg)
	} else if _, err := exec.LookPath("ffmpeg"); err == nil {
		log("ffmpeg found on PATH; skipping download")
	} else {
		log("downloading static ffmpeg …")
		if err := installFfmpeg(p.Ffmpeg); err != nil {
			log("ffmpeg download failed (%v); install ffmpeg manually for video/non-wav input", err)
		} else {
			log("ffmpeg -> %s", p.Ffmpeg)
		}
	}
	return nil
}

func fileBigger(path string, min int64) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() >= min
}

func download(url, dest string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
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
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmp, dest)
}

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
	defer gz.Close()
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

// linkSo makes sure libonnxruntime.so exists (the tgz ships the versioned name + symlinks,
// but tar extraction of symlinks is filtered out, so create the plain name if missing).
func linkSo(libDir string) error {
	plain := filepath.Join(libDir, "libonnxruntime.so")
	if exists(plain) {
		return nil
	}
	entries, _ := os.ReadDir(libDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "libonnxruntime.so.") {
			return os.Symlink(e.Name(), plain)
		}
	}
	return fmt.Errorf("libonnxruntime.so not found after extract")
}

func installFfmpeg(dest string) error {
	tmp, err := os.CreateTemp("", "ffmpeg-*.tar.xz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	tmp.Close()
	if err := download(ffmpegURL, tmp.Name()); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "ffmpeg-extract")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	// rely on system tar for xz; johnvansickle ships only .tar.xz
	if out, err := exec.Command("tar", "xJf", tmp.Name(), "-C", work).CombinedOutput(); err != nil {
		return fmt.Errorf("tar xJf: %v: %s", err, out)
	}
	var src string
	filepath.Walk(work, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && filepath.Base(path) == "ffmpeg" {
			src = path
		}
		return nil
	})
	if src == "" {
		return fmt.Errorf("ffmpeg binary not found in archive")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
