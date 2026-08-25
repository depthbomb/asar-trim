package optimizer

import (
	"os"
	"path/filepath"
	"testing"

	asar "github.com/depthbomb/go-asar"
)

func TestGeneratedHintIsNotPublishedWhenOptimizationFails(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "package.json"), []byte(`{"main":"index.js"}`))
	mustWrite(t, filepath.Join(source, "index.js"), []byte("console.log('ok')"))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	badMetadata := filepath.Join(root, "bad-meta.json")
	mustWrite(t, badMetadata, []byte("not JSON"))
	hint := filepath.Join(root, "startup.hint")
	output := filepath.Join(root, "optimized.asar")
	_, err := Optimize(archive, Options{Profile: ProfileSafe, Output: output, GenerateHint: hint, BundlerMetadata: []string{badMetadata}})
	if err == nil {
		t.Fatal("optimization unexpectedly succeeded")
	}
	if _, statErr := os.Stat(hint); !os.IsNotExist(statErr) {
		t.Fatalf("generated hint was published after failure: %v", statErr)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("output was published after failure: %v", statErr)
	}
}

func TestForcedGeneratedHintPreservesExistingFileWhenOptimizationFails(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "package.json"), []byte(`{"main":"index.js"}`))
	mustWrite(t, filepath.Join(source, "index.js"), []byte("console.log('ok')"))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	badMetadata := filepath.Join(root, "bad-meta.json")
	mustWrite(t, badMetadata, []byte("not JSON"))
	hint := filepath.Join(root, "startup.hint")
	mustWrite(t, hint, []byte("existing hint"))
	_, err := Optimize(archive, Options{Profile: ProfileSafe, Output: filepath.Join(root, "optimized.asar"), GenerateHint: hint, ForceHint: true, BundlerMetadata: []string{badMetadata}})
	if err == nil {
		t.Fatal("optimization unexpectedly succeeded")
	}
	data, readErr := os.ReadFile(hint)
	if readErr != nil || string(data) != "existing hint" {
		t.Fatalf("existing hint = %q, %v", data, readErr)
	}
}
