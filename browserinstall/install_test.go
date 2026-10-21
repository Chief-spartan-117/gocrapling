package browserinstall

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractKeepsModesAndSymlinksAndBlocksTraversal(t *testing.T) {
	dir := t.TempDir()
	zpath := filepath.Join(dir, "t.zip")
	f, _ := os.Create(zpath)
	zw := zip.NewWriter(f)
	h := &zip.FileHeader{Name: "app/run.sh", Method: zip.Deflate}
	h.SetMode(0o755)
	w, _ := zw.CreateHeader(h)
	w.Write([]byte("#!/bin/sh\n"))
	lh := &zip.FileHeader{Name: "app/link"}
	lh.SetMode(os.ModeSymlink | 0o777)
	lw, _ := zw.CreateHeader(lh)
	lw.Write([]byte("run.sh"))
	zw.Close()
	f.Close()

	out := filepath.Join(dir, "out")
	if err := extract(zpath, out); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(out, "app", "run.sh"))
	if err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("exec bit lost: %v %v", st, err)
	}
	if tgt, err := os.Readlink(filepath.Join(out, "app", "link")); err != nil || tgt != "run.sh" {
		t.Fatalf("symlink: %q %v", tgt, err)
	}

	// zip-slip
	bad := filepath.Join(dir, "bad.zip")
	bf, _ := os.Create(bad)
	bz := zip.NewWriter(bf)
	bw, _ := bz.Create("../escape.txt")
	bw.Write([]byte("x"))
	bz.Close()
	bf.Close()
	if err := extract(bad, filepath.Join(dir, "out2")); err == nil {
		t.Fatal("path traversal must be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("file escaped the destination")
	}
}

func TestResolveLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network")
	}
	plat, err := Platform()
	if err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	v, url, err := resolve(ctx, plat)
	if err != nil || v == "" || url == "" {
		t.Fatalf("resolve: %q %q %v", v, url, err)
	}
	t.Logf("stable %s %s", v, url)
}
