// Package app defines the asar-trim command-line interface.
package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/depthbomb/asar-trim/internal/optimizer"
	"github.com/urfave/cli/v3"
)

const defaultProfile = "safe"

// Engine is the optimizer surface used by the CLI. It is intentionally small
// so command parsing and output can be tested without rewriting an archive.
type Engine interface {
	Analyze(input string, opts optimizer.Options) (optimizer.Report, error)
	Optimize(input string, opts optimizer.Options) (optimizer.Report, error)
	GenerateHint(input string, opts optimizer.HintOptions) (optimizer.HintReport, error)
}

type optimizerEngine struct{}

func (optimizerEngine) Analyze(input string, opts optimizer.Options) (optimizer.Report, error) {
	return optimizer.Analyze(input, opts)
}

func (optimizerEngine) Optimize(input string, opts optimizer.Options) (optimizer.Report, error) {
	return optimizer.Optimize(input, opts)
}

func (optimizerEngine) GenerateHint(input string, opts optimizer.HintOptions) (optimizer.HintReport, error) {
	return optimizer.GenerateHint(input, opts)
}

// New returns the root asar-trim command.
func New(version string) *cli.Command {
	return NewWithEngine(version, optimizerEngine{})
}

// NewWithEngine returns a root command backed by engine. It is primarily
// useful to embedders and tests.
func NewWithEngine(version string, engine Engine) *cli.Command {
	if engine == nil {
		panic("app: nil optimizer engine")
	}

	return &cli.Command{
		Name:                   "asar-trim",
		Usage:                  "analyze and optimize Electron ASAR archives",
		Version:                version,
		EnableShellCompletion:  true,
		UseShortOptionHandling: true,
		// Glob patterns can legitimately contain commas in brace alternatives.
		DisableSliceFlagSeparator: true,
		Commands: []*cli.Command{
			analyzeCommand(engine),
			hintCommand(engine),
			optimizeCommand(engine),
		},
	}
}

func hintCommand(engine Engine) *cli.Command {
	return &cli.Command{
		Name:      "hint",
		Usage:     "generate a startup ordering hint from static module references",
		ArgsUsage: "<archive-or-resources-directory>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "write the generated hint to `FILE`", Required: true},
			&cli.StringSliceFlag{Name: "entry", Usage: "add startup entry point `PATH` (repeatable)"},
			&cli.StringSliceFlag{Name: "access-log", Usage: "merge Electron ASAR read log `FILE` (repeatable)"},
			&cli.BoolFlag{Name: "force", Usage: "replace an existing hint file"},
			&cli.StringFlag{Name: "format", Usage: "report format: text or json", Value: "text"},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			input, err := singleInput(cmd)
			if err != nil {
				return err
			}
			format, err := reportFormat(cmd)
			if err != nil {
				return err
			}
			report, err := engine.GenerateHint(input, optimizer.HintOptions{Output: cmd.String("output"), EntryPoints: cmd.StringSlice("entry"), AccessLogs: cmd.StringSlice("access-log"), Force: cmd.Bool("force")})
			if err != nil {
				return fmt.Errorf("generate hint for %q: %w", input, err)
			}
			return writeHintReport(cmd.Writer, format, report)
		},
	}
}

func analyzeCommand(engine Engine) *cli.Command {
	return &cli.Command{
		Name:                      "analyze",
		Aliases:                   []string{"inspect"},
		Usage:                     "show optimization opportunities without changing the archive",
		ArgsUsage:                 "<archive-or-resources-directory>",
		DisableSliceFlagSeparator: true,
		Flags: append(policyFlags(),
			&cli.StringFlag{
				Name:  "format",
				Usage: "report format: text or json",
				Value: "text",
			},
		),
		Action: func(_ context.Context, cmd *cli.Command) error {
			input, err := singleInput(cmd)
			if err != nil {
				return err
			}
			opts, err := optionsFromCommand(cmd)
			if err != nil {
				return err
			}
			report, err := engine.Analyze(input, opts)
			if err != nil {
				return fmt.Errorf("analyze %q: %w", input, err)
			}
			if err := writeRecommendationsFile(cmd.String("write-recommendations"), cmd.Bool("force-recommendations"), report); err != nil {
				return err
			}
			return writeReport(cmd.Writer, cmd.String("format"), report)
		},
	}
}

func optimizeCommand(engine Engine) *cli.Command {
	return &cli.Command{
		Name:                      "optimize",
		Aliases:                   []string{"trim"},
		Usage:                     "rewrite an archive using the selected optimization policy",
		ArgsUsage:                 "<archive-or-resources-directory>",
		DisableSliceFlagSeparator: true,
		Flags: append(policyFlags(),
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "write to `FILE` instead of replacing the input archive",
			},
			&cli.BoolFlag{
				Name:    "backup",
				Aliases: []string{"b"},
				Usage:   "back up the archive before replacing it",
			},
			&cli.StringFlag{
				Name:  "backup-path",
				Usage: "write the backup to `FILE` (implies --backup)",
			},
			&cli.BoolFlag{
				Name:  "force-backup",
				Usage: "replace an existing backup",
			},
			&cli.StringFlag{
				Name:  "work-dir",
				Usage: "use `DIRECTORY` for temporary extracted files",
			},
			&cli.BoolFlag{
				Name:  "keep-work-dir",
				Usage: "keep extracted working files after optimization",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "analyze only; do not rewrite the archive",
			},
			&cli.StringFlag{
				Name:  "generate-hint",
				Usage: "generate a startup hint at `FILE` and use it for this rewrite",
			},
			&cli.StringSliceFlag{
				Name:  "hint-entry",
				Usage: "add startup entry point `PATH` when generating a hint (repeatable)",
			},
			&cli.BoolFlag{
				Name:  "force-hint",
				Usage: "replace an existing generated hint file",
			},
			&cli.BoolFlag{
				Name:  "dangerously-remove-licenses",
				Usage: "remove license files and common license headers after interactive acknowledgement",
			},
			&cli.BoolFlag{Name: "windows-compress", Usage: "on Windows, apply WOF LZX compression with NTFS compression fallback"},
			&cli.StringFlag{
				Name:  "format",
				Usage: "report format: text or json",
				Value: "text",
			},
		),
		Action: func(_ context.Context, cmd *cli.Command) error {
			input, err := singleInput(cmd)
			if err != nil {
				return err
			}
			opts, err := optionsFromCommand(cmd)
			if err != nil {
				return err
			}
			opts.Output = cmd.String("output")
			opts.Backup = cmd.Bool("backup") || cmd.String("backup-path") != ""
			opts.BackupPath = cmd.String("backup-path")
			opts.ForceBackup = cmd.Bool("force-backup")
			opts.Verify = cmd.Bool("verify")
			opts.WorkDir = cmd.String("work-dir")
			opts.KeepWorkDir = cmd.Bool("keep-work-dir")
			opts.GenerateHint = cmd.String("generate-hint")
			opts.HintEntries = cmd.StringSlice("hint-entry")
			opts.ForceHint = cmd.Bool("force-hint")
			opts.DangerouslyRemoveLicenses = cmd.Bool("dangerously-remove-licenses")
			opts.WindowsCompress = cmd.Bool("windows-compress")
			if opts.DangerouslyRemoveLicenses && opts.ExternalizeLicenses != "" {
				return fmt.Errorf("--dangerously-remove-licenses and --externalize-licenses cannot be used together")
			}
			if opts.GenerateHint != "" && opts.HintFile != "" {
				return fmt.Errorf("--generate-hint and --hint-file cannot be used together")
			}
			if cmd.Bool("dry-run") && opts.GenerateHint != "" {
				return fmt.Errorf("--generate-hint cannot be used with --dry-run; use the hint command instead")
			}
			if cmd.Bool("dry-run") && opts.WindowsCompress {
				return fmt.Errorf("--windows-compress cannot be used with --dry-run")
			}
			if opts.DangerouslyRemoveLicenses {
				if cmd.Bool("dry-run") {
					return fmt.Errorf("--dangerously-remove-licenses cannot be used with --dry-run")
				}
				if err := confirmDangerousLicenseRemoval(cmd); err != nil {
					return err
				}
				opts.LicenseRemovalAcknowledged = true
			}

			var report optimizer.Report
			if cmd.Bool("dry-run") {
				report, err = engine.Analyze(input, opts)
			} else {
				report, err = engine.Optimize(input, opts)
			}
			if err != nil {
				return fmt.Errorf("optimize %q: %w", input, err)
			}
			if err := writeRecommendationsFile(cmd.String("write-recommendations"), cmd.Bool("force-recommendations"), report); err != nil {
				return err
			}
			return writeReport(cmd.Writer, cmd.String("format"), report)
		},
	}
}

func policyFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "profile",
			Aliases: []string{"p"},
			Usage:   "policy profile: safe, balanced, or aggressive",
			Value:   defaultProfile,
		},
		&cli.StringSliceFlag{
			Name:  "keep",
			Usage: "keep paths matching `GLOB` (repeatable)",
		},
		&cli.StringSliceFlag{
			Name:  "remove",
			Usage: "remove paths matching `GLOB` (repeatable)",
		},
		&cli.BoolFlag{
			Name:  "minify-json",
			Usage: "compact valid JSON files",
			Value: true,
		},
		&cli.BoolFlag{
			Name:  "prune-package",
			Usage: "remove profile-approved metadata from package.json files",
			Value: true,
		},
		&cli.StringFlag{
			Name:  "hint-file",
			Usage: "order archive entries using Electron/Atom hint `FILE`",
		},
		&cli.BoolFlag{
			Name:  "verify",
			Usage: "verify integrity metadata before processing",
			Value: true,
		},
		&cli.StringSliceFlag{Name: "access-log", Usage: "use Electron ASAR read log `FILE` (repeatable)"},
		&cli.StringFlag{Name: "target-platform", Usage: "target Node platform such as win32, darwin, or linux"},
		&cli.StringFlag{Name: "target-arch", Usage: "target architecture such as x64, arm64, or ia32"},
		&cli.StringFlag{Name: "target-libc", Usage: "target Linux libc such as glibc or musl"},
		&cli.StringSliceFlag{Name: "locale", Usage: "retain locale `NAME` (repeatable)"},
		&cli.BoolFlag{Name: "prune-incompatible", Usage: "remove packages and native variants proven incompatible with the explicit target"},
		&cli.BoolFlag{Name: "prune-extraneous", Usage: "remove package trees not reachable from runtime dependencies"},
		&cli.BoolFlag{Name: "omit-optional", Usage: "remove optional dependency trees"},
		&cli.BoolFlag{Name: "remove-unread", Usage: "remove files absent from all supplied runtime access logs"},
		&cli.BoolFlag{Name: "trim-electron-locales", Usage: "trim resources/locales using explicit --locale values"},
		&cli.BoolFlag{Name: "strip-sourcemap-sources", Usage: "remove embedded sourcesContent from retained source maps"},
		&cli.StringFlag{Name: "externalize-source-maps", Usage: "move source maps into `DIRECTORY` instead of packing them"},
		&cli.StringFlag{Name: "externalize-licenses", Usage: "consolidate license files into `FILE` and remove their archive copies"},
		&cli.BoolFlag{Name: "minify-js", Usage: "conservatively compact JavaScript horizontal whitespace (opt-in)"},
		&cli.BoolFlag{Name: "minify-css", Usage: "conservatively compact CSS horizontal whitespace (opt-in)"},
		&cli.BoolFlag{Name: "minify-html", Usage: "minify HTML whitespace and comments (opt-in)"},
		&cli.BoolFlag{Name: "minify-svg", Usage: "minify SVG whitespace and comments (opt-in)"},
		&cli.BoolFlag{Name: "smart-unpack", Usage: "unpack native modules and executable payloads"},
		&cli.BoolFlag{Name: "remove-empty-dirs", Usage: "omit empty directories after trimming"},
		&cli.StringSliceFlag{Name: "bundler-metadata", Usage: "ingest esbuild metafile or Webpack stats `FILE` (repeatable)"},
		&cli.StringFlag{Name: "write-recommendations", Usage: "write structured packaging recommendations to JSON `FILE`"},
		&cli.BoolFlag{Name: "force-recommendations", Usage: "replace an existing recommendations file"},
	}
}

func writeRecommendationsFile(name string, force bool, report optimizer.Report) error {
	if name == "" {
		return nil
	}
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(name, flags, 0o644)
	if err != nil {
		return fmt.Errorf("create recommendations file: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	writeErr := enc.Encode(struct {
		Archive         string                     `json:"archive"`
		Recommendations []optimizer.Recommendation `json:"recommendations"`
	}{Archive: report.Archive, Recommendations: report.Recommendations})
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write recommendations file: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close recommendations file: %w", closeErr)
	}
	return nil
}

func optionsFromCommand(cmd *cli.Command) (optimizer.Options, error) {
	profile := optimizer.Profile(strings.ToLower(cmd.String("profile")))
	switch profile {
	case optimizer.ProfileSafe, optimizer.ProfileBalanced, optimizer.ProfileAggressive:
	default:
		return optimizer.Options{}, fmt.Errorf("invalid profile %q (want safe, balanced, or aggressive)", cmd.String("profile"))
	}
	if _, err := reportFormat(cmd); err != nil {
		return optimizer.Options{}, err
	}
	opts := optimizer.Options{
		Profile:               profile,
		KeepPatterns:          cmd.StringSlice("keep"),
		RemovePatterns:        cmd.StringSlice("remove"),
		MinifyJSON:            cmd.Bool("minify-json"),
		PrunePackage:          cmd.Bool("prune-package"),
		HintFile:              cmd.String("hint-file"),
		Verify:                cmd.Bool("verify"),
		AccessLogs:            cmd.StringSlice("access-log"),
		TargetPlatform:        strings.ToLower(cmd.String("target-platform")),
		TargetArch:            strings.ToLower(cmd.String("target-arch")),
		TargetLibc:            strings.ToLower(cmd.String("target-libc")),
		Locales:               cmd.StringSlice("locale"),
		PruneIncompatible:     cmd.Bool("prune-incompatible"),
		PruneExtraneous:       cmd.Bool("prune-extraneous"),
		OmitOptional:          cmd.Bool("omit-optional"),
		RemoveUnread:          cmd.Bool("remove-unread"),
		TrimElectronLocales:   cmd.Bool("trim-electron-locales"),
		StripSourceMapSources: cmd.Bool("strip-sourcemap-sources"),
		ExternalizeSourceMaps: cmd.String("externalize-source-maps"),
		ExternalizeLicenses:   cmd.String("externalize-licenses"),
		MinifyJS:              cmd.Bool("minify-js"),
		MinifyCSS:             cmd.Bool("minify-css"),
		MinifyHTML:            cmd.Bool("minify-html"),
		MinifySVG:             cmd.Bool("minify-svg"),
		SmartUnpack:           cmd.Bool("smart-unpack"),
		RemoveEmptyDirs:       cmd.Bool("remove-empty-dirs"),
		BundlerMetadata:       cmd.StringSlice("bundler-metadata"),
	}
	if opts.RemoveUnread && len(opts.AccessLogs) == 0 {
		return optimizer.Options{}, fmt.Errorf("--remove-unread requires at least one --access-log")
	}
	if (opts.PruneIncompatible || opts.TargetArch != "" || opts.TargetLibc != "") && opts.TargetPlatform == "" {
		return optimizer.Options{}, fmt.Errorf("--target-platform is required for target-aware pruning")
	}
	if opts.TrimElectronLocales && len(opts.Locales) == 0 {
		return optimizer.Options{}, fmt.Errorf("--trim-electron-locales requires at least one --locale")
	}
	return opts, nil
}

const dangerousLicenseAcknowledgement = "REMOVE LICENSES"

func confirmDangerousLicenseRemoval(cmd *cli.Command) error {
	if _, err := fmt.Fprintf(cmd.ErrWriter, "WARNING: Removing license files and license header comments may violate distribution terms.\nOnly use this on an application you installed for your own private use, never on an application you plan to distribute.\nType %q to continue: ", dangerousLicenseAcknowledgement); err != nil {
		return err
	}
	line, err := bufio.NewReader(cmd.Reader).ReadString('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("license removal acknowledgement was not provided: %w", err)
	}
	if strings.TrimSpace(line) != dangerousLicenseAcknowledgement {
		return fmt.Errorf("license removal cancelled: acknowledgement did not match %q", dangerousLicenseAcknowledgement)
	}
	return nil
}

func reportFormat(cmd *cli.Command) (string, error) {
	format := strings.ToLower(cmd.String("format"))
	if format != "text" && format != "json" {
		return "", fmt.Errorf("invalid format %q (want text or json)", cmd.String("format"))
	}
	return format, nil
}

func singleInput(cmd *cli.Command) (string, error) {
	if cmd.NArg() != 1 {
		return "", fmt.Errorf("expected exactly one archive or resources directory, got %d", cmd.NArg())
	}
	return cmd.Args().First(), nil
}

func writeReport(w io.Writer, format string, report optimizer.Report) error {
	if strings.EqualFold(format, "json") {
		if err := report.WriteJSON(w); err != nil {
			return fmt.Errorf("write JSON report: %w", err)
		}
		return nil
	}
	if err := report.WriteText(w); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func writeHintReport(w io.Writer, format string, report optimizer.HintReport) error {
	if strings.EqualFold(format, "json") {
		if err := report.WriteJSON(w); err != nil {
			return fmt.Errorf("write JSON report: %w", err)
		}
		return nil
	}
	if err := report.WriteText(w); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}
