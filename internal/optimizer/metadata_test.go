package optimizer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundlerRecommendations(t *testing.T) {
	root := t.TempDir()
	meta := filepath.Join(root, "meta.json")
	data := []byte(`{"inputs":{"src/main.ts":{"bytes":1000}},"outputs":{"dist/main.js":{"bytes":300,"inputs":{"src/main.ts":{"bytesInOutput":250}}}}}`)
	if err := os.WriteFile(meta, data, 0o644); err != nil {
		t.Fatal(err)
	}
	recs, err := bundlerRecommendations([]string{meta}, map[string]int64{"src/main.ts": 1000, "dist/main.js": 300})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].EstimatedBytes != 700 || recs[1].EstimatedBytes != 1000 {
		t.Fatalf("recommendations = %+v", recs)
	}
}
