package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"testing"
)

func TestParseVersion(t *testing.T) {
	// lenient on purpose: a tag it cannot read would silently switch updates off
	for s, want := range map[string][3]int{
		"v0.6.1":       {0, 6, 1},
		"0.6.1":        {0, 6, 1},
		" v0.6.1 ":     {0, 6, 1},
		"V0.6.1":       {0, 6, 1},
		"v0.7.0-rc1":   {0, 7, 0},
		"v1.2.3+build": {1, 2, 3},
		"v1.2":         {1, 2, 0},
		"2":            {2, 0, 0},
	} {
		if got, ok := parseVersion(s); !ok || got != want {
			t.Errorf("parseVersion(%q) = %v, %v; want %v", s, got, ok, want)
		}
	}
	for _, s := range []string{"", "v1.2.3.4", "v1.2.x", "latest", "v-1.2.3"} {
		if _, ok := parseVersion(s); ok {
			t.Errorf("parseVersion(%q) should be rejected", s)
		}
	}
}

func TestNewerThanRunning(t *testing.T) {
	old := Version
	Version = "0.6.1"
	defer func() { Version = old }()

	for _, tag := range []string{"v0.6.2", "v0.7.0", "v1.0.0"} {
		if !newerThanRunning(tag) {
			t.Errorf("%s should count as newer than %s", tag, Version)
		}
	}
	// same, older or unparsable must never trigger an update
	for _, tag := range []string{"v0.6.1", "v0.6.0", "v0.5.9", "v0.0.1", "nightly", ""} {
		if newerThanRunning(tag) {
			t.Errorf("%s must not count as newer than %s", tag, Version)
		}
	}
}

func TestVerifySumAcceptsMatchingDigest(t *testing.T) {
	blob := []byte("release archive")
	sum := sha256.Sum256(blob)
	name := "tallyawg_0.6.1_linux_amd64.tar.gz"
	sums := []byte("deadbeef  other_file.zip\n" + hex.EncodeToString(sum[:]) + "  " + name + "\n")

	if err := verifySum(blob, sums, name); err != nil {
		t.Fatalf("matching digest rejected: %v", err)
	}
}

// The whole point of the check: a tampered download must not be installed.
func TestVerifySumRejectsTamperedArchive(t *testing.T) {
	sum := sha256.Sum256([]byte("the real archive"))
	name := "tallyawg_0.6.1_linux_amd64.tar.gz"
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")

	if err := verifySum([]byte("something else"), sums, name); err == nil {
		t.Fatal("a mismatching digest must be refused")
	}
}

func TestVerifySumRejectsUnlistedArchive(t *testing.T) {
	blob := []byte("x")
	if err := verifySum(blob, []byte("abc  other.tar.gz\n"), "tallyawg_0.6.1_linux_amd64.tar.gz"); err == nil {
		t.Fatal("an archive missing from SHA256SUMS must be refused")
	}
}

// GitHub's own sums files use a binary-mode marker; both spellings must parse.
func TestVerifySumAcceptsBinaryMarker(t *testing.T) {
	blob := []byte("release archive")
	sum := sha256.Sum256(blob)
	name := "tallyawg_0.6.1_linux_amd64.tar.gz"
	sums := []byte(hex.EncodeToString(sum[:]) + " *" + name + "\n")

	if err := verifySum(blob, sums, name); err != nil {
		t.Fatalf("binary-mode line rejected: %v", err)
	}
}

func tarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipOf(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	return buf.Bytes()
}

func TestBinaryFromArchive(t *testing.T) {
	body := []byte("\x7fELF fake binary")

	got, err := binaryFromArchive("tallyawg_0.6.1_linux_amd64.tar.gz", tarGz(t, "tallyawg_0.6.1_linux_amd64", body))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("tar.gz: got %q, err %v", got, err)
	}
	got, err = binaryFromArchive("tallyawg_0.6.1_darwin_arm64.zip", zipOf(t, "tallyawg_0.6.1_darwin_arm64", body))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("zip: got %q, err %v", got, err)
	}
}

func TestBinaryFromArchiveRejectsGarbage(t *testing.T) {
	if _, err := binaryFromArchive("x.tar.gz", []byte("not gzip")); err == nil {
		t.Fatal("garbage must not parse as an archive")
	}
}

func TestAssetForPicksThisPlatform(t *testing.T) {
	ext := ".tar.gz"
	if runtime.GOOS == "darwin" {
		ext = ".zip"
	}
	mine := "tallyawg_0.6.1_" + runtime.GOOS + "_" + runtime.GOARCH + ext
	r := &release{Tag: "v0.6.1"}
	r.Assets = append(r.Assets,
		struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{"tallyawg_0.6.1_plan9_mips.tar.gz", "http://x/wrong"},
		struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{mine, "http://x/right"},
		struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{"SHA256SUMS", "http://x/sums"},
	)

	archive, sums, name, err := assetFor(r)
	if err != nil {
		t.Fatal(err)
	}
	if archive != "http://x/right" || name != mine {
		t.Fatalf("picked %s (%s)", name, archive)
	}
	if sums != "http://x/sums" {
		t.Fatalf("sums url = %s", sums)
	}
}

// Without a checksum file there is nothing to verify against, so the update must not proceed.
func TestAssetForRequiresChecksums(t *testing.T) {
	mine := "tallyawg_0.6.1_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	r := &release{Tag: "v0.6.1"}
	r.Assets = append(r.Assets, struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}{mine, "http://x/right"})

	if _, _, _, err := assetFor(r); err == nil {
		t.Fatal("a release without SHA256SUMS must be refused")
	}
}

func TestAssetForRequiresThisPlatform(t *testing.T) {
	r := &release{Tag: "v0.6.1"}
	r.Assets = append(r.Assets, struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}{"SHA256SUMS", "http://x/sums"})

	if _, _, _, err := assetFor(r); err == nil {
		t.Fatal("a release with no build for this platform must be refused")
	}
}

// build.sh leaves the version out of the asset names, so the matcher must find them without it.
func TestAssetForVersionlessNames(t *testing.T) {
	mine := "tallyawg_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	r := &release{Tag: "v0.7.0"}
	add := func(name, url string) {
		r.Assets = append(r.Assets, struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{name, url})
	}
	add("tallyawg_openbsd_riscv64.tar.gz", "http://x/wrong")
	add(mine, "http://x/right")
	add("SHA256SUMS", "http://x/sums")

	archive, sums, name, err := assetFor(r)
	if err != nil {
		t.Fatal(err)
	}
	if archive != "http://x/right" || name != mine {
		t.Fatalf("picked %s (%s)", name, archive)
	}
	if sums != "http://x/sums" {
		t.Fatalf("sums url = %s", sums)
	}
}
