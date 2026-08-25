//go:build windows

package optimizer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	asar "github.com/depthbomb/go-asar"
)

func TestCompressWindowsArchivePreservesContents(t *testing.T) {
	name := filepath.Join(t.TempDir(), "archive.asar")
	want := bytes.Repeat([]byte("compressible archive payload\n"), 8192)
	if err := os.WriteFile(name, want, 0o644); err != nil {
		t.Fatal(err)
	}
	method, err := compressWindowsArchive(name)
	if err != nil {
		t.Skipf("test filesystem does not support WOF or NTFS compression: %v", err)
	}
	if method != "wof-lzx" && method != "ntfs" {
		t.Fatalf("method = %q", method)
	}
	got, err := os.ReadFile(name)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("contents changed: %v", err)
	}
}

func TestOptimizeWithWindowsCompression(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "index.js"), bytes.Repeat([]byte("const value = 'compressible';\n"), 4096))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	report, err := Optimize(archive, Options{Profile: ProfileSafe, Verify: true, WindowsCompress: true})
	if err != nil {
		t.Skipf("test filesystem does not support WOF or NTFS compression: %v", err)
	}
	if report.WindowsCompression == "" {
		t.Fatal("compression method was not reported")
	}
	a, err := asar.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.VerifyAll(); err != nil {
		t.Fatalf("compressed archive integrity failed: %v", err)
	}
}
