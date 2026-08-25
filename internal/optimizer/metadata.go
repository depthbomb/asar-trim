package optimizer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type bundlerDocument struct {
	Inputs map[string]struct {
		Bytes int64 `json:"bytes"`
	} `json:"inputs"`
	Outputs map[string]struct {
		Bytes  int64 `json:"bytes"`
		Inputs map[string]struct {
			BytesInOutput int64 `json:"bytesInOutput"`
		} `json:"inputs"`
	} `json:"outputs"`
	Assets []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
	Modules []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"modules"`
}

func bundlerRecommendations(metadataFiles []string, archivePaths map[string]int64) ([]Recommendation, error) {
	var recommendations []Recommendation
	for _, name := range metadataFiles {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read bundler metadata %s: %w", name, err)
		}
		var doc bundlerDocument
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse bundler metadata %s: %w", name, err)
		}
		var inputBytes, outputBytes int64
		for _, input := range doc.Inputs {
			inputBytes += input.Bytes
		}
		for _, output := range doc.Outputs {
			outputBytes += output.Bytes
		}
		if len(doc.Outputs) > 0 {
			recommendations = append(recommendations, Recommendation{
				Kind:           "bundler",
				Message:        fmt.Sprintf("%s describes %d inputs and %d outputs; exclude original inputs already represented by these outputs at packaging time", filepath.Base(name), len(doc.Inputs), len(doc.Outputs)),
				EstimatedBytes: max64(0, inputBytes-outputBytes),
			})
			if paths, bytes := archivedBundlerInputs(doc, archivePaths); len(paths) > 0 {
				recommendations = append(recommendations, Recommendation{Kind: "bundled-inputs", Message: "possible original bundler inputs are still present in the archive; verify and exclude them from the packaging allowlist", Paths: paths, EstimatedBytes: bytes})
			}
			continue
		}
		if len(doc.Assets) > 0 || len(doc.Modules) > 0 {
			for _, asset := range doc.Assets {
				outputBytes += asset.Size
			}
			for _, module := range doc.Modules {
				inputBytes += module.Size
			}
			recommendations = append(recommendations, Recommendation{
				Kind:           "bundler",
				Message:        fmt.Sprintf("%s describes %d Webpack modules and %d assets; use the module list to exclude source trees duplicated by emitted assets", filepath.Base(name), len(doc.Modules), len(doc.Assets)),
				EstimatedBytes: max64(0, inputBytes-outputBytes),
			})
			continue
		}
		return nil, fmt.Errorf("bundler metadata %s is neither an esbuild metafile nor Webpack stats", name)
	}
	return recommendations, nil
}

func archivedBundlerInputs(doc bundlerDocument, archivePaths map[string]int64) ([]string, int64) {
	outputs := make(map[string]bool)
	for name := range doc.Outputs {
		outputs[normalizeMetadataPath(name)] = true
	}
	var paths []string
	var bytes int64
	for input := range doc.Inputs {
		candidate := normalizeMetadataPath(input)
		if outputs[candidate] {
			continue
		}
		for archivePath, size := range archivePaths {
			if archivePath == candidate || strings.HasSuffix(archivePath, "/"+candidate) {
				paths = append(paths, archivePath)
				bytes += size
				break
			}
		}
	}
	return paths, bytes
}

func normalizeMetadataPath(name string) string {
	name = filepath.ToSlash(filepath.Clean(name))
	return strings.TrimPrefix(name, "./")
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
