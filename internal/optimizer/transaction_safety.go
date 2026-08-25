package optimizer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	archivefs "github.com/depthbomb/asar-trim/internal/archive"
)

// generatedHintStage keeps --generate-hint private until the optimized archive
// has been fully built and verified. If a later commit fails, rollback restores
// a force-overwritten hint or removes the newly installed one.
type generatedHintStage struct {
	target, staged, backup string
	force                  bool
	installed              bool
}

func prepareGeneratedHint(source archivefs.Pair, opts Options) (*generatedHintStage, string, error) {
	if opts.GenerateHint == "" {
		return nil, opts.HintFile, nil
	}
	target, err := filepath.Abs(opts.GenerateHint)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, "", err
	}
	if !opts.ForceHint {
		if _, err := os.Lstat(target); err == nil {
			return nil, "", &fs.PathError{Op: "generate hint", Path: target, Err: fs.ErrExist}
		} else if !os.IsNotExist(err) {
			return nil, "", err
		}
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".asar-trim-hint-*.hint")
	if err != nil {
		return nil, "", err
	}
	staged := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(staged)
		return nil, "", err
	}
	if err := os.Remove(staged); err != nil {
		return nil, "", err
	}
	stage := &generatedHintStage{target: target, staged: staged, force: opts.ForceHint}
	if len(opts.AccessLogs) > 0 {
		_, _, err = GenerateAccessLogHint(source.ArchivePath, opts.AccessLogs, staged, false)
	} else {
		_, err = generateHintPair(source, HintOptions{Output: staged, EntryPoints: opts.HintEntries, Force: false})
	}
	if err != nil {
		stage.cleanup()
		return nil, "", err
	}
	return stage, staged, nil
}

func (s *generatedHintStage) commit() error {
	if s == nil {
		return nil
	}
	if _, err := os.Lstat(s.target); err == nil {
		if !s.force {
			return &fs.PathError{Op: "generate hint", Path: s.target, Err: fs.ErrExist}
		}
		f, createErr := os.CreateTemp(filepath.Dir(s.target), ".asar-trim-hint-rollback-*")
		if createErr != nil {
			return createErr
		}
		s.backup = f.Name()
		if closeErr := f.Close(); closeErr != nil {
			_ = os.Remove(s.backup)
			return closeErr
		}
		if removeErr := os.Remove(s.backup); removeErr != nil {
			return removeErr
		}
		if renameErr := os.Rename(s.target, s.backup); renameErr != nil {
			return renameErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(s.staged, s.target); err != nil {
		if s.backup != "" {
			_ = os.Rename(s.backup, s.target)
			s.backup = ""
		}
		return err
	}
	s.staged = ""
	s.installed = true
	return nil
}

func (s *generatedHintStage) rollback() {
	if s == nil {
		return
	}
	if s.installed {
		_ = os.Remove(s.target)
		s.installed = false
	}
	if s.backup != "" {
		_ = os.Rename(s.backup, s.target)
		s.backup = ""
	}
}

func (s *generatedHintStage) finalize() error {
	if s == nil || s.backup == "" {
		return nil
	}
	err := os.Remove(s.backup)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	s.backup = ""
	return err
}

func (s *generatedHintStage) cleanup() {
	if s == nil {
		return
	}
	if s.staged != "" {
		_ = os.Remove(s.staged)
	}
}
