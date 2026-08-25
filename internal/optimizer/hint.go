package optimizer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	archivefs "github.com/depthbomb/asar-trim/internal/archive"
	asar "github.com/depthbomb/go-asar"
)

var (
	moduleReference      = regexp.MustCompile(`(?m)(?:require\s*\(|from\s+|import\s*\(?\s*)["']([^"']+)["']`)
	runtimeReference     = regexp.MustCompile(`(?m)(?:new\s+(?:Shared)?Worker\s*\(|utilityProcess\.fork\s*\(|preload\s*:\s*)["']([^"']+)["']`)
	runtimePathReference = regexp.MustCompile(`(?m)(?:new\s+(?:Shared)?Worker\s*\(|utilityProcess\.fork\s*\(|preload\s*:)[^\r\n]{0,160}?path\.(?:join|resolve)\s*\(\s*__dirname\s*,\s*["']([^"']+)["']`)
	htmlScriptReference  = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)
)

// GenerateHint builds a best-effort startup ordering hint from statically
// discoverable JavaScript module references in an archive.
func GenerateHint(input string, opts HintOptions) (HintReport, error) {
	if len(opts.AccessLogs) > 0 {
		report, _, err := GenerateAccessLogHint(input, opts.AccessLogs, opts.Output, opts.Force)
		return report, err
	}
	pair, err := archivefs.Resolve(input)
	if err != nil {
		return HintReport{}, err
	}
	return generateHintPair(pair, opts)
}

func generateHintPair(pair archivefs.Pair, opts HintOptions) (HintReport, error) {
	if opts.Output == "" {
		return HintReport{}, errors.New("hint output path is required")
	}
	out, err := filepath.Abs(opts.Output)
	if err != nil {
		return HintReport{}, err
	}
	a, err := asar.Open(pair.ArchivePath)
	if err != nil {
		return HintReport{}, err
	}
	defer a.Close()

	files := make(map[string]bool)
	allPaths := make([]string, 0, len(a.Entries()))
	for _, entry := range a.Entries() {
		if !entry.IsDir() && entry.Link == "" {
			files[entry.Path] = true
			allPaths = append(allPaths, entry.Path)
		}
	}

	seeds := append([]string{}, opts.EntryPoints...)
	if files["package.json"] {
		seeds = append([]string{"package.json"}, seeds...)
		var pkg struct {
			Main string `json:"main"`
		}
		if data, readErr := a.ReadFile("package.json"); readErr == nil && json.Unmarshal(data, &pkg) == nil && pkg.Main != "" {
			seeds = append(seeds, pkg.Main)
		}
	}
	if len(seeds) == 0 {
		for _, fallback := range []string{"index.js", "main.js", "index.cjs", "index.mjs"} {
			if files[fallback] {
				seeds = append(seeds, fallback)
				break
			}
		}
	}

	queue := make([]string, 0, len(seeds))
	resolvedSeeds := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		cleaned := strings.TrimPrefix(filepath.ToSlash(seed), "./")
		resolved := resolveFileOrDirectory(a, files, cleaned)
		if resolved == "" {
			resolved = resolveModule(a, files, "", cleaned)
		}
		if resolved != "" {
			queue = append(queue, resolved)
			resolvedSeeds = append(resolvedSeeds, resolved)
		}
	}
	seen := make(map[string]bool)
	ordered := make([]string, 0)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		ordered = append(ordered, name)
		if !isScript(name) && !isHTML(name) {
			continue
		}
		data, readErr := a.ReadFile(name)
		if readErr != nil {
			return HintReport{}, fmt.Errorf("read %s: %w", name, readErr)
		}
		for _, spec := range discoverReferences(name, data) {
			if dep := resolveModule(a, files, name, spec); dep != "" && !seen[dep] {
				if manifest := moduleManifest(files, dep); manifest != "" && !seen[manifest] {
					queue = append(queue, manifest)
				}
				queue = append(queue, dep)
			}
		}
	}
	if len(ordered) == 0 {
		return HintReport{}, errors.New("no startup entry points could be resolved; pass --entry PATH")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return HintReport{}, err
	}
	flags := os.O_WRONLY | os.O_CREATE
	if opts.Force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(out, flags, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return HintReport{}, fmt.Errorf("hint output %q already exists", out)
		}
		return HintReport{}, err
	}
	_, writeErr := (&asar.Hint{Paths: ordered}).WriteTo(f)
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(out)
		return HintReport{}, writeErr
	}
	if closeErr != nil {
		_ = os.Remove(out)
		return HintReport{}, closeErr
	}
	report := HintReport{Archive: pair.ArchivePath, Output: out, Paths: len(ordered), Coverage: (&asar.Hint{Paths: ordered}).Coverage(allPaths), EntryPoints: resolvedSeeds}
	report.Warnings = append(report.Warnings, "static analysis cannot discover every dynamic or computed module load; validate startup behavior and add --entry paths when needed")
	return report, nil
}

func discoverReferences(name string, data []byte) []string {
	var matches [][][]byte
	var joinedPathMatches [][][]byte
	if isHTML(name) {
		matches = htmlScriptReference.FindAllSubmatch(data, -1)
	} else {
		matches = moduleReference.FindAllSubmatch(data, -1)
		matches = append(matches, runtimeReference.FindAllSubmatch(data, -1)...)
		joinedPathMatches = runtimePathReference.FindAllSubmatch(data, -1)
	}
	seen := make(map[string]bool)
	var refs []string
	for _, match := range matches {
		if len(match) > 1 && !seen[string(match[1])] {
			seen[string(match[1])] = true
			refs = append(refs, string(match[1]))
		}
	}
	for _, match := range joinedPathMatches {
		if len(match) < 2 {
			continue
		}
		ref := string(match[1])
		if !strings.HasPrefix(ref, ".") && !strings.HasPrefix(ref, "/") {
			ref = "./" + ref
		}
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	return refs
}

func moduleManifest(files map[string]bool, resolved string) string {
	parts := strings.Split(resolved, "/")
	index := -1
	for i, part := range parts {
		if part == "node_modules" {
			index = i
		}
	}
	if index < 0 || index+1 >= len(parts) {
		return ""
	}
	end := index + 2
	if strings.HasPrefix(parts[index+1], "@") {
		end++
	}
	if end > len(parts) {
		return ""
	}
	manifest := strings.Join(append(append([]string{}, parts[:end]...), "package.json"), "/")
	if files[manifest] {
		return manifest
	}
	return ""
}

func isScript(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".cjs", ".mjs", ".jsx", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

func isHTML(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm":
		return true
	default:
		return false
	}
}

func resolveModule(a *asar.Archive, files map[string]bool, importer, spec string) string {
	spec = filepath.ToSlash(spec)
	if spec == "" || strings.HasPrefix(spec, "node:") || (!strings.HasPrefix(spec, ".") && strings.Contains(spec, ":")) {
		return ""
	}
	if strings.HasPrefix(spec, "#") {
		return resolvePackageImport(a, files, importer, spec)
	}
	var bases []string
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
		bases = []string{path.Clean(path.Join(path.Dir(importer), spec))}
	} else {
		packageName, subpath := splitPackageSpecifier(spec)
		for dir := path.Dir(importer); ; dir = path.Dir(dir) {
			packageDir := path.Join(dir, "node_modules", packageName)
			if target := resolvePackageExport(a, files, packageDir, subpath); target != "" {
				return target
			}
			bases = append(bases, path.Join(packageDir, subpath))
			if dir == "." || dir == "" || dir == "/" {
				break
			}
		}
	}
	for _, base := range bases {
		base = strings.TrimPrefix(path.Clean(base), "./")
		if found := resolveFileOrDirectory(a, files, base); found != "" {
			return found
		}
	}
	return ""
}

func splitPackageSpecifier(spec string) (string, string) {
	parts := strings.Split(spec, "/")
	n := 1
	if strings.HasPrefix(spec, "@") && len(parts) > 1 {
		n = 2
	}
	return strings.Join(parts[:n], "/"), strings.Join(parts[n:], "/")
}

func resolvePackageExport(a *asar.Archive, files map[string]bool, packageDir, subpath string) string {
	manifest := path.Join(packageDir, "package.json")
	if !files[manifest] {
		return ""
	}
	var pkg struct {
		Exports any `json:"exports"`
	}
	data, err := a.ReadFile(manifest)
	if err != nil || json.Unmarshal(data, &pkg) != nil || pkg.Exports == nil {
		return ""
	}
	key := "."
	if subpath != "" {
		key = "./" + subpath
	}
	target := conditionalTarget(pkg.Exports, key)
	if target == "" || !strings.HasPrefix(target, "./") {
		return ""
	}
	return resolveFileOrDirectory(a, files, path.Join(packageDir, target))
}

func conditionalTarget(value any, key string) string {
	switch v := value.(type) {
	case string:
		if key == "." {
			return v
		}
	case []any:
		for _, item := range v {
			if target := conditionalTarget(item, key); target != "" {
				return target
			}
		}
	case map[string]any:
		if direct, ok := v[key]; ok {
			return conditionalTarget(direct, ".")
		}
		bestPattern := ""
		var bestItem any
		for pattern, item := range v {
			star := strings.IndexByte(pattern, '*')
			if star < 0 || !strings.HasPrefix(key, pattern[:star]) || !strings.HasSuffix(key, pattern[star+1:]) {
				continue
			}
			if bestPattern == "" || star > strings.IndexByte(bestPattern, '*') || (star == strings.IndexByte(bestPattern, '*') && pattern < bestPattern) {
				bestPattern, bestItem = pattern, item
			}
		}
		if bestPattern != "" {
			star := strings.IndexByte(bestPattern, '*')
			matched := key[star : len(key)-len(bestPattern[star+1:])]
			if target := conditionalTarget(bestItem, "."); target != "" {
				return strings.ReplaceAll(target, "*", matched)
			}
		}
		for _, condition := range []string{"electron", "node", "import", "require", "default"} {
			if item, ok := v[condition]; ok {
				if target := conditionalTarget(item, key); target != "" {
					return target
				}
			}
		}
	}
	return ""
}

func resolvePackageImport(a *asar.Archive, files map[string]bool, importer, spec string) string {
	for dir := path.Dir(importer); ; dir = path.Dir(dir) {
		manifest := path.Join(dir, "package.json")
		if files[manifest] {
			var pkg struct {
				Imports map[string]any `json:"imports"`
			}
			if data, err := a.ReadFile(manifest); err == nil && json.Unmarshal(data, &pkg) == nil {
				if target := conditionalTarget(pkg.Imports, spec); target != "" {
					if strings.HasPrefix(target, "./") {
						return resolveFileOrDirectory(a, files, path.Join(dir, target))
					}
					if target != spec {
						return resolveModule(a, files, importer, target)
					}
				}
			}
			return ""
		}
		if dir == "." || dir == "" || dir == "/" {
			break
		}
	}
	return ""
}

func resolveFileOrDirectory(a *asar.Archive, files map[string]bool, base string) string {
	for _, candidate := range []string{base, base + ".js", base + ".cjs", base + ".mjs", base + ".json"} {
		if files[candidate] {
			return candidate
		}
	}
	pkgName := path.Join(base, "package.json")
	if files[pkgName] {
		var pkg struct {
			Main string `json:"main"`
		}
		if data, err := a.ReadFile(pkgName); err == nil && json.Unmarshal(data, &pkg) == nil && pkg.Main != "" {
			if found := resolveFileOrDirectory(a, files, path.Join(base, pkg.Main)); found != "" {
				return found
			}
		}
	}
	for _, candidate := range []string{path.Join(base, "index.js"), path.Join(base, "index.cjs"), path.Join(base, "index.mjs"), path.Join(base, "index.json")} {
		if files[candidate] {
			return candidate
		}
	}
	return ""
}
