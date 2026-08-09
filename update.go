package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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

// no blanket timeout: it would cover the body too, and an archive needs longer than JSON.
var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

const (
	metaTimeout     = 60 * time.Second
	downloadTimeout = 10 * time.Minute // matches the write deadline the page holds open
)

// parseVersion stays lenient: a tag it cannot read switches updates off for everyone.
func parseVersion(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if i := strings.IndexAny(s, "-+"); i >= 0 { // drop a pre-release or build suffix
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
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

func fetch(url string) ([]byte, error) { return fetchWithin(url, metaTimeout) }

func fetchWithin(url string, d time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

// readExactly fails loudly past the cap: a truncated binary would only die on the next start.
func readExactly(r io.Reader, name string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxDownload {
		return nil, fmt.Errorf("the binary in %s is larger than %d bytes", name, maxDownload)
	}
	return b, nil
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
			return readExactly(rc, name)
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
			return readExactly(tr, name)
		}
	}
}

var updating sync.Mutex

// selfUpdate downloads the newest release, checks it against SHA256SUMS and swaps the running binary.
func selfUpdate() (string, error) {
	if !updating.TryLock() { // a second click must not race the first into the same temp file
		return "", fmt.Errorf("an update is already running")
	}
	defer updating.Unlock()

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
	archive, err := fetchWithin(archiveURL, downloadTimeout)
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
	if err := swapBinary(self, bin); err != nil {
		return "", err
	}
	return rel.Tag, nil
}

// swapBinary installs bin at self, keeping the old one until the new one proves it runs.
func swapBinary(self string, bin []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(self), ".tallyawg-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeded
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	// run the downloaded binary before trusting it: a truncated or mispackaged file dies here, not on the next boot
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, tmpName, "version").CombinedOutput(); err != nil {
		return fmt.Errorf("the downloaded binary does not run (%v): %s", err, strings.TrimSpace(string(out)))
	}

	backup := self + ".old"
	_ = os.Remove(backup)
	if err := os.Link(self, backup); err != nil && !os.IsNotExist(err) {
		backup = "" // no backup possible, carry on — the swap itself is still atomic
	}
	if err := os.Rename(tmpName, self); err != nil { // rename over a running binary is fine on unix
		return err
	}
	if backup != "" {
		_ = os.Remove(backup)
	}
	return nil
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
