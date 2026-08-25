package optimizer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// AssetMinificationCaveat describes the intentionally narrow scope of the
// built-in minifiers. They are suitable for generated assets only. In
// particular, they are not substitutes for a parser-aware build-time minifier.
const AssetMinificationCaveat = "built-in minification is conservative but not parser-aware; use only on generated assets and verify the application"

var (
	sourceMapDirective = regexp.MustCompile(`(?m)(?:^|\n)[\t ]*(?://[#@][\t ]*sourceMappingURL=\S+|/\*[#@][\t ]*sourceMappingURL=\S+[\t ]*\*/)[\t ]*(?:\n|$)`)
	licenseMarker      = regexp.MustCompile(`(?i)(?:spdx-license-identifier|copyright|licensed under|permission is hereby granted|all rights reserved|mozilla public license|gnu (?:general|lesser|affero) public license)`)
	htmlComment        = regexp.MustCompile(`(?s)<!--[\t ]*(.*?)[\t ]*-->`)
	betweenTags        = regexp.MustCompile(`>[\t\r\n ]+<`)
)

// StripSourceMapSourcesContent removes embedded source bodies while retaining
// mappings and source names. Invalid maps are reported rather than silently
// rewritten.
func StripSourceMapSourcesContent(data []byte) (out []byte, changed bool, err error) {
	var sourceMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &sourceMap); err != nil {
		return nil, false, fmt.Errorf("parse source map: %w", err)
	}
	if _, ok := sourceMap["sourcesContent"]; !ok {
		return data, false, nil
	}
	delete(sourceMap, "sourcesContent")
	out, err = json.Marshal(sourceMap)
	if err != nil {
		return nil, false, fmt.Errorf("encode source map: %w", err)
	}
	return out, true, nil
}

// RemoveSourceMappingURL removes external or data-URL source map directives
// from JavaScript and CSS without otherwise rewriting the asset.
func RemoveSourceMappingURL(data []byte) (out []byte, changed bool) {
	out = sourceMapDirective.ReplaceAllFunc(data, func(match []byte) []byte {
		if len(match) > 0 && match[0] == '\n' {
			return []byte("\n")
		}
		return nil
	})
	return out, !bytes.Equal(out, data)
}

// SourceMapExternalizationPlan contains pure, side-effect-free output for an
// externalization operation. The caller writes MapData to DestinationPath and
// replaces the archive asset with AssetData; it may then remove MapPath.
type SourceMapExternalizationPlan struct {
	AssetPath              string
	MapPath                string
	DestinationPath        string
	AssetData              []byte
	MapData                []byte
	StrippedSourcesContent bool
}

// PlanSourceMapExternalization validates archive-relative paths, removes the
// sourceMappingURL from the asset, and optionally strips embedded sources.
// It performs no filesystem writes.
func PlanSourceMapExternalization(assetPath, mapPath string, assetData, mapData []byte, destinationRoot string, stripSourcesContent bool) (SourceMapExternalizationPlan, error) {
	if destinationRoot == "" {
		return SourceMapExternalizationPlan{}, errors.New("source map destination root is empty")
	}
	cleanMap, err := cleanArchiveRelativePath(mapPath)
	if err != nil {
		return SourceMapExternalizationPlan{}, fmt.Errorf("source map path: %w", err)
	}
	cleanAsset, err := cleanArchiveRelativePath(assetPath)
	if err != nil {
		return SourceMapExternalizationPlan{}, fmt.Errorf("asset path: %w", err)
	}
	rewrittenAsset, _ := RemoveSourceMappingURL(assetData)
	rewrittenMap := mapData
	stripped := false
	if stripSourcesContent {
		rewrittenMap, stripped, err = StripSourceMapSourcesContent(mapData)
		if err != nil {
			return SourceMapExternalizationPlan{}, err
		}
	}
	return SourceMapExternalizationPlan{
		AssetPath: cleanAsset, MapPath: cleanMap,
		DestinationPath: filepath.Join(destinationRoot, filepath.FromSlash(cleanMap)),
		AssetData:       rewrittenAsset, MapData: rewrittenMap,
		StrippedSourcesContent: stripped,
	}, nil
}

func cleanArchiveRelativePath(name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || filepath.IsAbs(name) || strings.Contains(clean, ":") {
		return "", fmt.Errorf("must be an archive-relative path: %q", name)
	}
	return clean, nil
}

// MinifyAsset applies a small dependency-free minifier selected by extension.
// JavaScript and CSS retain comments and newlines and only coalesce horizontal
// whitespace outside strings. HTML and SVG remove ordinary comments and
// whitespace between tags; conditional and binding-looking comments remain.
func MinifyAsset(path string, data []byte) (out []byte, changed bool, err error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs", ".css":
		out = collapseHorizontalWhitespace(data)
	case ".html", ".htm", ".svg":
		out = htmlComment.ReplaceAllFunc(data, func(comment []byte) []byte {
			body := htmlComment.FindSubmatch(comment)[1]
			lower := bytes.ToLower(body)
			if bytes.Contains(body, []byte("[")) || bytes.Contains(lower, []byte("ko ")) || licenseMarker.Match(body) {
				return comment
			}
			return nil
		})
		out = betweenTags.ReplaceAll(out, []byte("><"))
		out = bytes.TrimSpace(out)
	default:
		return data, false, fmt.Errorf("unsupported minification extension %q", filepath.Ext(path))
	}
	return out, !bytes.Equal(out, data), nil
}

// collapseHorizontalWhitespace deliberately preserves newlines (including ASI
// boundaries), comments, quoted strings, and template literals. Keeping one
// space also avoids joining identifiers or operators such as `+ +`.
func collapseHorizontalWhitespace(data []byte) []byte {
	out := make([]byte, 0, len(data))
	var quote byte
	escaped := false
	lineComment, blockComment := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if lineComment {
			out = append(out, c)
			if c == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			out = append(out, c)
			if c == '*' && i+1 < len(data) && data[i+1] == '/' {
				out = append(out, '/')
				i++
				blockComment = false
			}
			continue
		}
		if quote != 0 {
			out = append(out, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			quote = c
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(data) && data[i+1] == '/' {
			lineComment = true
			out = append(out, '/', '/')
			i++
			continue
		}
		if c == '/' && i+1 < len(data) && data[i+1] == '*' {
			blockComment = true
			out = append(out, '/', '*')
			i++
			continue
		}
		if c == ' ' || c == '\t' {
			out = append(out, ' ')
			for i+1 < len(data) && (data[i+1] == ' ' || data[i+1] == '\t') {
				i++
			}
			continue
		}
		out = append(out, c)
	}
	return out
}

// IsLicenseFile recognizes conventional standalone legal/attribution files.
// It intentionally operates on the base name only.
func IsLicenseFile(path string) bool {
	raw := strings.ToLower(filepath.Base(filepath.ToSlash(path)))
	base := strings.TrimSuffix(raw, filepath.Ext(raw))
	if slices.Contains([]string{"license", "licenses", "licence", "licences", "copying", "copyright", "notice", "notices", "authors", "third-party-licenses", "third_party_licenses", "third-party-notices", "third_party_notices"}, base) {
		return true
	}
	for _, prefix := range []string{"license.", "licence.", "notice.", "copying."} {
		if strings.HasPrefix(raw, prefix) {
			return true
		}
	}
	for _, prefix := range []string{"license-", "license_", "licence-", "licence_"} {
		if after, ok := strings.CutPrefix(base, prefix); ok {
			suffix := after
			if slices.Contains([]string{"mit", "apache", "apache-2", "apache-2.0", "bsd", "bsd-2-clause", "bsd-3-clause", "isc", "mpl", "mpl-2.0", "gpl", "gpl-2.0", "gpl-3.0", "lgpl", "agpl", "unlicense"}, suffix) {
				return true
			}
		}
	}
	return false
}

// StripLicenseHeader removes consecutive leading comments only when each
// removed comment contains a recognizable legal marker. A shebang is retained.
// This is intentionally dangerous: licenses can require preservation of these
// notices, and the caller must obtain explicit acknowledgement before use.
func StripLicenseHeader(path string, data []byte) (out []byte, changed bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if !supportsHeaderComments(ext) {
		return data, false
	}
	prefixEnd := 0
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		prefixEnd = 3
	}
	if bytes.HasPrefix(data[prefixEnd:], []byte("#!")) {
		if nl := bytes.IndexByte(data[prefixEnd:], '\n'); nl >= 0 {
			prefixEnd += nl + 1
		} else {
			return data, false
		}
	}
	pos := prefixEnd
	for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\r' || data[pos] == '\n') {
		pos++
	}
	start := pos
	for {
		end, ok := leadingCommentEnd(ext, data, pos)
		if !ok || !licenseMarker.Match(data[pos:end]) {
			break
		}
		pos = end
		for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\r' || data[pos] == '\n') {
			pos++
		}
	}
	if pos == start {
		return data, false
	}
	out = append([]byte{}, data[:prefixEnd]...)
	out = append(out, data[pos:]...)
	return out, true
}

func supportsHeaderComments(ext string) bool {
	return slices.Contains([]string{
		".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".css",
		".html", ".htm", ".svg", ".xml",
		".c", ".cc", ".cpp", ".h", ".hpp", ".java", ".kt", ".go", ".rs", ".swift",
		".py", ".rb", ".sh", ".bash", ".zsh", ".ps1", ".yaml", ".yml", ".toml",
	}, ext)
}

func leadingCommentEnd(ext string, data []byte, pos int) (int, bool) {
	if pos >= len(data) {
		return 0, false
	}
	if (ext == ".html" || ext == ".htm" || ext == ".svg" || ext == ".xml") && bytes.HasPrefix(data[pos:], []byte("<!--")) {
		if end := bytes.Index(data[pos+4:], []byte("-->")); end >= 0 {
			return pos + 4 + end + 3, true
		}
	}
	if bytes.HasPrefix(data[pos:], []byte("/*")) {
		if end := bytes.Index(data[pos+2:], []byte("*/")); end >= 0 {
			return pos + 2 + end + 2, true
		}
	}
	if bytes.HasPrefix(data[pos:], []byte("//")) {
		end := pos
		for end < len(data) && bytes.HasPrefix(data[end:], []byte("//")) {
			if nl := bytes.IndexByte(data[end:], '\n'); nl >= 0 {
				end += nl + 1
			} else {
				return len(data), true
			}
			for end < len(data) && (data[end] == ' ' || data[end] == '\t') {
				end++
			}
		}
		return end, true
	}
	if bytes.HasPrefix(data[pos:], []byte("#")) && !bytes.HasPrefix(data[pos:], []byte("#!")) {
		end := pos
		for end < len(data) && bytes.HasPrefix(data[end:], []byte("#")) && !bytes.HasPrefix(data[end:], []byte("#!")) {
			if nl := bytes.IndexByte(data[end:], '\n'); nl >= 0 {
				end += nl + 1
			} else {
				return len(data), true
			}
			for end < len(data) && (data[end] == ' ' || data[end] == '\t') {
				end++
			}
		}
		return end, true
	}
	return 0, false
}
