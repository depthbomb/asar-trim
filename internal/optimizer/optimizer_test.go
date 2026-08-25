package optimizer

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	asar "github.com/depthbomb/go-asar"
)

func TestAnalyzeAndOptimizePreserveRuntimeAndUnpackedContent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "package.json"), []byte("{\n  \"name\": \"demo\",\n  \"main\": \"index.js\",\n  \"dependencies\": {\"runtime\": \"1\"},\n  \"devDependencies\": {\"tool\": \"1\"}\n}\n"))
	mustWrite(t, filepath.Join(source, "index.js"), []byte("console.log('ok')\n"))
	mustWrite(t, filepath.Join(source, "node_modules", "dep", ".editorconfig"), []byte("root=true\n"))
	mustWrite(t, filepath.Join(source, "node_modules", "dep", "code.js.map"), []byte(`{"version":3}`))
	mustWrite(t, filepath.Join(source, "native", "addon.node"), []byte{0, 1, 2, 3})
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{Unpack: []string{"native/addon.node"}}); err != nil {
		t.Fatal(err)
	}
	hint := filepath.Join(root, "startup.hint")
	if err := os.WriteFile(hint, []byte(": index.js\npackage.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := Options{Profile: ProfileSafe, MinifyJSON: true, PrunePackage: true, Verify: true, HintFile: hint}
	analysis, err := Analyze(archive, opts)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.EstimatedSavings <= 0 || analysis.UnpackedFiles != 1 || analysis.HintCoverage <= 0 {
		t.Fatalf("unexpected analysis: %+v", analysis)
	}

	output := filepath.Join(root, "optimized.asar")
	opts.Output = output
	report, err := Optimize(archive, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Optimized || report.Output != output {
		t.Fatalf("unexpected report: %+v", report)
	}
	optimized, err := asar.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer optimized.Close()
	if err := optimized.VerifyAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := optimized.Entry("node_modules/dep/.editorconfig", false); !os.IsNotExist(err) {
		t.Fatalf("removed file still exists: %v", err)
	}
	pkg, err := optimized.ReadFile("package.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(pkg) != `{"dependencies":{"runtime":"1"},"main":"index.js","name":"demo"}` {
		t.Fatalf("package metadata not conservatively transformed: %s", pkg)
	}
	entry, err := optimized.Entry("native/addon.node", false)
	if err != nil || !entry.Unpacked {
		t.Fatalf("unpacked status lost: %+v, %v", entry, err)
	}
	data, err := optimized.ReadFile("native/addon.node")
	if err != nil || string(data) != string([]byte{0, 1, 2, 3}) {
		t.Fatalf("unpacked content changed: %v, %v", data, err)
	}
}

func TestOptimizeInPlaceBackup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "index.js"), []byte("ok"))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Optimize(archive, Options{Profile: ProfileSafe, Backup: true, Verify: true}); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(archive + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != string(original) {
		t.Fatal("backup does not match original archive")
	}
	if _, err := Optimize(archive, Options{Profile: ProfileSafe, Backup: true, Verify: true}); err == nil {
		t.Fatal("second backup unexpectedly overwrote existing backup")
	}
}

func TestAnalyzeFindsAndRebuildDeduplicatesPayloads(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	payload := []byte("the same production asset")
	mustWrite(t, filepath.Join(source, "a.bin"), payload)
	mustWrite(t, filepath.Join(source, "b.bin"), payload)
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{DisableDeduplication: true}); err != nil {
		t.Fatal(err)
	}
	report, err := Analyze(archive, Options{Profile: ProfileSafe, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.DuplicateSavings != int64(len(payload)) {
		t.Fatalf("duplicate savings = %d, want %d", report.DuplicateSavings, len(payload))
	}
	output := filepath.Join(root, "optimized.asar")
	result, err := Optimize(archive, Options{Profile: ProfileSafe, Verify: true, Output: output})
	if err != nil {
		t.Fatal(err)
	}
	if result.ActualSavings < int64(len(payload)) {
		t.Fatalf("actual savings = %d, want at least %d", result.ActualSavings, len(payload))
	}
}

func TestOptimizeDangerouslyRemovesLicensesAndHeaders(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "LICENSE"), []byte("Permission is hereby granted"))
	mustWrite(t, filepath.Join(source, "index.js"), []byte("/*! Copyright 2026 Acme. Licensed under MIT. */\nconst value = 1;\n"))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "optimized.asar")
	report, err := Optimize(archive, Options{Profile: ProfileSafe, Verify: true, Output: output, DangerouslyRemoveLicenses: true, LicenseRemovalAcknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Optimized {
		t.Fatalf("unexpected report: %+v", report)
	}
	a, err := asar.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.Entry("LICENSE", false); !os.IsNotExist(err) {
		t.Fatalf("license survived: %v", err)
	}
	data, err := a.ReadFile("index.js")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "const value = 1;\n" {
		t.Fatalf("license header survived or code changed: %q", data)
	}
}

func TestOptimizeExternalizesMapsAndLicenses(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "LICENSE"), []byte("MIT license text\n"))
	mustWrite(t, filepath.Join(source, "dist", "app.js"), []byte("run();\n//# sourceMappingURL=app.js.map\n"))
	inlineMap := base64.StdEncoding.EncodeToString([]byte(`{"version":3,"sources":["inline.ts"],"sourcesContent":["inline()"],"mappings":"AAAA"}`))
	mustWrite(t, filepath.Join(source, "dist", "inline.js"), []byte("inline();\n//# sourceMappingURL=data:application/json;base64,"+inlineMap+"\n"))
	mustWrite(t, filepath.Join(source, "dist", "app.js.map"), []byte(`{"version":3,"sources":["src.ts"],"sourcesContent":["run()"],"mappings":"AAAA"}`))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "optimized.asar")
	maps := filepath.Join(root, "maps")
	licenses := filepath.Join(root, "THIRD-PARTY-LICENSES.txt")
	_, err := Optimize(archive, Options{Profile: ProfileSafe, Verify: true, Output: output, ExternalizeSourceMaps: maps, StripSourceMapSources: true, ExternalizeLicenses: licenses})
	if err != nil {
		t.Fatal(err)
	}
	mapData, err := os.ReadFile(filepath.Join(maps, "dist", "app.js.map"))
	if err != nil || strings.Contains(string(mapData), "sourcesContent") {
		t.Fatalf("external map = %s, %v", mapData, err)
	}
	inlineData, err := os.ReadFile(filepath.Join(maps, "dist", "inline.js.map"))
	if err != nil || strings.Contains(string(inlineData), "sourcesContent") {
		t.Fatalf("external inline map = %s, %v", inlineData, err)
	}
	licenseData, err := os.ReadFile(licenses)
	if err != nil || !strings.Contains(string(licenseData), "MIT license text") {
		t.Fatalf("external licenses = %s, %v", licenseData, err)
	}
	a, err := asar.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.Entry("LICENSE", false); !os.IsNotExist(err) {
		t.Fatal("license remains in archive")
	}
	if _, err := a.Entry("dist/app.js.map", false); !os.IsNotExist(err) {
		t.Fatal("source map remains in archive")
	}
	asset, err := a.ReadFile("dist/app.js")
	if err != nil || strings.Contains(string(asset), "sourceMappingURL") {
		t.Fatalf("source map directive remains: %q, %v", asset, err)
	}
	inlineAsset, err := a.ReadFile("dist/inline.js")
	if err != nil || strings.Contains(string(inlineAsset), "sourceMappingURL") {
		t.Fatalf("inline source map remains: %q, %v", inlineAsset, err)
	}
}

func TestOptimizePrunesExplicitTargetAndPackageLocale(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "package.json"), []byte(`{"dependencies":{"native":"1"}}`))
	mustWrite(t, filepath.Join(source, "node_modules", "native", "package.json"), []byte(`{"name":"native","version":"1","os":["linux"]}`))
	mustWrite(t, filepath.Join(source, "node_modules", "native", "index.js"), []byte("native"))
	mustWrite(t, filepath.Join(source, "node_modules", "dates", "package.json"), []byte(`{"name":"dates","version":"1"}`))
	mustWrite(t, filepath.Join(source, "node_modules", "dates", "locales", "en-US.json"), []byte(`{}`))
	mustWrite(t, filepath.Join(source, "node_modules", "dates", "locales", "fr.json"), []byte(`{}`))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "optimized.asar")
	_, err := Optimize(archive, Options{Profile: ProfileSafe, Verify: true, Output: output, TargetPlatform: "win32", TargetArch: "x64", PruneIncompatible: true, Locales: []string{"en-US"}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := asar.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, removed := range []string{"node_modules/native/index.js", "node_modules/dates/locales/fr.json"} {
		if _, err := a.Entry(removed, false); !os.IsNotExist(err) {
			t.Errorf("%s survived", removed)
		}
	}
	if _, err := a.Entry("node_modules/dates/locales/en-US.json", false); err != nil {
		t.Fatal("allowed locale was removed")
	}
}

func TestOptimizeResourcesPrunesChromiumLocalesTransactionally(t *testing.T) {
	root := t.TempDir()
	resources := filepath.Join(root, "resources")
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "index.js"), []byte("ok"))
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := asar.Create(source, filepath.Join(resources, "app.asar"), asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(resources, "locales", "en-US.pak"), []byte("english"))
	mustWrite(t, filepath.Join(resources, "locales", "fr.pak"), []byte("french"))
	_, err := Optimize(resources, Options{Profile: ProfileSafe, Verify: true, Locales: []string{"en-US"}, TrimElectronLocales: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(resources, "locales", "en-US.pak")); err != nil {
		t.Fatal("allowed Chromium locale was removed")
	}
	if _, err := os.Stat(filepath.Join(resources, "locales", "fr.pak")); !os.IsNotExist(err) {
		t.Fatal("unselected Chromium locale survived")
	}
}

func TestOptimizeSmartUnpacksNativePayload(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mustWrite(t, filepath.Join(source, "native", "addon.node"), []byte("native payload"))
	archive := filepath.Join(root, "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "optimized.asar")
	if _, err := Optimize(archive, Options{Profile: ProfileSafe, Verify: true, Output: output, SmartUnpack: true}); err != nil {
		t.Fatal(err)
	}
	a, err := asar.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	entry, err := a.Entry("native/addon.node", false)
	if err != nil || !entry.Unpacked {
		t.Fatalf("native payload was not unpacked: %+v, %v", entry, err)
	}
}

func mustWrite(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
