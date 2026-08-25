package optimizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

type policy struct {
	opts       Options
	keep       []*regexp.Regexp
	remove     []*regexp.Regexp
	profile    []*regexp.Regexp
	profileWhy []string
}

var safeRules = []struct{ pattern, reason string }{
	{"**/node_modules/**/.github/**", "dependency repository metadata"},
	{"**/node_modules/**/.nyc_output/**", "dependency test coverage"},
	{"**/node_modules/**/{test,tests,__tests__,coverage,docs,doc,examples,example,benchmarks,benchmark}/**", "dependency development resources"},
	{"**/node_modules/**/*.{spec,test}.{js,cjs,mjs,ts,tsx}", "dependency test source"},
	{"**/node_modules/**/*.map", "dependency source map"},
	{"**/node_modules/**/*.d.ts", "dependency TypeScript declarations"},
	{"**/node_modules/**/*.tsbuildinfo", "TypeScript build cache"},
	{"**/node_modules/**/{readme,readme.*,changelog,changelog.*,changes,changes.*}", "dependency documentation"},
	{"**/node_modules/**/{.babelrc,.babelrc.js,.babelrc.json,.editorconfig,.eslintignore,.eslintrc,.eslintrc.js,.eslintrc.json,.npmignore,.prettierignore,.prettierrc,.prettierrc.js,.prettierrc.json,.travis.yml,tsconfig.json,jsconfig.json,yarn.lock,package-lock.json,pnpm-lock.yaml}", "dependency development configuration"},
}

var balancedRules = []struct{ pattern, reason string }{
	{"**/*.map", "source map"},
}

var aggressiveRules = []struct{ pattern, reason string }{
	{"**/*.{map,md,markdown,ts,tsx,jsx,vue,coffee,scss,bak,log,gyp,gypi,c,cc,cpp,h,hpp,patch,pdf,tgz}", "aggressive legacy trim rule"},
	{"**/{test,tests,__tests__,docs,doc,examples,coverage}/**", "aggressive non-runtime directory rule"},
	{"**/{.babelrc,.editorconfig,.eslintignore,.eslintrc,.gitmodules,.npmignore,.travis.yml,tsconfig.json,yarn.lock}", "aggressive development metadata rule"},
}

func newPolicy(opts Options) (*policy, error) {
	var err error
	opts, err = normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	p := &policy{opts: opts}
	if p.keep, err = compileGlobs(opts.KeepPatterns); err != nil {
		return nil, fmt.Errorf("keep pattern: %w", err)
	}
	if p.remove, err = compileGlobs(opts.RemovePatterns); err != nil {
		return nil, fmt.Errorf("remove pattern: %w", err)
	}
	rules := append([]struct{ pattern, reason string }{}, safeRules...)
	if opts.Profile == ProfileBalanced || opts.Profile == ProfileAggressive {
		rules = append(rules, balancedRules...)
	}
	if opts.Profile == ProfileAggressive {
		rules = append(rules, aggressiveRules...)
	}
	for _, rule := range rules {
		re, compileErr := compileGlob(rule.pattern)
		if compileErr != nil {
			return nil, compileErr
		}
		p.profile = append(p.profile, re)
		p.profileWhy = append(p.profileWhy, rule.reason)
	}
	return p, nil
}

func (p *policy) decide(name string) Decision {
	name = normalizePath(name)
	if matchRegexps(p.keep, name) {
		return Decision{Action: ActionKeep, Reason: "matched keep override"}
	}
	if matchRegexps(p.remove, name) {
		return Decision{Action: ActionRemove, Reason: "matched remove override", Risk: "user"}
	}
	if p.opts.DangerouslyRemoveLicenses && IsLicenseFile(name) {
		return Decision{Action: ActionRemove, Reason: "dangerous license removal requested", Risk: "critical"}
	}
	if p.opts.ExternalizeLicenses != "" && IsLicenseFile(name) {
		return Decision{Action: ActionRemove, Reason: "license consolidated outside archive", Risk: "low"}
	}
	if p.opts.ExternalizeSourceMaps != "" && strings.EqualFold(path.Ext(name), ".map") {
		return Decision{Action: ActionRemove, Reason: "source map externalized", Risk: "low"}
	}
	if protectedLegalFile(path.Base(name)) {
		return Decision{Action: ActionKeep, Reason: "protected legal metadata"}
	}
	for i, re := range p.profile {
		if re.MatchString(name) {
			risk := "low"
			if p.opts.Profile == ProfileBalanced {
				risk = "medium"
			} else if p.opts.Profile == ProfileAggressive {
				risk = "high"
			}
			return Decision{Action: ActionRemove, Reason: p.profileWhy[i], Risk: risk}
		}
	}
	return Decision{Action: ActionKeep}
}

func protectedLegalFile(base string) bool {
	return IsLicenseFile(base)
}

func (p *policy) transform(name string, input []byte) ([]byte, string, error) {
	out := input
	var reasons []string
	ext := strings.ToLower(path.Ext(name))
	if p.opts.StripSourceMapSources && ext == ".map" {
		stripped, changed, err := StripSourceMapSourcesContent(out)
		if err != nil {
			return input, "", err
		}
		if changed {
			out = stripped
			reasons = append(reasons, "stripped embedded source-map sources")
		}
	}
	jsonOut, jsonReason, err := p.transformJSON(name, out)
	if err != nil {
		return input, "", err
	}
	if jsonReason != "" {
		out = jsonOut
		reasons = append(reasons, jsonReason)
	}
	wantsMinify := (p.opts.MinifyJS && (ext == ".js" || ext == ".mjs" || ext == ".cjs")) ||
		(p.opts.MinifyCSS && ext == ".css") || (p.opts.MinifyHTML && (ext == ".html" || ext == ".htm")) ||
		(p.opts.MinifySVG && ext == ".svg")
	if wantsMinify {
		minified, changed, minifyErr := MinifyAsset(name, out)
		if minifyErr != nil {
			return input, "", minifyErr
		}
		if changed {
			out = minified
			reasons = append(reasons, "minified generated asset")
		}
	}
	if p.opts.DangerouslyRemoveLicenses {
		stripped, changed := StripLicenseHeader(name, out)
		if changed {
			out = stripped
			reasons = append(reasons, "removed license header")
		}
	}
	if len(reasons) == 0 || bytes.Equal(out, input) {
		return input, "", nil
	}
	return out, strings.Join(reasons, "; "), nil
}

func (p *policy) transformJSON(name string, input []byte) ([]byte, string, error) {
	if !strings.EqualFold(path.Ext(name), ".json") && !strings.EqualFold(path.Ext(name), ".map") {
		return input, "", nil
	}
	isPackage := strings.EqualFold(path.Base(normalizePath(name)), "package.json")
	if !p.opts.MinifyJSON && !(p.opts.PrunePackage && isPackage) {
		return input, "", nil
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return input, "", nil
	}
	if dec.Decode(new(any)) == nil {
		return input, "", nil
	}
	reason := "minified JSON"
	pruned := false
	if p.opts.PrunePackage && isPackage {
		if object, ok := value.(map[string]any); ok {
			for _, key := range packageKeys(p.opts.Profile) {
				if _, exists := object[key]; exists {
					delete(object, key)
					pruned = true
				}
			}
			reason = "minified and pruned package metadata"
		}
	}
	var out []byte
	var err error
	if pruned {
		out, err = json.Marshal(value)
	} else {
		var compact bytes.Buffer
		err = json.Compact(&compact, input)
		out = compact.Bytes()
	}
	if err != nil {
		return input, "", err
	}
	if bytes.Equal(out, input) {
		return input, "", nil
	}
	return out, reason, nil
}

func packageKeys(profile Profile) []string {
	keys := []string{"bugs", "contributors", "devDependencies", "directories", "funding", "homepage", "keywords", "readme", "readmeFilename", "repository"}
	if profile == ProfileBalanced || profile == ProfileAggressive {
		keys = append(keys, "description", "files", "man", "packageManager", "scripts", "typesVersions")
	}
	if profile == ProfileAggressive {
		keys = append(keys, "author", "bin", "browser", "browserslist", "dependencies", "description", "engines", "exports", "imports", "license", "name", "optionalDependencies", "peerDependencies", "private", "publishConfig", "types", "typings", "version")
	}
	return keys
}

func normalizePath(name string) string {
	return strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./")
}

func compileGlobs(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := compileGlob(pattern)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, nil
}

func compileGlob(pattern string) (*regexp.Regexp, error) {
	pattern = normalizePath(pattern)
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '{':
			end := strings.IndexByte(pattern[i+1:], '}')
			if end < 0 {
				return nil, fmt.Errorf("unclosed brace in %q", pattern)
			}
			end += i + 1
			parts := strings.Split(pattern[i+1:end], ",")
			b.WriteString("(?:")
			for j, part := range parts {
				if j > 0 {
					b.WriteByte('|')
				}
				b.WriteString(regexp.QuoteMeta(part))
			}
			b.WriteByte(')')
			i = end
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteByte('$')
	return regexp.Compile(b.String())
}

func matchRegexps(patterns []*regexp.Regexp, name string) bool {
	return slices.ContainsFunc(patterns, func(re *regexp.Regexp) bool { return re.MatchString(name) })
}
