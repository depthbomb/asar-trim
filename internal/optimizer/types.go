package optimizer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type Profile string

const (
	ProfileSafe       Profile = "safe"
	ProfileBalanced   Profile = "balanced"
	ProfileAggressive Profile = "aggressive"
)

type Options struct {
	Profile                    Profile
	KeepPatterns               []string
	RemovePatterns             []string
	MinifyJSON                 bool
	PrunePackage               bool
	HintFile                   string
	GenerateHint               string
	HintEntries                []string
	AccessLogs                 []string
	TargetPlatform             string
	TargetArch                 string
	TargetLibc                 string
	Locales                    []string
	BundlerMetadata            []string
	Output                     string
	BackupPath                 string
	WorkDir                    string
	Backup                     bool
	ForceBackup                bool
	ForceHint                  bool
	Verify                     bool
	KeepWorkDir                bool
	PruneIncompatible          bool
	PruneExtraneous            bool
	OmitOptional               bool
	RemoveUnread               bool
	TrimElectronLocales        bool
	StripSourceMapSources      bool
	ExternalizeSourceMaps      string
	ExternalizeLicenses        string
	MinifyJS                   bool
	MinifyCSS                  bool
	MinifyHTML                 bool
	MinifySVG                  bool
	SmartUnpack                bool
	RemoveEmptyDirs            bool
	DangerouslyRemoveLicenses  bool
	LicenseRemovalAcknowledged bool
	WindowsCompress            bool
}

type HintOptions struct {
	Output      string
	EntryPoints []string
	AccessLogs  []string
	Force       bool
}

type HintReport struct {
	Archive     string   `json:"archive"`
	Output      string   `json:"output"`
	Paths       int      `json:"paths"`
	Coverage    float64  `json:"coverage"`
	EntryPoints []string `json:"entry_points,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

func (r HintReport) WriteText(w io.Writer) error {
	_, err := fmt.Fprintf(w, "Archive: %s\nHint: %s\nOrdered paths: %d\nArchive coverage: %.1f%%\n", r.Archive, r.Output, r.Paths, r.Coverage*100)
	if err != nil {
		return err
	}
	for _, warning := range r.Warnings {
		if _, err = fmt.Fprintf(w, "Warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}

func (r HintReport) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

type Action string

const (
	ActionKeep    Action = "keep"
	ActionRemove  Action = "remove"
	ActionRewrite Action = "rewrite"
	ActionDedup   Action = "deduplicate"
)

type Decision struct {
	Action Action `json:"action"`
	Reason string `json:"reason,omitempty"`
	Risk   string `json:"risk,omitempty"`
}

type Finding struct {
	Path   string `json:"path"`
	Action Action `json:"action"`
	Reason string `json:"reason"`
	Risk   string `json:"risk"`
	Before int64  `json:"before_bytes"`
	After  int64  `json:"after_bytes"`
}

type Recommendation struct {
	Kind           string   `json:"kind"`
	Message        string   `json:"message"`
	Paths          []string `json:"paths,omitempty"`
	EstimatedBytes int64    `json:"estimated_bytes,omitempty"`
}

type Report struct {
	Archive            string           `json:"archive"`
	Output             string           `json:"output,omitempty"`
	Profile            Profile          `json:"profile"`
	Entries            int              `json:"entries"`
	PackedFiles        int              `json:"packed_files"`
	UnpackedFiles      int              `json:"unpacked_files"`
	ExecutableFiles    int              `json:"executable_files"`
	ArchiveBytes       int64            `json:"archive_bytes"`
	UnpackedBytes      int64            `json:"unpacked_bytes"`
	EstimatedSavings   int64            `json:"estimated_savings_bytes"`
	ActualSavings      int64            `json:"actual_savings_bytes,omitempty"`
	DuplicateSavings   int64            `json:"duplicate_savings_bytes"`
	HintCoverage       float64          `json:"hint_coverage,omitempty"`
	OriginalHeaderHash string           `json:"original_header_sha256,omitempty"`
	OutputHeaderHash   string           `json:"output_header_sha256,omitempty"`
	Findings           []Finding        `json:"findings,omitempty"`
	Warnings           []string         `json:"warnings,omitempty"`
	Recommendations    []Recommendation `json:"recommendations,omitempty"`
	UnreachableFiles   int              `json:"unreachable_files,omitempty"`
	UnreachableBytes   int64            `json:"unreachable_bytes,omitempty"`
	WindowsCompression string           `json:"windows_compression,omitempty"`
	Optimized          bool             `json:"optimized"`
}

func (r Report) WriteText(w io.Writer) error {
	_, err := fmt.Fprintf(w, "Archive: %s\nProfile: %s\nEntries: %d (%d packed, %d unpacked)\nCurrent size: %s\nPotential savings: %s\n",
		r.Archive, r.Profile, r.Entries, r.PackedFiles, r.UnpackedFiles,
		formatBytes(r.ArchiveBytes+r.UnpackedBytes), formatBytes(r.EstimatedSavings))
	if err != nil {
		return err
	}
	if r.Optimized {
		if _, err = fmt.Fprintf(w, "Output: %s\nActual savings: %s\n", r.Output, formatBytes(r.ActualSavings)); err != nil {
			return err
		}
	}
	for _, f := range r.Findings {
		if _, err = fmt.Fprintf(w, "  %-7s %10s  %s (%s)\n", f.Action, formatBytes(f.Before-f.After), f.Path, f.Reason); err != nil {
			return err
		}
	}
	for _, warning := range r.Warnings {
		if _, err = fmt.Fprintf(w, "Warning: %s\n", warning); err != nil {
			return err
		}
	}
	for _, recommendation := range r.Recommendations {
		if _, err = fmt.Fprintf(w, "Recommendation [%s]: %s", recommendation.Kind, recommendation.Message); err != nil {
			return err
		}
		if recommendation.EstimatedBytes > 0 {
			if _, err = fmt.Fprintf(w, " (up to %s)", formatBytes(recommendation.EstimatedBytes)); err != nil {
				return err
			}
		}
		if _, err = fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n >= div*unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func normalizeOptions(opts Options) (Options, error) {
	if opts.Profile == "" {
		opts.Profile = ProfileSafe
	}
	switch opts.Profile {
	case ProfileSafe, ProfileBalanced, ProfileAggressive:
	default:
		return opts, fmt.Errorf("unknown profile %q (want safe, balanced, or aggressive)", opts.Profile)
	}
	if opts.GenerateHint != "" && opts.HintFile != "" {
		return opts, errors.New("generated hint and existing hint file cannot be used together")
	}
	if opts.DangerouslyRemoveLicenses && opts.ExternalizeLicenses != "" {
		return opts, errors.New("dangerous license removal and license externalization cannot be used together")
	}
	if opts.DangerouslyRemoveLicenses && !opts.LicenseRemovalAcknowledged {
		return opts, errors.New("dangerous license removal requires explicit acknowledgement")
	}
	if opts.RemoveUnread && len(opts.AccessLogs) == 0 {
		return opts, errors.New("remove-unread requires at least one access log")
	}
	if (opts.PruneIncompatible || opts.TargetArch != "" || opts.TargetLibc != "") && opts.TargetPlatform == "" {
		return opts, errors.New("target-platform is required for target-aware pruning")
	}
	if opts.TrimElectronLocales && len(opts.Locales) == 0 {
		return opts, errors.New("trim-electron-locales requires at least one locale")
	}
	for i := range opts.KeepPatterns {
		opts.KeepPatterns[i] = strings.ReplaceAll(opts.KeepPatterns[i], "\\", "/")
	}
	for i := range opts.RemovePatterns {
		opts.RemovePatterns[i] = strings.ReplaceAll(opts.RemovePatterns[i], "\\", "/")
	}
	return opts, nil
}
