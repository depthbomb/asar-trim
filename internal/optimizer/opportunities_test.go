package optimizer

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	asar "github.com/depthbomb/go-asar"
)

func TestSelectorAllows(t *testing.T) {
	tests := []struct {
		values []string
		target string
		want   bool
	}{
		{nil, "win32", true},
		{[]string{"linux", "darwin"}, "win32", false},
		{[]string{"linux", "win32"}, "win32", true},
		{[]string{"!win32"}, "win32", false},
		{[]string{"!darwin"}, "win32", true},
		{[]string{"win32", "!win32"}, "win32", false},
	}
	for _, test := range tests {
		if got := selectorAllows(test.values, test.target); got != test.want {
			t.Errorf("selectorAllows(%v, %q) = %v, want %v", test.values, test.target, got, test.want)
		}
	}
}

func TestAnalyzeOpportunities(t *testing.T) {
	source := t.TempDir()
	files := map[string]string{
		"package.json":                                        `{"name":"app","dependencies":{"a":"1","native":"1","missing":"1","shared":"1"},"optionalDependencies":{"opt":"1","optional-missing":"1"}}`,
		"node_modules/a/package.json":                         `{"name":"a","version":"1.0.0","dependencies":{"child":"1"}}`,
		"node_modules/a/index.js":                             `module.exports = 1`,
		"node_modules/a/node_modules/child/package.json":      `{"name":"child","version":"1.0.0"}`,
		"node_modules/a/node_modules/child/index.js":          `child`,
		"node_modules/native/package.json":                    `{"name":"native","version":"1.0.0","os":["linux"]}`,
		"node_modules/native/index.js":                        `native`,
		"node_modules/pre/prebuilds/linux-x64/addon.node":     `linux`,
		"node_modules/pre/prebuilds/win32-x64/addon.node":     `windows`,
		"node_modules/a/locales/fr.json":                      `{}`,
		"node_modules/a/locales/en-US.json":                   `{}`,
		"node_modules/a/.github/workflows/test.yml":           `ci`,
		"node_modules/extra/package.json":                     `{"name":"extra","version":"1.0.0"}`,
		"node_modules/extra/index.js":                         `extra`,
		"node_modules/opt/package.json":                       `{"name":"opt","version":"1.0.0","dependencies":{"shared":"1","optchild":"1"}}`,
		"node_modules/opt/index.js":                           `opt`,
		"node_modules/opt/node_modules/optchild/package.json": `{"name":"optchild","version":"1.0.0"}`,
		"node_modules/opt/node_modules/optchild/index.js":     `optchild`,
		"node_modules/shared/package.json":                    `{"name":"shared","version":"1.0.0"}`,
		"node_modules/shared/index.js":                        `shared`,
		"node_modules/dup/package.json":                       `{"name":"dup","version":"1.0.0"}`,
		"node_modules/dup/index.js":                           `same`,
		"node_modules/a/node_modules/dup/package.json":        `{"name":"dup","version":"1.0.0"}`,
		"node_modules/a/node_modules/dup/index.js":            `same`,
	}
	for name, data := range files {
		full := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "app.asar")
	if _, err := asar.Create(source, archive, asar.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	report, err := AnalyzeOpportunities(archive, OpportunityOptions{
		Target: Target{Platform: "win32", Arch: "x64"}, AuditDependencies: true,
		ScanJunk: true, PruneLocales: true, Locales: []string{"en-US"},
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := report.CandidatePaths()
	assertContains := func(fragment string) {
		t.Helper()
		if !slices.ContainsFunc(paths, func(name string) bool { return strings.Contains(name, fragment) }) {
			t.Errorf("candidate paths do not contain %q: %v", fragment, paths)
		}
	}
	assertContains("node_modules/native/index.js")
	assertContains("prebuilds/linux-x64/addon.node")
	assertContains("locales/fr.json")
	assertContains(".github/workflows/test.yml")
	if slices.Contains(paths, "node_modules/pre/prebuilds/win32-x64/addon.node") {
		t.Error("matching native prebuild was removed")
	}
	if slices.Contains(paths, "node_modules/a/locales/en-US.json") {
		t.Error("allowed locale was removed")
	}
	if !slices.Contains(report.MissingRuntime, ". -> missing") {
		t.Errorf("missing runtime dependencies = %v", report.MissingRuntime)
	}
	if slices.Contains(report.MissingRuntime, ". -> optional-missing") {
		t.Errorf("missing optional dependency reported as required: %v", report.MissingRuntime)
	}
	if !slices.ContainsFunc(report.PackageIssues, func(issue PackageIssue) bool { return issue.Root == "node_modules/extra" && issue.Kind == "extraneous" }) {
		t.Errorf("expected extraneous package issue: %+v", report.PackageIssues)
	}
	if len(report.DuplicatePackages) != 1 || report.DuplicatePackages[0].Name != "dup" || report.DuplicatePackages[0].RecoverableBytes <= 0 {
		t.Errorf("duplicate packages = %+v", report.DuplicatePackages)
	}

	pruned, err := AnalyzeOpportunities(archive, OpportunityOptions{AuditDependencies: true, PruneExtraneous: true, OmitOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(pruned.Candidates, func(candidate RemovalCandidate) bool {
		return candidate.Category == "extraneous-package" && candidate.Path == "node_modules/extra/index.js"
	}) {
		t.Errorf("extraneous package files were not planned: %+v", pruned.Candidates)
	}
	if !slices.ContainsFunc(pruned.Candidates, func(candidate RemovalCandidate) bool {
		return candidate.Category == "optional-package" && candidate.Path == "node_modules/opt/index.js"
	}) {
		t.Errorf("optional package files were not planned: %+v", pruned.Candidates)
	}
	if !slices.ContainsFunc(pruned.Candidates, func(candidate RemovalCandidate) bool {
		return candidate.Category == "optional-package" && candidate.Path == "node_modules/opt/node_modules/optchild/index.js"
	}) {
		t.Errorf("optional dependency subtree was not planned: %+v", pruned.Candidates)
	}
	if slices.ContainsFunc(pruned.Candidates, func(candidate RemovalCandidate) bool {
		return candidate.Category == "optional-package" && candidate.Path == "node_modules/shared/index.js"
	}) {
		t.Error("package also reachable through required dependencies was classified optional")
	}
}

func TestAnalyzeOpportunitiesRequiresLocaleAllowlist(t *testing.T) {
	_, err := AnalyzeOpportunities("unused.asar", OpportunityOptions{PruneLocales: true})
	if err == nil || !strings.Contains(err.Error(), "explicitly allowed locale") {
		t.Fatalf("got error %v", err)
	}
}

func TestPackageLocaleIsDependencyScoped(t *testing.T) {
	if _, ok := packageLocale("assets/locales/fr.json"); ok {
		t.Fatal("application locale should not be classified")
	}
	if got, ok := packageLocale("node_modules/moment/locale/fr.js"); !ok || got != "fr" {
		t.Fatalf("got %q, %v", got, ok)
	}
}

func TestDirectoriesMadeEmpty(t *testing.T) {
	entries := []asar.Entry{
		{Path: "node_modules", Mode: os.ModeDir},
		{Path: "node_modules/a", Mode: os.ModeDir},
		{Path: "node_modules/a/cache", Mode: os.ModeDir},
		{Path: "node_modules/a/cache/junk.o", Size: 1},
		{Path: "node_modules/a/index.js", Size: 1},
	}
	got := DirectoriesMadeEmpty(entries, []string{"node_modules/a/cache/junk.o"})
	if !slices.Equal(got, []string{"node_modules/a/cache"}) {
		t.Fatalf("empty directories = %v", got)
	}
}

func TestIncompatiblePrebuildLibc(t *testing.T) {
	if reason, ok := incompatiblePrebuild("node_modules/x/prebuilds/linux-x64/node.napi.musl.node", Target{Platform: "linux", Arch: "x64", Libc: "glibc"}); !ok || !strings.Contains(reason, "musl") {
		t.Fatalf("got %q, %v", reason, ok)
	}
	if _, ok := incompatiblePrebuild("node_modules/x/prebuilds/linux-x64/node.napi.glibc.node", Target{Platform: "linux", Arch: "x64", Libc: "glibc"}); ok {
		t.Fatal("matching libc classified as incompatible")
	}
}

func TestNormalizeTargetRejectsTypos(t *testing.T) {
	if _, err := normalizeTarget(Target{Platform: "windows"}); err == nil {
		t.Fatal("unknown target platform accepted")
	}
	got, err := normalizeTarget(Target{Platform: " WIN32 ", Arch: "X64", Libc: ""})
	if err != nil || got.Platform != "win32" || got.Arch != "x64" {
		t.Fatalf("normalized target = %+v, %v", got, err)
	}
}

func TestPlanResourceLocalePruning(t *testing.T) {
	resources := t.TempDir()
	localeDir := filepath.Join(resources, "locales")
	if err := os.Mkdir(localeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"en-US.pak", "fr.pak", "README.txt"} {
		if err := os.WriteFile(filepath.Join(localeDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := PlanResourceLocalePruning(resources, []string{"en"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Retained) != 1 || filepath.Base(plan.Retained[0]) != "en-US.pak" {
		t.Fatalf("retained = %v", plan.Retained)
	}
	if len(plan.Candidates) != 1 || filepath.Base(plan.Candidates[0].Path) != "fr.pak" {
		t.Fatalf("candidates = %+v", plan.Candidates)
	}
	if _, err := PlanResourceLocalePruning(resources, []string{"de"}); err == nil || !strings.Contains(err.Error(), "removes every locale") {
		t.Fatalf("expected all-locales guard, got %v", err)
	}
	if _, err := PlanResourceLocalePruning(resources, nil); err == nil {
		t.Fatal("empty locale allowlist accepted")
	}
}
