package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"

	"github.com/austinrr/knapsack/internal/selfupdate"
)

func binaryFile() string {
	if runtime.GOOS == "windows" {
		return "knapsack.exe"
	}
	return "knapsack"
}

// newBinary is what the fake release's binary contains.
func newBinary(version string) string { return "knapsack binary " + version }

// serveFakeKnapsack answers for knapsack's own releases on the fake
// GitHub: the latest release, with this platform's archive (unless
// noBuild) and another platform's, each with its digest. It reports
// whether the request was one of these.
func (s *sandbox) serveFakeKnapsack(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	latest, noBuild := s.latest, s.noBuild
	s.mu.Unlock()
	switch {
	case r.URL.Path == "/repos/austinrr/knapsack/releases/latest":
		if latest == "" {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return true
		}
		platforms := [][2]string{{"plan9", "386"}}
		if !noBuild {
			platforms = append(platforms, [2]string{runtime.GOOS, runtime.GOARCH})
		}
		var assets []map[string]string
		for _, p := range platforms {
			name := selfupdate.AssetName(latest, p[0], p[1])
			sum := sha256.Sum256(fakeArchive(latest, p[0]))
			assets = append(assets, map[string]string{
				"name":                 name,
				"browser_download_url": "http://" + r.Host + "/knapsack/" + name,
				"digest":               "sha256:" + hex.EncodeToString(sum[:]),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": latest, "assets": assets})
		return true
	case strings.HasPrefix(r.URL.Path, "/knapsack/"):
		goos := "plan9"
		if strings.Contains(r.URL.Path, "_"+runtime.GOOS+"_") {
			goos = runtime.GOOS
		}
		_, _ = w.Write(fakeArchive(latest, goos))
		return true
	}
	return false
}

// fakeArchive builds a release archive as goreleaser does: the binary at
// the root, next to LICENSE, as a zip for Windows and a tar.gz otherwise.
func fakeArchive(version, goos string) []byte {
	name := "knapsack"
	if goos == "windows" {
		name = "knapsack.exe"
	}
	files := map[string]string{"LICENSE": "MIT", name: newBinary(version)}
	var buf bytes.Buffer
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		for _, n := range []string{"LICENSE", name} {
			f, _ := zw.Create(n)
			_, _ = f.Write([]byte(files[n]))
		}
		_ = zw.Close()
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range []string{"LICENSE", name} {
		_ = tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(files[n])), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(files[n]))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}
