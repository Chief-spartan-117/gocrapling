package parser

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

// Signature is a structural fingerprint of an element, used to find it again
// after the page markup changes.
type Signature struct {
	Tag         string            `json:"tag"`
	Attrs       map[string]string `json:"attrs"`
	Text        string            `json:"text"`
	Path        []string          `json:"path"`
	ParentTag   string            `json:"parent_tag"`
	ParentAttrs map[string]string `json:"parent_attrs"`
	Siblings    []string          `json:"siblings"`
}

func signatureOf(n *html.Node) Signature {
	sig := Signature{Tag: n.Data, Attrs: attrMap(n)}
	var b strings.Builder
	collectText(n, &b)
	sig.Text = truncate(strings.Join(strings.Fields(b.String()), " "), 256)
	for p := n.Parent; p != nil && p.Type == html.ElementNode; p = p.Parent {
		sig.Path = append([]string{p.Data}, sig.Path...)
	}
	if p := n.Parent; p != nil && p.Type == html.ElementNode {
		sig.ParentTag = p.Data
		sig.ParentAttrs = attrMap(p)
		for c := p.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c != n {
				sig.Siblings = append(sig.Siblings, c.Data)
			}
		}
	}
	return sig
}

func attrMap(n *html.Node) map[string]string {
	m := make(map[string]string, len(n.Attr))
	for _, a := range n.Attr {
		m[a.Key] = a.Val
	}
	return m
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// similarity scores two signatures in [0,1].
func similarity(a, b Signature) float64 { return score(a, b, 3) }

// score weighs text by textWeight; structural lookups (FindSimilar) pass 0
// because sibling items carry different text.
func score(a, b Signature, textWeight float64) float64 {
	type feat struct{ w, v float64 }
	tag := 0.0
	if a.Tag == b.Tag {
		tag = 1
	}
	feats := []feat{
		{1, tag},
		{textWeight, textScore(a.Text, b.Text)},
		{1.5, attrScore(a.Attrs, b.Attrs)},
		{1.5, lcsRatio(a.Path, b.Path)},
		{1, parentScore(a, b)},
		{1, jaccard(a.Siblings, b.Siblings)},
	}
	var sum, wsum float64
	for _, f := range feats {
		sum += f.w * f.v
		wsum += f.w
	}
	return sum / wsum
}

func textScore(a, b string) float64 {
	if a == "" && b == "" {
		return 1
	}
	return dice(a, b)
}

// dice is the bigram Sørensen–Dice coefficient.
func dice(a, b string) float64 {
	if a == b {
		return 1
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 2 || len(rb) < 2 {
		return 0
	}
	grams := map[[2]rune]int{}
	for i := 0; i+1 < len(ra); i++ {
		grams[[2]rune{ra[i], ra[i+1]}]++
	}
	hit := 0
	for i := 0; i+1 < len(rb); i++ {
		g := [2]rune{rb[i], rb[i+1]}
		if grams[g] > 0 {
			grams[g]--
			hit++
		}
	}
	return 2 * float64(hit) / float64(len(ra)-1+len(rb)-1)
}

func attrScore(a, b map[string]string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var total float64
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			continue
		}
		if k == "class" {
			total += jaccard(strings.Fields(va), strings.Fields(vb))
		} else {
			total += dice(va, vb)
		}
	}
	denom := float64(len(a))
	if len(b) > len(a) {
		denom = float64(len(b))
	}
	return total / denom
}

func parentScore(a, b Signature) float64 {
	s := 0.0
	if a.ParentTag == b.ParentTag {
		s += 0.5
	}
	return s + 0.5*attrScore(a.ParentAttrs, b.ParentAttrs)
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	inter := 0
	union := len(set)
	seen := map[string]bool{}
	for _, x := range b {
		if seen[x] {
			continue
		}
		seen[x] = true
		if set[x] {
			inter++
		} else {
			union++
		}
	}
	return float64(inter) / float64(union)
}

func lcsRatio(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			switch {
			case a[i-1] == b[j-1]:
				dp[i][j] = dp[i-1][j-1] + 1
			case dp[i-1][j] >= dp[i][j-1]:
				dp[i][j] = dp[i-1][j]
			default:
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	m := len(a)
	if len(b) > m {
		m = len(b)
	}
	return float64(dp[len(a)][len(b)]) / float64(m)
}

// bestMatch scans the tree for the element most similar to sig.
func bestMatch(root *html.Node, sig Signature, skip map[*html.Node]bool) (*html.Node, float64) {
	var best *html.Node
	bestScore := 0.0
	walk(root, func(n *html.Node) {
		if n.Type != html.ElementNode || skip[n] {
			return
		}
		if v := similarity(sig, signatureOf(n)); v > bestScore {
			best, bestScore = n, v
		}
	})
	return best, bestScore
}

// ---- storage ----

// Storage persists element signatures between runs.
type Storage interface {
	Save(namespace, id string, sigs []Signature) error
	Load(namespace, id string) ([]Signature, error)
}

// MemoryStorage keeps signatures in process memory.
type MemoryStorage struct {
	mu sync.RWMutex
	m  map[string][]Signature
}

func NewMemoryStorage() *MemoryStorage { return &MemoryStorage{m: map[string][]Signature{}} }

func (s *MemoryStorage) Save(ns, id string, sigs []Signature) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[ns+"\x00"+id] = sigs
	return nil
}

func (s *MemoryStorage) Load(ns, id string) ([]Signature, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m[ns+"\x00"+id], nil
}

// FileStorage persists signatures in one JSON file. Safe for concurrent use
// within a process.
type FileStorage struct {
	path string
	mu   sync.Mutex
	data map[string][]Signature
}

func NewFileStorage(path string) (*FileStorage, error) {
	fs := &FileStorage{path: path, data: map[string][]Signature{}}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &fs.data); err != nil {
			return nil, err
		}
	}
	return fs, nil
}

func (s *FileStorage) Save(ns, id string, sigs []Signature) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[ns+"\x00"+id] = sigs
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *FileStorage) Load(ns, id string) ([]Signature, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[ns+"\x00"+id], nil
}
