package optimizer

// This file contains opt-in, evidence-based optimization analysis. It is kept
// separate from the default policy so that merely supplying host information
// can never make an archive less portable.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	asar "github.com/depthbomb/go-asar"
)

type Target struct {
	Platform string `json:"platform,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Libc     string `json:"libc,omitempty"`
}

type OpportunityOptions struct {
	Target            Target   `json:"target"`
	AuditDependencies bool     `json:"audit_dependencies"`
	PruneExtraneous   bool     `json:"prune_extraneous"`
	OmitOptional      bool     `json:"omit_optional"`
	ScanJunk          bool     `json:"scan_junk"`
	PruneLocales      bool     `json:"prune_locales"`
	Locales           []string `json:"locales,omitempty"`
}

type RemovalCandidate struct {
	Path     string `json:"path"`
	Category string `json:"category"`
	Reason   string `json:"reason"`
	Risk     string `json:"risk"`
	Bytes    int64  `json:"bytes"`
}

type PackageIssue struct {
	Root    string `json:"root"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail"`
	Bytes   int64  `json:"bytes,omitempty"`
}

type DuplicatePackageGroup struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	Roots            []string `json:"roots"`
	ContentHash      string   `json:"content_sha256"`
	TotalBytes       int64    `json:"total_bytes"`
	RecoverableBytes int64    `json:"recoverable_bytes"`
}

type OpportunityReport struct {
	Candidates        []RemovalCandidate      `json:"candidates,omitempty"`
	PackageIssues     []PackageIssue          `json:"package_issues,omitempty"`
	DuplicatePackages []DuplicatePackageGroup `json:"duplicate_packages,omitempty"`
	MissingRuntime    []string                `json:"missing_runtime_dependencies,omitempty"`
	Warnings          []string                `json:"warnings,omitempty"`
}

type ResourceLocalePlan struct {
	ResourcesDir string             `json:"resources_directory"`
	Allowed      []string           `json:"allowed"`
	Retained     []string           `json:"retained"`
	Candidates   []RemovalCandidate `json:"candidates,omitempty"`
}

// CandidatePaths returns individual archive files that a caller may feed into
// an explicit removal policy. Directory candidates are deliberately expanded.
func (r OpportunityReport) CandidatePaths() []string {
	out := make([]string, 0, len(r.Candidates))
	for _, candidate := range r.Candidates {
		out = append(out, candidate.Path)
	}
	return out
}

// DirectoriesMadeEmpty returns archive directories that contain a planned
// removal but no surviving file or link. Callers may remove these deepest-first
// after applying CandidatePaths; directory entries do not contribute byte
// savings themselves.
func DirectoriesMadeEmpty(entries []asar.Entry, removalPaths []string) []string {
	removed := make(map[string]bool, len(removalPaths))
	for _, name := range removalPaths {
		removed[path.Clean(strings.ReplaceAll(name, "\\", "/"))] = true
	}
	var out []string
	for _, dir := range entries {
		if !dir.IsDir() {
			continue
		}
		hadRemoval, hasSurvivor := false, false
		for _, entry := range entries {
			if entry.IsDir() || !(strings.HasPrefix(entry.Path, dir.Path+"/")) {
				continue
			}
			if removed[entry.Path] {
				hadRemoval = true
			} else {
				hasSurvivor = true
				break
			}
		}
		if hadRemoval && !hasSurvivor {
			out = append(out, dir.Path)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		if depth := strings.Count(b, "/") - strings.Count(a, "/"); depth != 0 {
			return depth
		}
		return strings.Compare(a, b)
	})
	return out
}

type packageManifest struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	OS                   []string          `json:"os"`
	CPU                  []string          `json:"cpu"`
	Libc                 []string          `json:"libc"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

type installedPackage struct {
	root     string
	manifest packageManifest
	files    []asar.Entry
	size     int64
}

// AnalyzeOpportunities inspects an archive for target-specific, package-tree,
// locale, and dependency junk opportunities. It never modifies the archive.
func AnalyzeOpportunities(archivePath string, opts OpportunityOptions) (OpportunityReport, error) {
	if opts.PruneLocales && len(opts.Locales) == 0 {
		return OpportunityReport{}, errors.New("locale pruning requires at least one explicitly allowed locale")
	}
	var err error
	if opts.Target, err = normalizeTarget(opts.Target); err != nil {
		return OpportunityReport{}, err
	}
	a, err := asar.Open(archivePath)
	if err != nil {
		return OpportunityReport{}, err
	}
	defer a.Close()

	entries := a.Entries()
	packages, rootManifest, rootFound, warnings := collectPackages(a, entries)
	report := OpportunityReport{Warnings: warnings}
	seen := map[string]bool{}
	addTree := func(root, category, reason, risk string) {
		for _, entry := range entries {
			if entry.IsDir() || entry.Link != "" || !(entry.Path == root || strings.HasPrefix(entry.Path, root+"/")) || seen[entry.Path] {
				continue
			}
			seen[entry.Path] = true
			report.Candidates = append(report.Candidates, RemovalCandidate{Path: entry.Path, Category: category, Reason: reason, Risk: risk, Bytes: entry.Size})
		}
	}

	if opts.Target.Platform != "" || opts.Target.Arch != "" || opts.Target.Libc != "" {
		for _, pkg := range packages {
			if field, values, target, incompatible := incompatiblePackage(pkg.manifest, opts.Target); incompatible {
				reason := fmt.Sprintf("package %s@%s excludes target %s=%s (%s=%s)", pkg.manifest.Name, pkg.manifest.Version, field, target, field, strings.Join(values, ","))
				report.PackageIssues = append(report.PackageIssues, PackageIssue{Root: pkg.root, Name: pkg.manifest.Name, Version: pkg.manifest.Version, Kind: "incompatible-target", Detail: reason, Bytes: pkg.size})
				addTree(pkg.root, "incompatible-package", reason, "low")
			}
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Link != "" || seen[entry.Path] {
				continue
			}
			if reason, ok := incompatiblePrebuild(entry.Path, opts.Target); ok {
				seen[entry.Path] = true
				report.Candidates = append(report.Candidates, RemovalCandidate{Path: entry.Path, Category: "foreign-native-prebuild", Reason: reason, Risk: "low", Bytes: entry.Size})
			}
		}
	}

	if opts.PruneLocales {
		allowed := normalizeLocales(opts.Locales)
		for _, entry := range entries {
			if entry.IsDir() || entry.Link != "" || seen[entry.Path] {
				continue
			}
			if locale, ok := packageLocale(entry.Path); ok && !localeAllowed(locale, allowed) {
				seen[entry.Path] = true
				report.Candidates = append(report.Candidates, RemovalCandidate{Path: entry.Path, Category: "locale", Reason: fmt.Sprintf("locale %s is not in the explicit allowlist", locale), Risk: "medium", Bytes: entry.Size})
			}
		}
	}

	if opts.ScanJunk {
		for _, entry := range entries {
			if entry.IsDir() || entry.Link != "" || seen[entry.Path] || !strings.Contains(entry.Path, "node_modules/") {
				continue
			}
			if reason, ok := dependencyJunk(entry.Path); ok {
				seen[entry.Path] = true
				report.Candidates = append(report.Candidates, RemovalCandidate{Path: entry.Path, Category: "dependency-junk", Reason: reason, Risk: "low", Bytes: entry.Size})
			}
		}
	}

	if opts.AuditDependencies || opts.PruneExtraneous || opts.OmitOptional {
		if !rootFound {
			report.Warnings = append(report.Warnings, "dependency audit skipped: root package.json was not found or could not be parsed")
		} else {
			issues, missing, reachability := auditDependencyTree(packages, rootManifest)
			report.PackageIssues = append(report.PackageIssues, issues...)
			report.MissingRuntime = missing
			if opts.PruneExtraneous {
				for _, issue := range issues {
					if issue.Kind == "extraneous" {
						addTree(issue.Root, "extraneous-package", issue.Detail, "medium")
					}
				}
			}
			if opts.OmitOptional {
				for _, pkg := range packages {
					if reachability[pkg.root] == reachableOptional {
						reason := fmt.Sprintf("package %s@%s is reachable only through optional dependencies", pkg.manifest.Name, pkg.manifest.Version)
						addTree(pkg.root, "optional-package", reason, "high")
					}
				}
			}
		}
	}
	report.DuplicatePackages = duplicatePackages(a, packages, &report.Warnings)
	slices.SortFunc(report.Candidates, func(a, b RemovalCandidate) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(report.PackageIssues, func(a, b PackageIssue) int { return strings.Compare(a.Root+a.Kind, b.Root+b.Kind) })
	slices.Sort(report.MissingRuntime)
	return report, nil
}

// PlanResourceLocalePruning identifies Chromium locale .pak files outside an
// ASAR. It is read-only and refuses a plan that would retain no locale.
func PlanResourceLocalePruning(resourcesDir string, locales []string) (ResourceLocalePlan, error) {
	if len(locales) == 0 {
		return ResourceLocalePlan{}, errors.New("resource locale pruning requires at least one explicitly allowed locale")
	}
	abs, err := filepath.Abs(resourcesDir)
	if err != nil {
		return ResourceLocalePlan{}, err
	}
	localeDir := filepath.Join(abs, "locales")
	entries, err := os.ReadDir(localeDir)
	if err != nil {
		return ResourceLocalePlan{}, fmt.Errorf("read Chromium locales: %w", err)
	}
	allowed := normalizeLocales(locales)
	plan := ResourceLocalePlan{ResourcesDir: abs, Allowed: slices.Clone(locales)}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(entry.Name()), ".pak") {
			continue
		}
		locale := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		full := filepath.Join(localeDir, entry.Name())
		info, infoErr := entry.Info()
		if infoErr != nil {
			return ResourceLocalePlan{}, fmt.Errorf("stat Chromium locale %s: %w", entry.Name(), infoErr)
		}
		if localeAllowed(locale, allowed) {
			plan.Retained = append(plan.Retained, full)
		} else {
			plan.Candidates = append(plan.Candidates, RemovalCandidate{Path: full, Category: "chromium-locale", Reason: fmt.Sprintf("Chromium locale %s is not in the explicit allowlist", locale), Risk: "low", Bytes: info.Size()})
		}
	}
	if len(plan.Retained) == 0 {
		return ResourceLocalePlan{}, errors.New("locale allowlist matches no Chromium .pak file; refusing to produce a plan that removes every locale")
	}
	slices.Sort(plan.Allowed)
	slices.Sort(plan.Retained)
	slices.SortFunc(plan.Candidates, func(a, b RemovalCandidate) int { return strings.Compare(a.Path, b.Path) })
	return plan, nil
}

func normalizeTarget(target Target) (Target, error) {
	target.Platform = strings.ToLower(strings.TrimSpace(target.Platform))
	target.Arch = strings.ToLower(strings.TrimSpace(target.Arch))
	target.Libc = strings.ToLower(strings.TrimSpace(target.Libc))
	valid := func(value string, allowed []string, label string) error {
		if value != "" && !slices.Contains(allowed, value) {
			return fmt.Errorf("unknown target %s %q", label, value)
		}
		return nil
	}
	if err := valid(target.Platform, []string{"aix", "android", "darwin", "freebsd", "linux", "openbsd", "sunos", "win32"}, "platform"); err != nil {
		return Target{}, err
	}
	if err := valid(target.Arch, []string{"arm", "arm64", "ia32", "loong64", "mips", "mipsel", "ppc", "ppc64", "riscv64", "s390", "s390x", "x64"}, "architecture"); err != nil {
		return Target{}, err
	}
	if err := valid(target.Libc, []string{"glibc", "musl"}, "libc"); err != nil {
		return Target{}, err
	}
	return target, nil
}

func collectPackages(a *asar.Archive, entries []asar.Entry) ([]installedPackage, packageManifest, bool, []string) {
	var packages []installedPackage
	var root packageManifest
	rootFound := false
	var warnings []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Link != "" || path.Base(entry.Path) != "package.json" {
			continue
		}
		data, err := a.ReadFile(entry.Path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("read %s: %v", entry.Path, err))
			continue
		}
		var manifest packageManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			warnings = append(warnings, fmt.Sprintf("parse %s: %v", entry.Path, err))
			continue
		}
		rootPath := path.Dir(entry.Path)
		if rootPath == "." {
			root = manifest
			rootFound = true
			continue
		}
		if !isInstalledPackageRoot(rootPath) {
			continue
		}
		packages = append(packages, installedPackage{root: rootPath, manifest: manifest})
	}
	for i := range packages {
		for _, entry := range entries {
			if entry.IsDir() || entry.Link != "" || !(entry.Path == packages[i].root || strings.HasPrefix(entry.Path, packages[i].root+"/")) {
				continue
			}
			// Exclude nested installed packages from their parent's content and size.
			rel := strings.TrimPrefix(entry.Path, packages[i].root+"/")
			if strings.Contains(rel, "/node_modules/") || strings.HasPrefix(rel, "node_modules/") {
				continue
			}
			packages[i].files = append(packages[i].files, entry)
			packages[i].size += entry.Size
		}
	}
	return packages, root, rootFound, warnings
}

func isInstalledPackageRoot(root string) bool {
	parts := strings.Split(root, "/")
	for i, part := range slices.Backward(parts) {
		if part != "node_modules" {
			continue
		}
		remaining := len(parts) - i - 1
		return remaining == 1 || (remaining == 2 && strings.HasPrefix(parts[i+1], "@"))
	}
	return false
}

func incompatiblePackage(m packageManifest, target Target) (string, []string, string, bool) {
	checks := []struct {
		name   string
		values []string
		target string
	}{{"os", m.OS, target.Platform}, {"cpu", m.CPU, target.Arch}, {"libc", m.Libc, target.Libc}}
	for _, check := range checks {
		if check.target != "" && !selectorAllows(check.values, check.target) {
			return check.name, check.values, check.target, true
		}
	}
	return "", nil, "", false
}

func selectorAllows(values []string, target string) bool {
	if len(values) == 0 {
		return true
	}
	target = strings.ToLower(target)
	hasPositive, positiveMatch := false, false
	for _, raw := range values {
		v := strings.ToLower(strings.TrimSpace(raw))
		if after, ok := strings.CutPrefix(v, "!"); ok {
			if after == target {
				return false
			}
			continue
		}
		hasPositive = true
		positiveMatch = positiveMatch || v == target
	}
	return !hasPositive || positiveMatch
}

func incompatiblePrebuild(name string, target Target) (string, bool) {
	parts := strings.Split(strings.ToLower(name), "/")
	for i, part := range parts {
		if part != "prebuilds" || i+1 >= len(parts) {
			continue
		}
		tag, _, _ := strings.Cut(parts[i+1], "+")
		bits := strings.Split(tag, "-")
		if len(bits) < 2 {
			return "", false
		}
		platform, arch := bits[0], bits[1]
		knownPlatform := slices.Contains([]string{"win32", "linux", "darwin", "freebsd", "android"}, platform)
		knownArch := slices.Contains([]string{"x64", "ia32", "arm64", "arm", "ppc64", "s390x", "riscv64"}, arch)
		if !knownPlatform || !knownArch {
			return "", false
		}
		if (target.Platform != "" && platform != strings.ToLower(target.Platform)) || (target.Arch != "" && arch != strings.ToLower(target.Arch)) {
			return fmt.Sprintf("native prebuild targets %s-%s, not %s-%s", platform, arch, target.Platform, target.Arch), true
		}
		if target.Libc != "" && platform == "linux" {
			base := strings.ToLower(path.Base(name))
			for _, libc := range []string{"glibc", "musl"} {
				if (strings.Contains(base, "."+libc+".") || strings.Contains(base, "-"+libc+".")) && libc != strings.ToLower(target.Libc) {
					return fmt.Sprintf("native prebuild targets libc %s, not %s", libc, target.Libc), true
				}
			}
		}
	}
	return "", false
}

func normalizeLocales(locales []string) map[string]bool {
	out := make(map[string]bool, len(locales))
	for _, locale := range locales {
		out[strings.ToLower(strings.ReplaceAll(strings.TrimSpace(locale), "_", "-"))] = true
	}
	return out
}

func localeAllowed(locale string, allowed map[string]bool) bool {
	locale = strings.ToLower(strings.ReplaceAll(locale, "_", "-"))
	if allowed[locale] {
		return true
	}
	base := strings.Split(locale, "-")[0]
	return allowed[base]
}

func packageLocale(name string) (string, bool) {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if i == 0 || i+1 >= len(parts) || !slices.Contains([]string{"locale", "locales"}, strings.ToLower(part)) {
			continue
		}
		// Restrict this heuristic to installed dependencies; application i18n
		// layouts are too application-specific for safe generic classification.
		if !slices.Contains(parts[:i], "node_modules") {
			continue
		}
		locale := strings.TrimSuffix(parts[i+1], path.Ext(parts[i+1]))
		if locale != "" && locale != "index" && locale != "package" {
			return locale, true
		}
	}
	return "", false
}

func dependencyJunk(name string) (string, bool) {
	base := strings.ToLower(path.Base(name))
	parts := strings.SplitSeq(strings.ToLower(name), "/")
	for part := range parts {
		if slices.Contains([]string{".git", ".hg", ".svn", ".circleci", ".github", ".nyc_output", "coverage"}, part) {
			return "dependency repository, CI, or coverage metadata", true
		}
	}
	if slices.Contains([]string{".ds_store", "thumbs.db", "desktop.ini", "appveyor.yml", ".codecov.yml"}, base) {
		return "dependency development or operating-system metadata", true
	}
	ext := strings.ToLower(path.Ext(base))
	if slices.Contains([]string{".o", ".a", ".lib", ".pdb"}, ext) {
		return "native build or debug artifact", true
	}
	return "", false
}

const (
	reachableOptional = 1
	reachableRequired = 2
)

func auditDependencyTree(packages []installedPackage, root packageManifest) ([]PackageIssue, []string, map[string]int) {
	byRoot := make(map[string]*installedPackage, len(packages))
	for i := range packages {
		byRoot[packages[i].root] = &packages[i]
	}
	reachability := map[string]int{}
	var missing []string
	var packageIssues []PackageIssue
	type request struct {
		importer, name string
		strength       int
	}
	queue := make([]request, 0, len(root.Dependencies)+len(root.OptionalDependencies))
	for name := range root.Dependencies {
		queue = append(queue, request{".", name, reachableRequired})
	}
	for name := range root.OptionalDependencies {
		queue = append(queue, request{".", name, reachableOptional})
	}
	for len(queue) > 0 {
		req := queue[0]
		queue = queue[1:]
		pkg := resolveInstalled(byRoot, req.importer, req.name)
		if pkg == nil {
			if req.strength == reachableRequired {
				missing = append(missing, req.importer+" -> "+req.name)
			}
			continue
		}
		if reachability[pkg.root] >= req.strength {
			continue
		}
		reachability[pkg.root] = req.strength
		for name := range pkg.manifest.Dependencies {
			queue = append(queue, request{pkg.root, name, req.strength})
		}
		for name := range pkg.manifest.OptionalDependencies {
			queue = append(queue, request{pkg.root, name, reachableOptional})
		}
	}
	for _, pkg := range packages {
		if reachability[pkg.root] == 0 {
			packageIssues = append(packageIssues, PackageIssue{Root: pkg.root, Name: pkg.manifest.Name, Version: pkg.manifest.Version, Kind: "extraneous", Detail: "not reachable from root runtime dependencies", Bytes: pkg.size})
		}
	}
	slices.Sort(missing)
	return packageIssues, slices.Compact(missing), reachability
}

func resolveInstalled(byRoot map[string]*installedPackage, importer, name string) *installedPackage {
	dir := importer
	if dir == "." {
		dir = ""
	}
	for {
		candidate := path.Join(dir, "node_modules", name)
		if pkg := byRoot[candidate]; pkg != nil {
			return pkg
		}
		if dir == "" {
			break
		}
		parent := path.Dir(dir)
		if parent == "." || parent == dir {
			dir = ""
		} else {
			dir = parent
		}
	}
	return nil
}

func duplicatePackages(a *asar.Archive, packages []installedPackage, warnings *[]string) []DuplicatePackageGroup {
	type groupKey struct{ name, version, hash string }
	groups := map[groupKey][]installedPackage{}
	for _, pkg := range packages {
		h := sha256.New()
		failed := false
		for _, entry := range pkg.files {
			data, err := a.ReadFile(entry.Path)
			if err != nil {
				*warnings = append(*warnings, fmt.Sprintf("hash %s: %v", entry.Path, err))
				failed = true
				break
			}
			rel := strings.TrimPrefix(entry.Path, pkg.root+"/")
			h.Write([]byte(rel))
			h.Write([]byte{0})
			h.Write(data)
			h.Write([]byte{0})
		}
		if !failed {
			key := groupKey{pkg.manifest.Name, pkg.manifest.Version, hex.EncodeToString(h.Sum(nil))}
			groups[key] = append(groups[key], pkg)
		}
	}
	var out []DuplicatePackageGroup
	for key, group := range groups {
		if len(group) < 2 {
			continue
		}
		roots := make([]string, len(group))
		var bytes int64
		for i, pkg := range group {
			roots[i] = pkg.root
			bytes += pkg.size
		}
		slices.Sort(roots)
		out = append(out, DuplicatePackageGroup{Name: key.name, Version: key.version, Roots: roots, ContentHash: key.hash, TotalBytes: bytes, RecoverableBytes: bytes - group[0].size})
	}
	slices.SortFunc(out, func(a, b DuplicatePackageGroup) int { return strings.Compare(a.Name+a.Version, b.Name+b.Version) })
	return out
}
