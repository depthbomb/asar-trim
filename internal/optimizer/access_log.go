package optimizer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	archivefs "github.com/depthbomb/asar-trim/internal/archive"
	asar "github.com/depthbomb/go-asar"
)

// AccessLogReport describes the useful and unknown paths found by merging
// Electron ELECTRON_LOG_ASAR_READS output. Paths retain first-access order.
type AccessLogReport struct {
	Archive  string   `json:"archive"`
	Logs     []string `json:"logs"`
	Paths    []string `json:"paths"`
	Unknown  []string `json:"unknown,omitempty"`
	Coverage float64  `json:"coverage"`
}

// AnalyzeAccessLogs merges Electron ASAR access logs, removes duplicates, and
// filters paths that do not exist in the selected archive.
func AnalyzeAccessLogs(input string, logFiles []string) (AccessLogReport, error) {
	pair, err := archivefs.Resolve(input)
	if err != nil {
		return AccessLogReport{}, err
	}
	if len(logFiles) == 0 {
		return AccessLogReport{}, errors.New("at least one ASAR access log is required")
	}
	a, err := asar.Open(pair.ArchivePath)
	if err != nil {
		return AccessLogReport{}, err
	}
	defer a.Close()

	files := make(map[string]bool)
	allPaths := make([]string, 0, len(a.Entries()))
	for _, entry := range a.Entries() {
		if !entry.IsDir() {
			files[entry.Path] = true
			allPaths = append(allPaths, entry.Path)
		}
	}
	report := AccessLogReport{Archive: pair.ArchivePath}
	seen, unknownSeen := make(map[string]bool), make(map[string]bool)
	for _, logFile := range logFiles {
		absolute, err := filepath.Abs(logFile)
		if err != nil {
			return AccessLogReport{}, err
		}
		f, err := os.Open(absolute)
		if err != nil {
			return AccessLogReport{}, fmt.Errorf("open access log %q: %w", absolute, err)
		}
		hint, parseErr := asar.ParseHint(f)
		closeErr := f.Close()
		if parseErr != nil {
			return AccessLogReport{}, fmt.Errorf("parse access log %q: %w", absolute, parseErr)
		}
		if closeErr != nil {
			return AccessLogReport{}, closeErr
		}
		report.Logs = append(report.Logs, absolute)
		for _, raw := range hint.Paths {
			name := normalizeAccessLogPath(raw)
			if name == "" {
				continue
			}
			if files[name] {
				if !seen[name] {
					seen[name] = true
					report.Paths = append(report.Paths, name)
				}
			} else if !unknownSeen[name] {
				unknownSeen[name] = true
				report.Unknown = append(report.Unknown, name)
			}
		}
	}
	report.Coverage = (&asar.Hint{Paths: report.Paths}).Coverage(allPaths)
	return report, nil
}

// GenerateAccessLogHint writes a canonical go-asar ordering hint from one or
// more Electron access logs and returns both the normal hint report and trace
// diagnostics (including paths absent from the archive).
func GenerateAccessLogHint(input string, logFiles []string, output string, force bool) (HintReport, AccessLogReport, error) {
	trace, err := AnalyzeAccessLogs(input, logFiles)
	if err != nil {
		return HintReport{}, AccessLogReport{}, err
	}
	if len(trace.Paths) == 0 {
		return HintReport{}, trace, errors.New("access logs contain no paths present in the archive")
	}
	out, err := filepath.Abs(output)
	if err != nil {
		return HintReport{}, trace, err
	}
	if output == "" {
		return HintReport{}, trace, errors.New("hint output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return HintReport{}, trace, err
	}
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(out, flags, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return HintReport{}, trace, fmt.Errorf("hint output %q already exists", out)
		}
		return HintReport{}, trace, err
	}
	_, writeErr := (&asar.Hint{Paths: trace.Paths}).WriteTo(f)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(out)
		if writeErr != nil {
			return HintReport{}, trace, writeErr
		}
		return HintReport{}, trace, closeErr
	}
	report := HintReport{Archive: trace.Archive, Output: out, Paths: len(trace.Paths), Coverage: trace.Coverage}
	if len(trace.Unknown) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("ignored %d access-log paths absent from the archive", len(trace.Unknown)))
	}
	return report, trace, nil
}

func normalizeAccessLogPath(raw string) string {
	name := strings.Trim(strings.TrimSpace(raw), `"'`)
	name = strings.ReplaceAll(name, `\`, "/")
	lower := strings.ToLower(name)
	if index := strings.LastIndex(lower, ".asar/"); index >= 0 {
		name = name[index+len(".asar/"):]
	}
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimPrefix(name, "./")
	return name
}
