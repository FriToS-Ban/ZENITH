//go:build ignore

// Run via: go generate ./internal/localembedder/...
// Downloads the ONNX embedding model and platform-specific onnxruntime shared
// library into internal/localembedder/assets/. These files are gitignored and
// must be present before go build.

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
)

const (
	ortVersion = "1.25.0"
	modelURL   = "https://huggingface.co/Xenova/all-MiniLM-L6-v2/resolve/main/onnx/model_quantized.onnx"
)

// assetsDir resolves to internal/localembedder/assets/ relative to the
// repository root, regardless of which directory go generate is called from.
func assetsPath() string {
	_, file, _, _ := goruntime.Caller(0)
	// file = .../ZENITH/scripts/download_assets.go
	// go up one level from scripts/ to reach the repo root
	root := filepath.Dir(filepath.Dir(file))
	return filepath.Join(root, "internal", "localembedder", "assets")
}

type ortRelease struct {
	url      string
	archive  string // "zip" or "tgz"
	libInZip string // path of the .dll/.so/.dylib inside the archive
	outName  string // name to save to assets/
}

var ortReleases = map[string]ortRelease{
	"windows/amd64": {
		url:      fmt.Sprintf("https://github.com/microsoft/onnxruntime/releases/download/v%s/onnxruntime-win-x64-%s.zip", ortVersion, ortVersion),
		archive:  "zip",
		libInZip: fmt.Sprintf("onnxruntime-win-x64-%s/lib/onnxruntime.dll", ortVersion),
		outName:  "onnxruntime.dll",
	},
	"linux/amd64": {
		url:      fmt.Sprintf("https://github.com/microsoft/onnxruntime/releases/download/v%s/onnxruntime-linux-x64-%s.tgz", ortVersion, ortVersion),
		archive:  "tgz",
		libInZip: fmt.Sprintf("onnxruntime-linux-x64-%s/lib/libonnxruntime.so.%s", ortVersion, ortVersion),
		outName:  "libonnxruntime.so",
	},
	"darwin/arm64": {
		url:      fmt.Sprintf("https://github.com/microsoft/onnxruntime/releases/download/v%s/onnxruntime-osx-arm64-%s.tgz", ortVersion, ortVersion),
		archive:  "tgz",
		libInZip: fmt.Sprintf("onnxruntime-osx-arm64-%s/lib/libonnxruntime.%s.dylib", ortVersion, ortVersion),
		outName:  "libonnxruntime.dylib",
	},
	"darwin/amd64": {
		url:      fmt.Sprintf("https://github.com/microsoft/onnxruntime/releases/download/v%s/onnxruntime-osx-x86_64-%s.tgz", ortVersion, ortVersion),
		archive:  "tgz",
		libInZip: fmt.Sprintf("onnxruntime-osx-x86_64-%s/lib/libonnxruntime.%s.dylib", ortVersion, ortVersion),
		outName:  "libonnxruntime.dylib",
	},
}

func main() {
	platform := goruntime.GOOS + "/" + goruntime.GOARCH
	rel, ok := ortReleases[platform]
	if !ok {
		fmt.Fprintf(os.Stderr, "unsupported platform: %s\n", platform)
		os.Exit(1)
	}

	assetsDir := assetsPath()
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("Downloading ONNX model from HuggingFace...\n")
	if err := downloadFile(modelURL, filepath.Join(assetsDir, "model.onnx")); err != nil {
		fmt.Fprintf(os.Stderr, "model download failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  model.onnx saved\n")

	fmt.Printf("Downloading onnxruntime %s for %s...\n", ortVersion, platform)
	archiveData, err := fetchBytes(rel.url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ort download failed: %v\n", err)
		os.Exit(1)
	}

	outPath := filepath.Join(assetsDir, rel.outName)
	switch rel.archive {
	case "zip":
		err = extractFromZip(archiveData, rel.libInZip, outPath)
	case "tgz":
		err = extractFromTGZ(archiveData, rel.libInZip, outPath)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ort extract failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  %s saved\n", rel.outName)
}

func downloadFile(url, dest string) error {
	data, err := fetchBytes(url)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}

func fetchBytes(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func extractFromZip(data []byte, target, dest string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range r.File {
		if f.Name == target {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = io.Copy(out, rc)
			return err
		}
	}
	return fmt.Errorf("file %q not found in zip", target)
}

func extractFromTGZ(data []byte, target, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if strings.TrimPrefix(hdr.Name, "./") == target || hdr.Name == target {
			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = io.Copy(out, tr)
			return err
		}
	}
	return fmt.Errorf("file %q not found in tgz", target)
}
