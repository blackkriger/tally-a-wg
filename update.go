package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	releaseAPI  = "https://api.github.com/repos/blackkriger/tally-a-wg/releases/latest"
	maxDownload = 64 << 20
)

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// parseVersion turns "v0.6.1" into comparable numbers; ok is false for anything else.
func parseVersion(s string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func newerThanRunning(tag string) bool {
	remote, ok := parseVersion(tag)
	if !ok {
		return false
	}
	local, ok := parseVersion(Version)
	if !ok {
		return false
	}
	for i := range remote {
		if remote[i] != local[i] {
			return remote[i] > local[i]
		}
	}
	return false
}

func fetch(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tallyawg/"+Version)
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, maxDownload))
}

func latestRelease() (*release, error) {
	body, err := fetch(releaseAPI)
	if err != nil {
		return nil, err
	}
	var r release
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	if r.Tag == "" {
		return nil, fmt.Errorf("release has no tag")
	}
	return &r, nil
}

// assetFor picks this platform's archive plus the checksum file published beside it.
func assetFor(r *release) (archiveURL, sumsURL, archiveName string, err error) {
	want := "_" + runtime.GOOS + "_" + runtime.GOARCH + "."
	for _, a := range r.Assets {
		switch {
		case a.Name == "SHA256SUMS":
			sumsURL = a.URL
		case strings.Contains(a.Name, want) && (strings.HasSuffix(a.Name, ".tar.gz") || strings.HasSuffix(a.Name, ".zip")):
			archiveURL, archiveName = a.URL, a.Name
		}
	}
	if archiveURL == "" {
		return "", "", "", fmt.Errorf("release %s has no build for %s/%s", r.Tag, runtime.GOOS, runtime.GOARCH)
	}
	if sumsURL == "" {
		return "", "", "", fmt.Errorf("release %s has no SHA256SUMS to verify against", r.Tag)
	}
	return archiveURL, sumsURL, archiveName, nil
}

// verifySum refuses anything whose digest is missing from or disagrees with SHA256SUMS.
func verifySum(archive []byte, sums []byte, name string) error {
	got := sha256.Sum256(archive)
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = f[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("%s is not listed in SHA256SUMS", name)
	}
	if !strings.EqualFold(want, hex.EncodeToString(got[:])) {
		return fmt.Errorf("checksum mismatch for %s: refusing to install it", name)
	}
	return nil
}

// binaryFromArchive returns the single executable the release ships.
func binaryFromArchive(name string, blob []byte) ([]byte, error) {
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() || strings.HasPrefix(filepath.Base(f.Name), ".") {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, maxDownload))
		}
		return nil, fmt.Errorf("%s holds no file", name)
	}

	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s holds no file", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

// selfUpdate downloads the newest release, checks it against SHA256SUMS and swaps the running binary.
func selfUpdate() (string, error) {
	rel, err := latestRelease()
	if err != nil {
		return "", err
	}
	if !newerThanRunning(rel.Tag) {
		return "", fmt.Errorf("%s is not newer than the running %s", rel.Tag, Version)
	}
	archiveURL, sumsURL, name, err := assetFor(rel)
	if err != nil {
		return "", err
	}
	archive, err := fetch(archiveURL)
	if err != nil {
		return "", err
	}
	sums, err := fetch(sumsURL)
	if err != nil {
		return "", err
	}
	if err := verifySum(archive, sums, name); err != nil {
		return "", err
	}
	bin, err := binaryFromArchive(name, archive)
	if err != nil {
		return "", err
	}

	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return "", err
	}
	tmp := self + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, self); err != nil { // rename over a running binary is fine on unix
		os.Remove(tmp)
		return "", err
	}
	return rel.Tag, nil
}

// checkCache keeps the GitHub lookup off the render path: the page asks on every refresh.
var checkCache struct {
	sync.Mutex
	tag string
	at  time.Time
	err error
}

const checkEvery = time.Hour

// latestTag reports the newest published tag, refreshed at most once an hour.
func latestTag() (string, error) {
	checkCache.Lock()
	defer checkCache.Unlock()
	if time.Since(checkCache.at) < checkEvery {
		return checkCache.tag, checkCache.err
	}
	rel, err := latestRelease()
	checkCache.at = time.Now()
	if err != nil {
		checkCache.err = err
		return "", err
	}
	checkCache.tag, checkCache.err = rel.Tag, nil
	return rel.Tag, nil
}

func runUpdate(args []string) {
	if err := requireRoot("update"); err != nil {
		fail(err)
	}
	rel, err := latestRelease()
	if err != nil {
		fail(err)
	}
	if !newerThanRunning(rel.Tag) {
		fmt.Printf("already on the newest release (%s)\n", Version)
		return
	}
	fmt.Printf(">> updating %s -> %s\n", Version, rel.Tag)
	tag, err := selfUpdate()
	if err != nil {
		fail(err)
	}
	fmt.Println(">> installed " + tag)
	if err := restartService(); err != nil {
		fmt.Println(">> restart the service by hand to finish:", err)
		return
	}
	fmt.Println(">> service restarted")
}
