package spider

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Chief-spartan-117/gocrapling/pkg/parser"
)

func markdownOpts(c ContentConfig) parser.MarkdownOptions {
	return parser.MarkdownOptions{MainOnly: c.MainOnly, Safe: c.Safe}
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// markdownPath maps a page URL to a file under dir:
// https://h.test/a/b?x=1 -> dir/h.test/a/b__x-1.md. The mapping is
// injective enough for crawls (query becomes part of the name) and cannot
// escape dir.
func markdownPath(dir, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	p := strings.Trim(u.EscapedPath(), "/")
	if p == "" {
		p = "index"
	}
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		seg = unsafeFileChars.ReplaceAllString(seg, "-")
		seg = strings.Trim(seg, ".")
		if seg == "" {
			seg = "_"
		}
		parts[i] = seg
	}
	name := strings.Join(parts, string(os.PathSeparator))
	if u.RawQuery != "" {
		name += "__" + strings.Trim(unsafeFileChars.ReplaceAllString(u.RawQuery, "-"), "-")
	}
	host := unsafeFileChars.ReplaceAllString(u.Host, "_")
	return filepath.Join(dir, host, name+".md"), nil
}

func writeFileAll(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
