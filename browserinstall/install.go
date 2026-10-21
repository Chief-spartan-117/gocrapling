// Package browserinstall downloads a pinned-to-stable Chrome for Testing
// build into the user cache so the stealth browser works on machines with
// no Chrome installed.
package browserinstall

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const versionsURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

type manifest struct {
	Channels map[string]struct {
		Version   string `json:"version"`
		Downloads struct {
			Chrome []struct {
				Platform string `json:"platform"`
				URL      string `json:"url"`
			} `json:"chrome"`
		} `json:"downloads"`
	} `json:"channels"`
}

// Platform returns the Chrome for Testing platform id for this machine.
func Platform() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "mac-arm64", nil
	case "darwin/amd64":
		return "mac-x64", nil
	case "linux/amd64":
		return "linux64", nil
	case "windows/amd64":
		return "win64", nil
	case "windows/386":
		return "win32", nil
	}
	return "", fmt.Errorf("browserinstall: no Chrome for Testing build for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// Dir is where managed browsers live: <user cache>/gocrapling/chrome.
func Dir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "gocrapling", "chrome"), nil
}

// binaryRel is the executable's path inside an extracted build.
func binaryRel(platform string) string {
	switch platform {
	case "mac-arm64", "mac-x64":
		return filepath.Join("chrome-"+platform, "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing")
	case "linux64":
		return filepath.Join("chrome-linux64", "chrome")
	default:
		return filepath.Join("chrome-"+platform, "chrome.exe")
	}
}

// Find returns the newest installed managed Chrome, or "" if none.
func Find() string {
	dir, err := Dir()
	if err != nil {
		return ""
	}
	plat, err := Platform()
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range entries { // directory names are versions; lexical order is good enough within one channel
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), binaryRel(plat))
		if st, err := os.Stat(p); err == nil && !st.IsDir() && e.Name() > best {
			best = e.Name()
		}
	}
	if best == "" {
		return ""
	}
	return filepath.Join(dir, best, binaryRel(plat))
}

// Install downloads the stable Chrome for Testing build for this platform
// unless that version is already present, and returns the executable path.
func Install(ctx context.Context, force bool, log io.Writer) (string, error) {
	if log == nil {
		log = io.Discard
	}
	plat, err := Platform()
	if err != nil {
		return "", err
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	version, url, err := resolve(ctx, plat)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(dir, version)
	bin := filepath.Join(dest, binaryRel(plat))
	if st, err := os.Stat(bin); err == nil && !st.IsDir() && !force {
		fmt.Fprintf(log, "Chrome %s already installed\n", version)
		return bin, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	fmt.Fprintf(log, "downloading Chrome %s (%s)\n", version, plat)
	zipPath, err := download(ctx, url, dir)
	if err != nil {
		return "", err
	}
	defer os.Remove(zipPath)

	tmp := dest + ".partial"
	_ = os.RemoveAll(tmp)
	if err := extract(zipPath, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	_ = os.RemoveAll(dest)
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("browserinstall: executable missing after extract: %w", err)
	}
	fmt.Fprintf(log, "installed %s\n", bin)
	return bin, nil
}

func resolve(ctx context.Context, plat string) (version, url string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionsURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("browserinstall: manifest status %d", resp.StatusCode)
	}
	var m manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&m); err != nil {
		return "", "", err
	}
	ch, ok := m.Channels["Stable"]
	if !ok {
		return "", "", errors.New("browserinstall: no Stable channel in manifest")
	}
	for _, d := range ch.Downloads.Chrome {
		if d.Platform == plat {
			return ch.Version, d.URL, nil
		}
	}
	return "", "", fmt.Errorf("browserinstall: no %s download in manifest", plat)
}

// Limits guard against a hostile or corrupted archive.
const (
	maxDownload  = 1 << 30 // 1 GiB compressed
	maxExtracted = 3 << 30 // 3 GiB uncompressed
	maxEntries   = 20000   // files in the archive
)

// downloadHosts are the only hosts Chrome for Testing is served from.
var downloadHosts = map[string]bool{
	"storage.googleapis.com": true,
	"edgedl.me.gvt1.com":     true,
}

// checkDownloadURL insists on HTTPS from a known Google host, so a tampered
// manifest cannot point the installer at an arbitrary server.
func checkDownloadURL(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || !downloadHosts[strings.ToLower(u.Hostname())] {
		return fmt.Errorf("browserinstall: refusing download from %q", raw)
	}
	return nil
}

func download(ctx context.Context, url, dir string) (string, error) {
	if err := checkDownloadURL(url); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 20 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("browserinstall: download status %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(dir, "chrome-*.zip")
	if err != nil {
		return "", err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownload+1))
	if err == nil && n > maxDownload {
		err = fmt.Errorf("browserinstall: download exceeds %d bytes", int64(maxDownload))
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// extract unzips src into dest, keeping file modes and symlinks (macOS app
// bundles depend on both) and rejecting paths that escape dest.
func extract(src, dest string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if len(zr.File) > maxEntries {
		return fmt.Errorf("browserinstall: archive has %d entries", len(zr.File))
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > maxExtracted {
			return fmt.Errorf("browserinstall: archive expands beyond %d bytes", int64(maxExtracted))
		}
		target := filepath.Join(root, f.Name)
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return fmt.Errorf("browserinstall: unsafe path %q in archive", f.Name)
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case mode&os.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return err
			}
			link, err := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			// A link that points outside root would let a later entry
			// write through it to anywhere on disk.
			dest := string(link)
			if !filepath.IsAbs(dest) {
				dest = filepath.Join(filepath.Dir(target), dest)
			}
			if rel, rerr := filepath.Rel(root, filepath.Clean(dest)); rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("browserinstall: symlink %q escapes the archive", f.Name)
			}
			if err := os.Symlink(string(link), target); err != nil {
				return err
			}
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFile(f, target, mode.Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeFile(f *zip.File, target string, perm os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
