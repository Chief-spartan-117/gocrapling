package service

import (
	"strings"
	"testing"
)

func TestStatePathRejectsTraversal(t *testing.T) {
	s := New(Options{StateDir: t.TempDir()})
	defer s.Close()
	for _, bad := range []string{"../x", "a/b", "..", `a\b`, ""} {
		if _, err := s.statePath(bad); err == nil {
			t.Fatalf("statePath accepted %q", bad)
		}
	}
	if p, err := s.statePath("login"); err != nil || !strings.HasSuffix(p, "login.json") {
		t.Fatalf("%q %v", p, err)
	}
}

func TestCutTruncates(t *testing.T) {
	s := New(Options{DefaultMaxChars: 5})
	defer s.Close()
	if got, cut := s.cut("héllo wörld", 0); got != "héllo" || !cut {
		t.Fatalf("%q %v", got, cut)
	}
	if got, cut := s.cut("short", 0); got != "short" || cut {
		t.Fatalf("%q %v", got, cut)
	}
}
