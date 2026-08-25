package optimizer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	archivefs "github.com/depthbomb/asar-trim/internal/archive"
	asar "github.com/depthbomb/go-asar"
)

const integrityWarning = "rewriting changes the ASAR header hash; update Electron ASAR-integrity metadata and platform signatures if the application enforces them"

func Analyze(input string, opts Options) (Report, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return Report{}, err
	}
	pair, err := archivefs.Resolve(input)
	if err != nil {
		return Report{}, err
	}
	p, err := newPolicy(opts)
	if err != nil {
		return Report{}, err
	}
	report, err := analyzePair(pair, opts, p)
	if err != nil {
		return Report{}, err
	}
	localePlan, err := resourceLocalePlan(input, opts)
	if err != nil {
		return Report{}, err
	}
	appendResourceLocaleRecommendation(&report, localePlan)
	return report, nil
}

func analyzePair(pair archivefs.Pair, opts Options, p *policy) (Report, error) {
	a, err := asar.Open(pair.ArchivePath)
	if err != nil {
		return Report{}, err
	}
	defer a.Close()
	if opts.Verify {
		if err := a.VerifyAll(); err != nil {
			return Report{}, fmt.Errorf("verify input: %w", err)
		}
	}
	stat, err := os.Stat(pair.ArchivePath)
	if err != nil {
		return Report{}, err
	}
	headerHash := a.HeaderHash()
	report := Report{
		Archive:            pair.ArchivePath,
		Profile:            opts.Profile,
		ArchiveBytes:       stat.Size(),
		OriginalHeaderHash: hex.EncodeToString(headerHash[:]),
		Warnings:           []string{integrityWarning},
	}
	report.UnpackedBytes, err = directoryBytes(pair.UnpackedPath)
	if err != nil {
		return Report{}, err
	}
	entries := a.Entries()
	opportunities, opportunityErr := AnalyzeOpportunities(pair.ArchivePath, OpportunityOptions{
		Target:            Target{Platform: opts.TargetPlatform, Arch: opts.TargetArch, Libc: opts.TargetLibc},
		AuditDependencies: true,
		ScanJunk:          true,
		PruneLocales:      len(opts.Locales) > 0,
		Locales:           opts.Locales,
		PruneExtraneous:   opts.PruneExtraneous,
		OmitOptional:      opts.OmitOptional,
	})
	if opportunityErr != nil {
		return Report{}, opportunityErr
	}
	selectedCandidates := make(map[string]RemovalCandidate)
	var targetCandidatePaths []string
	var targetCandidateBytes int64
	for _, candidate := range opportunities.Candidates {
		if (candidate.Category == "incompatible-package" || candidate.Category == "foreign-native-prebuild") && !opts.PruneIncompatible {
			targetCandidatePaths = append(targetCandidatePaths, candidate.Path)
			targetCandidateBytes += candidate.Bytes
			continue
		}
		selectedCandidates[candidate.Path] = candidate
	}
	if len(targetCandidatePaths) > 0 {
		report.Recommendations = append(report.Recommendations, Recommendation{Kind: "incompatible-target", Message: "files are provably incompatible with the explicit target; pass --prune-incompatible to remove them", Paths: targetCandidatePaths, EstimatedBytes: targetCandidateBytes})
	}
	report.Warnings = append(report.Warnings, opportunities.Warnings...)
	for _, missing := range opportunities.MissingRuntime {
		report.Warnings = append(report.Warnings, "missing runtime dependency: "+missing)
	}
	for _, issue := range opportunities.PackageIssues {
		if issue.Kind == "extraneous" && opts.PruneExtraneous {
			continue
		}
		report.Recommendations = append(report.Recommendations, Recommendation{Kind: "package-" + issue.Kind, Message: issue.Detail, Paths: []string{issue.Root}, EstimatedBytes: issue.Bytes})
	}
	for _, duplicate := range opportunities.DuplicatePackages {
		report.Recommendations = append(report.Recommendations, Recommendation{Kind: "duplicate-package", Message: fmt.Sprintf("%s@%s is installed %d times; deduplicate with the package manager before packaging", duplicate.Name, duplicate.Version, len(duplicate.Roots)), Paths: duplicate.Roots, EstimatedBytes: duplicate.RecoverableBytes})
	}
	type dedupKey struct {
		size int64
		hash [sha256.Size]byte
	}
	seenContent := make(map[dedupKey]map[int64]struct{})
	archivePaths := make(map[string]int64)
	javascriptFiles := 0
	readPaths := make(map[string]bool)
	if len(opts.AccessLogs) > 0 {
		trace, traceErr := AnalyzeAccessLogs(pair.ArchivePath, opts.AccessLogs)
		if traceErr != nil {
			return Report{}, traceErr
		}
		for _, name := range trace.Paths {
			readPaths[name] = true
		}
		report.HintCoverage = trace.Coverage
		if len(trace.Unknown) > 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("ignored %d access-log paths absent from the archive", len(trace.Unknown)))
		}
	}
	report.Entries = len(entries)
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
		if entry.IsDir() || entry.Link != "" {
			continue
		}
		archivePaths[entry.Path] = entry.Size
		switch strings.ToLower(filepath.Ext(entry.Path)) {
		case ".js", ".mjs", ".cjs":
			javascriptFiles++
		}
		if entry.Unpacked {
			report.UnpackedFiles++
		} else {
			report.PackedFiles++
		}
		if entry.Executable {
			report.ExecutableFiles++
		}
		decision := p.decide(entry.Path)
		if candidate, ok := selectedCandidates[entry.Path]; ok && decision.Action == ActionKeep && decision.Reason == "" {
			decision = Decision{Action: ActionRemove, Reason: candidate.Reason, Risk: candidate.Risk}
		}
		if len(opts.AccessLogs) > 0 && !readPaths[entry.Path] {
			report.UnreachableFiles++
			report.UnreachableBytes += entry.Size
			if opts.RemoveUnread && decision.Action == ActionKeep && decision.Reason == "" {
				decision = Decision{Action: ActionRemove, Reason: "absent from supplied runtime access logs", Risk: "high"}
			}
		}
		if decision.Action == ActionRemove {
			report.Findings = append(report.Findings, Finding{Path: entry.Path, Action: ActionRemove, Reason: decision.Reason, Risk: decision.Risk, Before: entry.Size})
			report.EstimatedSavings += entry.Size
			continue
		}
		needsTransform := opts.MinifyJSON || opts.StripSourceMapSources || opts.MinifyJS || opts.MinifyCSS || opts.MinifyHTML || opts.MinifySVG || opts.DangerouslyRemoveLicenses || (opts.PrunePackage && strings.EqualFold(filepath.Base(entry.Path), "package.json"))
		var data []byte
		if needsTransform || !entry.Unpacked {
			var readErr error
			data, readErr = a.ReadFile(entry.Path)
			if readErr != nil {
				return Report{}, fmt.Errorf("read %s: %w", entry.Path, readErr)
			}
		}
		if needsTransform {
			transformed, reason, transformErr := p.transform(entry.Path, data)
			if transformErr != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", entry.Path, transformErr))
				continue
			}
			if reason != "" && len(transformed) < len(data) {
				saved := int64(len(data) - len(transformed))
				report.Findings = append(report.Findings, Finding{Path: entry.Path, Action: ActionRewrite, Reason: reason, Risk: "low", Before: int64(len(data)), After: int64(len(transformed))})
				report.EstimatedSavings += saved
				data = transformed
			}
		}
		if !entry.Unpacked {
			key := dedupKey{size: int64(len(data)), hash: sha256.Sum256(data)}
			offsets := seenContent[key]
			if offsets == nil {
				offsets = make(map[int64]struct{})
				seenContent[key] = offsets
			}
			if len(offsets) > 0 {
				if _, alreadyShared := offsets[entry.Offset]; !alreadyShared {
					report.DuplicateSavings += int64(len(data))
					report.EstimatedSavings += int64(len(data))
					report.Findings = append(report.Findings, Finding{Path: entry.Path, Action: ActionDedup, Reason: "identical packed content", Risk: "none", Before: int64(len(data))})
				}
			}
			offsets[entry.Offset] = struct{}{}
		}
	}
	if opts.HintFile != "" {
		hint, hintErr := asar.ReadHint(opts.HintFile)
		if hintErr != nil {
			return Report{}, fmt.Errorf("read hint file: %w", hintErr)
		}
		report.HintCoverage = hint.Coverage(paths)
	}
	if javascriptFiles > 500 {
		report.Recommendations = append(report.Recommendations, Recommendation{Kind: "bundling", Message: fmt.Sprintf("archive contains %d JavaScript modules; bundle and tree-shake main, preload, worker, and renderer entry points before packaging", javascriptFiles)})
	}
	metadataRecommendations, metadataErr := bundlerRecommendations(opts.BundlerMetadata, archivePaths)
	if metadataErr != nil {
		return Report{}, metadataErr
	}
	report.Recommendations = append(report.Recommendations, metadataRecommendations...)
	slices.SortFunc(report.Findings, func(a, b Finding) int { return strings.Compare(a.Path, b.Path) })
	return report, nil
}

func Optimize(input string, opts Options) (Report, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return Report{}, err
	}
	if opts.WindowsCompress && !windowsCompressionSupported() {
		return Report{}, fmt.Errorf("windows compression is only available on Windows")
	}
	source, err := archivefs.Resolve(input)
	if err != nil {
		return Report{}, err
	}
	sourceFingerprint, err := archivefs.PairFingerprint(source)
	if err != nil {
		return Report{}, fmt.Errorf("fingerprint source: %w", err)
	}
	if opts.Backup && opts.Output != "" {
		return Report{}, fmt.Errorf("backup is only valid for in-place optimization")
	}
	if opts.TrimElectronLocales && opts.Output != "" {
		return Report{}, fmt.Errorf("trim-electron-locales is only valid for in-place optimization")
	}
	hintStage, stagedHint, err := prepareGeneratedHint(source, opts)
	if err != nil {
		return Report{}, fmt.Errorf("generate hint: %w", err)
	}
	if hintStage != nil {
		defer hintStage.cleanup()
		opts.HintFile = stagedHint
		opts.GenerateHint = ""
	}
	p, err := newPolicy(opts)
	if err != nil {
		return Report{}, err
	}
	report, err := analyzePair(source, opts, p)
	if err != nil {
		return Report{}, err
	}
	localePlan, err := resourceLocalePlan(input, opts)
	if err != nil {
		return Report{}, err
	}
	appendResourceLocaleRecommendation(&report, localePlan)
	localeStage := newResourceLocaleStage(localePlan)
	external, err := newExternalArtifacts(opts)
	if err != nil {
		return Report{}, err
	}
	defer external.cleanup()

	if opts.Backup {
		if opts.BackupPath == "" {
			if _, err := archivefs.Backup(source, ".bak", opts.ForceBackup); err != nil {
				return Report{}, fmt.Errorf("backup: %w", err)
			}
		} else {
			dest, backupErr := backupPair(opts.BackupPath)
			if backupErr != nil {
				return Report{}, backupErr
			}
			if _, err := archivefs.BackupTo(source, dest, opts.ForceBackup); err != nil {
				return Report{}, fmt.Errorf("backup: %w", err)
			}
		}
	}

	work, err := os.MkdirTemp(opts.WorkDir, "asar-trim-work-*")
	if err != nil {
		return Report{}, err
	}
	if !opts.KeepWorkDir {
		defer os.RemoveAll(work)
	} else {
		report.Warnings = append(report.Warnings, "working directory retained at "+work)
	}
	extracted := filepath.Join(work, "contents")
	if err := asar.Extract(source.ArchivePath, extracted, asar.ExtractOptions{VerifyIntegrity: opts.Verify, PreserveSymlinks: true}); err != nil {
		return Report{}, fmt.Errorf("extract: %w", err)
	}
	if err := external.captureInlineSourceMaps(extracted, opts.StripSourceMapSources); err != nil {
		return Report{}, fmt.Errorf("externalize inline source maps: %w", err)
	}
	for _, finding := range report.Findings {
		target := filepath.Join(extracted, filepath.FromSlash(finding.Path))
		switch finding.Action {
		case ActionRemove:
			if err := external.captureRemoval(finding, target, extracted, opts.StripSourceMapSources); err != nil {
				return Report{}, fmt.Errorf("externalize %s: %w", finding.Path, err)
			}
			if strings.EqualFold(filepath.Ext(finding.Path), ".map") {
				asset := strings.TrimSuffix(target, ".map")
				if assetData, readErr := os.ReadFile(asset); readErr == nil {
					if rewritten, changed := RemoveSourceMappingURL(assetData); changed {
						if err := os.WriteFile(asset, rewritten, 0o644); err != nil {
							return Report{}, err
						}
					}
				}
			}
			if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return Report{}, fmt.Errorf("remove %s: %w", finding.Path, err)
			}
		case ActionRewrite:
			data, readErr := os.ReadFile(target)
			if readErr != nil {
				return Report{}, readErr
			}
			transformed, _, transformErr := p.transform(finding.Path, data)
			if transformErr != nil {
				return Report{}, transformErr
			}
			if err := os.WriteFile(target, transformed, 0o644); err != nil {
				return Report{}, err
			}
		}
	}
	if opts.RemoveEmptyDirs {
		if err := removeEmptyDirectories(extracted); err != nil {
			return Report{}, fmt.Errorf("remove empty directories: %w", err)
		}
	}

	original, err := asar.Open(source.ArchivePath)
	if err != nil {
		return Report{}, err
	}
	entries := original.Entries()
	_ = original.Close()
	unpacked, executables, err := preservedMetadata(entries, report.Findings, opts.SmartUnpack)
	if err != nil {
		return Report{}, err
	}

	var target archivefs.Pair
	var stage *archivefs.Stage
	inPlace := opts.Output == ""
	if opts.Output == "" {
		target = source
		stage, err = archivefs.NewStage(target)
		if err != nil {
			return Report{}, err
		}
		defer stage.Cleanup()
		target = stage.Pair()
	} else {
		out, absErr := filepath.Abs(opts.Output)
		if absErr != nil {
			return Report{}, absErr
		}
		if !strings.EqualFold(filepath.Ext(out), ".asar") {
			return Report{}, fmt.Errorf("output must have an .asar extension")
		}
		if _, statErr := os.Lstat(out); !errors.Is(statErr, fs.ErrNotExist) {
			return Report{}, fmt.Errorf("output %q already exists", out)
		}
		target = archivefs.Pair{ArchivePath: out, UnpackedPath: out + ".unpacked"}
		stage, err = archivefs.NewOutputStage(target)
		if err != nil {
			return Report{}, err
		}
		defer stage.Cleanup()
		target = stage.Pair()
	}
	_, err = asar.Create(extracted, target.ArchivePath, asar.CreateOptions{HintFile: opts.HintFile, Unpack: unpacked})
	if err != nil {
		return Report{}, fmt.Errorf("create: %w", err)
	}
	if err := restoreExecutables(target.ArchivePath, executables); err != nil {
		return Report{}, err
	}
	built, openErr := asar.Open(target.ArchivePath)
	if openErr != nil {
		return Report{}, openErr
	}
	if opts.Verify {
		verifyErr := built.VerifyAll()
		if verifyErr != nil {
			_ = built.Close()
			return Report{}, fmt.Errorf("verify output: %w", verifyErr)
		}
	}
	outputHash := built.HeaderHash()
	_ = built.Close()
	if opts.WindowsCompress {
		method, compressErr := compressWindowsArchive(target.ArchivePath)
		if compressErr != nil {
			return Report{}, fmt.Errorf("compress archive: %w", compressErr)
		}
		report.WindowsCompression = method
	}
	if err := localeStage.stage(); err != nil {
		return Report{}, fmt.Errorf("stage Chromium locale pruning: %w", err)
	}
	if !inPlace {
		actual, fingerprintErr := archivefs.PairFingerprint(source)
		if fingerprintErr != nil || actual != sourceFingerprint {
			localeStage.rollback()
			if fingerprintErr != nil {
				return Report{}, fmt.Errorf("source changed before commit: %w", fingerprintErr)
			}
			return Report{}, fmt.Errorf("source changed before commit (expected %s, found %s)", sourceFingerprint.String(), actual.String())
		}
	}
	if err := external.commit(); err != nil {
		localeStage.rollback()
		return Report{}, err
	}
	if err := hintStage.commit(); err != nil {
		external.rollback()
		localeStage.rollback()
		return Report{}, fmt.Errorf("commit generated hint: %w", err)
	}
	if stage != nil {
		var commitErr error
		if inPlace {
			commitErr = stage.CommitIfUnchanged(sourceFingerprint)
		} else {
			commitErr = stage.Commit()
		}
		if commitErr != nil {
			hintStage.rollback()
			external.rollback()
			localeStage.rollback()
			return Report{}, fmt.Errorf("commit: %w", commitErr)
		}
		if inPlace {
			target = source
		} else {
			out, _ := filepath.Abs(opts.Output)
			target = archivefs.Pair{ArchivePath: out, UnpackedPath: out + ".unpacked"}
		}
	}
	if err := localeStage.commit(); err != nil {
		report.Warnings = append(report.Warnings, "optimized archive and locale pruning committed, but cleanup of staged Chromium locales failed: "+err.Error())
	}
	if err := hintStage.finalize(); err != nil {
		report.Warnings = append(report.Warnings, "generated hint committed, but cleanup of its rollback file failed: "+err.Error())
	}
	report.Optimized = true
	report.Output = target.ArchivePath
	report.OutputHeaderHash = hex.EncodeToString(outputHash[:])
	newBytes, err := pairBytes(target)
	if err != nil {
		return Report{}, err
	}
	report.ActualSavings = report.ArchiveBytes + report.UnpackedBytes - newBytes
	return report, nil
}

func preservedMetadata(entries []asar.Entry, findings []Finding, smartUnpack bool) ([]string, []asar.Entry, error) {
	removed := make(map[string]bool)
	for _, f := range findings {
		if f.Action == ActionRemove {
			removed[f.Path] = true
		}
	}
	var unpacked []string
	var executables []asar.Entry
	for _, e := range entries {
		if removed[e.Path] || e.IsDir() || e.Link != "" {
			continue
		}
		shouldUnpack := e.Unpacked || (smartUnpack && shouldSmartUnpack(e.Path))
		if shouldUnpack {
			if strings.ContainsAny(e.Path, "*?[]{}") {
				return nil, nil, fmt.Errorf("cannot exactly preserve unpacked path containing glob metacharacters: %s", e.Path)
			}
			unpacked = append(unpacked, e.Path)
		}
		if e.Executable {
			executables = append(executables, e)
		}
	}
	return unpacked, executables, nil
}

func shouldSmartUnpack(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".node", ".dll", ".so", ".dylib", ".exe":
		return true
	default:
		return false
	}
}

func removeEmptyDirectories(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && name != root {
			dirs = append(dirs, name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	slices.SortFunc(dirs, func(a, b string) int { return len(b) - len(a) })
	for _, dir := range dirs {
		if err := os.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) || !isDirectoryNotEmpty(pathErr.Err) {
				return err
			}
		}
	}
	return nil
}

func isDirectoryNotEmpty(err error) bool {
	return errors.Is(err, fs.ErrExist) || strings.Contains(strings.ToLower(err.Error()), "not empty") || strings.Contains(strings.ToLower(err.Error()), "directory is not empty")
}

func restoreExecutables(archivePath string, entries []asar.Entry) error {
	for _, entry := range entries {
		a, err := asar.Open(archivePath)
		if err != nil {
			return err
		}
		data, readErr := a.ReadFile(entry.Path)
		_ = a.Close()
		if readErr != nil {
			return readErr
		}
		if err := asar.AddReader(archivePath, entry.Path, strings.NewReader(string(data)), asar.AddOptions{Replace: true, Executable: true, Unpacked: entry.Unpacked}); err != nil {
			return fmt.Errorf("restore executable metadata for %s: %w", entry.Path, err)
		}
	}
	return nil
}

func backupPair(name string) (archivefs.Pair, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return archivefs.Pair{}, err
	}
	return archivefs.Pair{ArchivePath: abs, UnpackedPath: abs + ".unpacked"}, nil
}

func pairBytes(pair archivefs.Pair) (int64, error) {
	st, err := os.Stat(pair.ArchivePath)
	if err != nil {
		return 0, err
	}
	side, err := directoryBytes(pair.UnpackedPath)
	return st.Size() + side, err
}

func directoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, fs.ErrNotExist) && name == root {
			return fs.SkipAll
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return total, err
}
