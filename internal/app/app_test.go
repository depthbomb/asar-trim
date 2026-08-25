package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/depthbomb/asar-trim/internal/optimizer"
)

type fakeEngine struct {
	analyzeCalls  int
	optimizeCalls int
	hintCalls     int
	input         string
	opts          optimizer.Options
	err           error
}

func (f *fakeEngine) Analyze(input string, opts optimizer.Options) (optimizer.Report, error) {
	f.analyzeCalls++
	f.input, f.opts = input, opts
	return optimizer.Report{}, f.err
}

func (f *fakeEngine) Optimize(input string, opts optimizer.Options) (optimizer.Report, error) {
	f.optimizeCalls++
	f.input, f.opts = input, opts
	return optimizer.Report{}, f.err
}

func (f *fakeEngine) GenerateHint(input string, opts optimizer.HintOptions) (optimizer.HintReport, error) {
	f.hintCalls++
	f.input = input
	return optimizer.HintReport{Output: opts.Output}, f.err
}

func TestHintCommand(t *testing.T) {
	engine := &fakeEngine{}
	cmd := NewWithEngine("test", engine)
	cmd.Writer = &bytes.Buffer{}
	if err := cmd.Run(context.Background(), []string{"asar-trim", "hint", "app.asar", "--output", "startup.hint", "--entry", "bootstrap.js"}); err != nil {
		t.Fatal(err)
	}
	if engine.hintCalls != 1 || engine.input != "app.asar" {
		t.Fatalf("hint calls = %d, input = %q", engine.hintCalls, engine.input)
	}
}

func TestAnalyzeMapsPolicyFlags(t *testing.T) {
	engine := &fakeEngine{}
	stdout := &bytes.Buffer{}
	cmd := NewWithEngine("test", engine)
	cmd.Writer = stdout

	err := cmd.Run(context.Background(), []string{
		"asar-trim", "analyze", "resources",
		"--profile", "balanced",
		"--keep", "**/runtime.ts",
		"--keep", "{LICENSE,NOTICE}",
		"--remove", "**/*.map",
		"--minify-json=false",
		"--prune-package=false",
		"--hint-file", "startup.hint",
		"--format", "json",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if engine.analyzeCalls != 1 || engine.optimizeCalls != 0 {
		t.Fatalf("calls = analyze %d, optimize %d", engine.analyzeCalls, engine.optimizeCalls)
	}
	if engine.input != "resources" {
		t.Errorf("input = %q, want resources", engine.input)
	}
	if engine.opts.Profile != optimizer.ProfileBalanced {
		t.Errorf("profile = %q, want balanced", engine.opts.Profile)
	}
	if engine.opts.MinifyJSON || engine.opts.PrunePackage {
		t.Errorf("boolean policy options were not disabled: %+v", engine.opts)
	}
	if engine.opts.HintFile != "startup.hint" {
		t.Errorf("hint file = %q", engine.opts.HintFile)
	}
	if len(engine.opts.KeepPatterns) != 2 || len(engine.opts.RemovePatterns) != 1 {
		t.Errorf("pattern options = keep %v, remove %v", engine.opts.KeepPatterns, engine.opts.RemovePatterns)
	}
	if len(engine.opts.KeepPatterns) == 2 && engine.opts.KeepPatterns[1] != "{LICENSE,NOTICE}" {
		t.Errorf("comma-containing glob was split: %v", engine.opts.KeepPatterns)
	}
	var report any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Errorf("JSON output is invalid: %v\n%s", err, stdout)
	}
}

func TestOptimizeMapsRewriteFlags(t *testing.T) {
	engine := &fakeEngine{}
	cmd := NewWithEngine("test", engine)
	cmd.Writer = &bytes.Buffer{}

	err := cmd.Run(context.Background(), []string{
		"asar-trim", "optimize", "app.asar",
		"--output", "optimized.asar",
		"--backup-path", "original.asar.bak",
		"--force-backup",
		"--verify=false",
		"--work-dir", "work",
		"--keep-work-dir",
		"--generate-hint", "startup.hint",
		"--hint-entry", "bootstrap.js",
		"--force-hint",
		"--windows-compress",
		"--target-platform", "win32",
		"--target-arch", "x64",
		"--locale", "en-US",
		"--prune-incompatible",
		"--prune-extraneous",
		"--omit-optional",
		"--strip-sourcemap-sources",
		"--minify-js",
		"--smart-unpack",
		"--remove-empty-dirs",
		"--format", "json",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if engine.optimizeCalls != 1 || engine.analyzeCalls != 0 {
		t.Fatalf("calls = analyze %d, optimize %d", engine.analyzeCalls, engine.optimizeCalls)
	}
	if engine.opts.Output != "optimized.asar" || engine.opts.BackupPath != "original.asar.bak" {
		t.Errorf("output/backup paths not mapped: %+v", engine.opts)
	}
	if !engine.opts.Backup || !engine.opts.ForceBackup {
		t.Errorf("backup options not mapped: %+v", engine.opts)
	}
	if engine.opts.Verify {
		t.Error("verify = true, want false")
	}
	if engine.opts.WorkDir != "work" || !engine.opts.KeepWorkDir {
		t.Errorf("work directory options not mapped: %+v", engine.opts)
	}
	if engine.opts.GenerateHint != "startup.hint" || len(engine.opts.HintEntries) != 1 || !engine.opts.ForceHint {
		t.Errorf("hint generation options not mapped: %+v", engine.opts)
	}
	if !engine.opts.WindowsCompress || engine.opts.TargetPlatform != "win32" || engine.opts.TargetArch != "x64" || !engine.opts.PruneIncompatible || !engine.opts.PruneExtraneous || !engine.opts.OmitOptional {
		t.Errorf("target/compression options not mapped: %+v", engine.opts)
	}
	if len(engine.opts.Locales) != 1 || !engine.opts.StripSourceMapSources || !engine.opts.MinifyJS || !engine.opts.SmartUnpack || !engine.opts.RemoveEmptyDirs {
		t.Errorf("content options not mapped: %+v", engine.opts)
	}
}

func TestOptimizeDryRunUsesAnalyzer(t *testing.T) {
	engine := &fakeEngine{}
	cmd := NewWithEngine("test", engine)
	cmd.Writer = &bytes.Buffer{}

	err := cmd.Run(context.Background(), []string{"asar-trim", "optimize", "app.asar", "--dry-run", "--format", "json"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if engine.analyzeCalls != 1 || engine.optimizeCalls != 0 {
		t.Fatalf("calls = analyze %d, optimize %d", engine.analyzeCalls, engine.optimizeCalls)
	}
}

func TestDangerousLicenseRemovalRequiresExactAcknowledgement(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  bool
	}{
		{name: "accepted", input: dangerousLicenseAcknowledgement + "\n", want: true},
		{name: "rejected", input: "yes\n", want: false},
		{name: "missing", input: "", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := &fakeEngine{}
			cmd := NewWithEngine("test", engine)
			cmd.Writer = &bytes.Buffer{}
			cmd.ErrWriter = &bytes.Buffer{}
			cmd.Reader = strings.NewReader(test.input)
			err := cmd.Run(context.Background(), []string{"asar-trim", "optimize", "app.asar", "--dangerously-remove-licenses", "--format", "json"})
			if test.want {
				if err != nil || engine.optimizeCalls != 1 || !engine.opts.DangerouslyRemoveLicenses {
					t.Fatalf("accepted acknowledgement: calls=%d opts=%+v err=%v", engine.optimizeCalls, engine.opts, err)
				}
			} else if err == nil || engine.optimizeCalls != 0 {
				t.Fatalf("rejected acknowledgement: calls=%d err=%v", engine.optimizeCalls, err)
			}
		})
	}
}

func TestValidationStopsBeforeEngine(t *testing.T) {
	tests := [][]string{
		{"asar-trim", "analyze"},
		{"asar-trim", "analyze", "one", "two"},
		{"asar-trim", "analyze", "app.asar", "--profile", "reckless"},
		{"asar-trim", "analyze", "app.asar", "--format", "yaml"},
	}
	for _, args := range tests {
		engine := &fakeEngine{}
		cmd := NewWithEngine("test", engine)
		cmd.Writer = &bytes.Buffer{}
		if err := cmd.Run(context.Background(), args); err == nil {
			t.Errorf("Run(%v) error = nil", args)
		}
		if engine.analyzeCalls != 0 || engine.optimizeCalls != 0 {
			t.Errorf("Run(%v) invoked engine", args)
		}
	}
}

func TestEngineErrorIncludesOperationAndInput(t *testing.T) {
	engine := &fakeEngine{err: errors.New("broken archive")}
	cmd := NewWithEngine("test", engine)
	cmd.Writer = &bytes.Buffer{}
	err := cmd.Run(context.Background(), []string{"asar-trim", "analyze", "bad.asar"})
	if err == nil || err.Error() != `analyze "bad.asar": broken archive` {
		t.Fatalf("Run() error = %v", err)
	}
}
