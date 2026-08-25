package optimizer

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	asar "github.com/depthbomb/go-asar"
)

func TestAnalyzeAccessLogsMergesFiltersAndPreservesOrder(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "main.js"), []byte("main"))
	mustWrite(t, filepath.Join(source, "later.js"), []byte("later"))
	mustWrite(t, filepath.Join(source, "unused.js"), []byte("unused"))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, "first.log")
	second := filepath.Join(root, "second.log")
	if err := os.WriteFile(first, []byte("C:\\Apps\\Demo\\resources\\app.asar\\main.js\nC:\\Apps\\Demo\\resources\\app.asar\\missing.js\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(": main.js\n: later.js\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := AnalyzeAccessLogs(archive, []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(report.Paths, []string{"main.js", "later.js"}) {
		t.Fatalf("paths = %v", report.Paths)
	}
	if !slices.Equal(report.Unknown, []string{"missing.js"}) {
		t.Fatalf("unknown = %v", report.Unknown)
	}
	if report.Coverage != 2.0/3.0 {
		t.Fatalf("coverage = %v", report.Coverage)
	}

	output := filepath.Join(root, "merged.hint")
	hintReport, trace, err := GenerateAccessLogHint(archive, []string{first, second}, output, false)
	if err != nil {
		t.Fatal(err)
	}
	if hintReport.Paths != 2 || len(trace.Unknown) != 1 {
		t.Fatalf("hint=%+v trace=%+v", hintReport, trace)
	}
	hint, err := asar.ReadHint(output)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hint.Paths, report.Paths) {
		t.Fatalf("written paths = %v", hint.Paths)
	}
	if _, _, err := GenerateAccessLogHint(archive, []string{first}, output, false); err == nil {
		t.Fatal("existing output unexpectedly overwritten")
	}
}

func TestAnalyzeAccessLogsRequiresInput(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "main.js"), []byte("main"))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := AnalyzeAccessLogs(archive, nil); err == nil {
		t.Fatal("missing logs accepted")
	}
}
