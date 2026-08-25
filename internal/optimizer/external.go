package optimizer

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var inlineSourceMap = regexp.MustCompile(`(?m)(?://[#@]\s*sourceMappingURL=|/\*[#@]\s*sourceMappingURL=)data:application/json(?:;charset=[^;,]+)?;base64,([A-Za-z0-9+/=]+)`)

type externalArtifacts struct {
	mapsDest, mapsStage       string
	licensesDest, licenseTemp string
	licenseFile               *os.File
	committedMaps             bool
	committedLicenses         bool
}

func newExternalArtifacts(opts Options) (*externalArtifacts, error) {
	e := &externalArtifacts{}
	var err error
	if opts.ExternalizeSourceMaps != "" {
		e.mapsDest, err = filepath.Abs(opts.ExternalizeSourceMaps)
		if err != nil {
			return nil, err
		}
		if err := requireAbsent(e.mapsDest); err != nil {
			return nil, fmt.Errorf("external source-map destination: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(e.mapsDest), 0o755); err != nil {
			return nil, err
		}
		e.mapsStage, err = os.MkdirTemp(filepath.Dir(e.mapsDest), ".asar-trim-maps-*")
		if err != nil {
			return nil, err
		}
	}
	if opts.ExternalizeLicenses != "" {
		e.licensesDest, err = filepath.Abs(opts.ExternalizeLicenses)
		if err != nil {
			e.cleanup()
			return nil, err
		}
		if err := requireAbsent(e.licensesDest); err != nil {
			e.cleanup()
			return nil, fmt.Errorf("external license destination: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(e.licensesDest), 0o755); err != nil {
			e.cleanup()
			return nil, err
		}
		f, createErr := os.CreateTemp(filepath.Dir(e.licensesDest), ".asar-trim-licenses-*")
		if createErr != nil {
			e.cleanup()
			return nil, createErr
		}
		e.licenseFile, e.licenseTemp = f, f.Name()
		if _, err := fmt.Fprintln(f, "Third-party licenses and notices extracted from the application archive."); err != nil {
			e.cleanup()
			return nil, err
		}
	}
	return e, nil
}

func requireAbsent(name string) error {
	if _, err := os.Lstat(name); err == nil {
		return fmt.Errorf("%q already exists", name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (e *externalArtifacts) captureRemoval(f Finding, extractedPath, extractedRoot string, stripSources bool) error {
	if f.Reason == "source map externalized" && e.mapsStage != "" {
		data, err := os.ReadFile(extractedPath)
		if err != nil {
			return err
		}
		if stripSources {
			if stripped, changed, stripErr := StripSourceMapSourcesContent(data); stripErr != nil {
				return fmt.Errorf("strip %s: %w", f.Path, stripErr)
			} else if changed {
				data = stripped
			}
		}
		dest := filepath.Join(e.mapsStage, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return err
		}
		assetPath := strings.TrimSuffix(f.Path, ".map")
		asset := filepath.Join(extractedRoot, filepath.FromSlash(assetPath))
		if assetData, readErr := os.ReadFile(asset); readErr == nil {
			if rewritten, changed := RemoveSourceMappingURL(assetData); changed {
				if err := os.WriteFile(asset, rewritten, 0o644); err != nil {
					return err
				}
			}
		}
	}
	if f.Reason == "license consolidated outside archive" && e.licenseFile != nil {
		data, err := os.ReadFile(extractedPath)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(e.licenseFile)
		if _, err := fmt.Fprintf(w, "\n===== %s =====\n", f.Path); err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		if len(data) == 0 || data[len(data)-1] != '\n' {
			if err := w.WriteByte('\n'); err != nil {
				return err
			}
		}
		return w.Flush()
	}
	return nil
}

func (e *externalArtifacts) captureInlineSourceMaps(extractedRoot string, stripSources bool) error {
	if e.mapsStage == "" {
		return nil
	}
	return filepath.WalkDir(extractedRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".js" && ext != ".mjs" && ext != ".cjs" && ext != ".css" {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		match := inlineSourceMap.FindSubmatch(data)
		if len(match) != 2 {
			return nil
		}
		mapData, err := base64.StdEncoding.DecodeString(string(match[1]))
		if err != nil {
			return fmt.Errorf("decode inline source map in %s: %w", name, err)
		}
		if stripSources {
			if stripped, changed, stripErr := StripSourceMapSourcesContent(mapData); stripErr != nil {
				return fmt.Errorf("strip inline source map in %s: %w", name, stripErr)
			} else if changed {
				mapData = stripped
			}
		}
		rel, err := filepath.Rel(extractedRoot, name)
		if err != nil {
			return err
		}
		dest := filepath.Join(e.mapsStage, rel+".map")
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, mapData, 0o644); err != nil {
			return err
		}
		rewritten, _ := RemoveSourceMappingURL(data)
		return os.WriteFile(name, rewritten, 0o644)
	})
}

func (e *externalArtifacts) commit() error {
	if e.licenseFile != nil {
		if err := e.licenseFile.Sync(); err != nil {
			return err
		}
		if err := e.licenseFile.Close(); err != nil {
			return err
		}
		e.licenseFile = nil
	}
	if e.mapsStage != "" {
		if err := os.Rename(e.mapsStage, e.mapsDest); err != nil {
			return fmt.Errorf("commit external source maps: %w", err)
		}
		e.mapsStage = ""
		e.committedMaps = true
	}
	if e.licenseTemp != "" {
		if err := os.Rename(e.licenseTemp, e.licensesDest); err != nil {
			e.rollback()
			return fmt.Errorf("commit external licenses: %w", err)
		}
		e.licenseTemp = ""
		e.committedLicenses = true
	}
	return nil
}

func (e *externalArtifacts) rollback() {
	if e.committedMaps {
		_ = os.RemoveAll(e.mapsDest)
		e.committedMaps = false
	}
	if e.committedLicenses {
		_ = os.Remove(e.licensesDest)
		e.committedLicenses = false
	}
}

func (e *externalArtifacts) cleanup() {
	if e == nil {
		return
	}
	if e.licenseFile != nil {
		_ = e.licenseFile.Close()
	}
	if e.mapsStage != "" {
		_ = os.RemoveAll(e.mapsStage)
	}
	if e.licenseTemp != "" {
		_ = os.Remove(e.licenseTemp)
	}
}
