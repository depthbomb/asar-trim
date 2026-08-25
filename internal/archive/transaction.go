// Package archive provides the filesystem transaction boundary used when an
// ASAR archive and its optional .unpacked sidecar are replaced.
package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Pair names an ASAR archive and its optional unpacked sidecar directory.
// UnpackedPath need not be ArchivePath+".unpacked", which also makes Pair
// useful for explicitly named backup destinations.
type Pair struct {
	ArchivePath  string
	UnpackedPath string
}

// Resolve accepts either an .asar file or an Electron resources directory
// containing app.asar. It returns absolute, cleaned paths and rejects symbolic
// links and unexpected file types at the pair boundary.
func Resolve(input string) (Pair, error) {
	if strings.TrimSpace(input) == "" {
		return Pair{}, fmt.Errorf("archive: input path is empty")
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return Pair{}, err
	}
	abs = filepath.Clean(abs)
	recoveryArchive := abs
	if info, statErr := os.Lstat(abs); statErr == nil && info.IsDir() {
		recoveryArchive = filepath.Join(abs, "app.asar")
	}
	if err := recoverJournal(recoveryArchive); err != nil {
		return Pair{}, fmt.Errorf("archive: recover interrupted transaction: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return Pair{}, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return Pair{}, fmt.Errorf("archive: refusing symbolic-link input %q", abs)
	}
	archivePath := abs
	if info.IsDir() {
		archivePath = filepath.Join(abs, "app.asar")
		info, err = os.Lstat(archivePath)
		if err != nil {
			return Pair{}, fmt.Errorf("archive: resources directory has no app.asar: %w", err)
		}
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Pair{}, fmt.Errorf("archive: %q is not a regular ASAR file", archivePath)
	}
	if !strings.EqualFold(filepath.Ext(archivePath), ".asar") {
		return Pair{}, fmt.Errorf("archive: %q does not have an .asar extension", archivePath)
	}
	pair := Pair{ArchivePath: archivePath, UnpackedPath: archivePath + ".unpacked"}
	if err := validateOptionalSidecar(pair.UnpackedPath); err != nil {
		return Pair{}, err
	}
	return pair, nil
}

// Backup copies source to paths formed by appending suffix to both pair
// members. Existing destinations are never touched unless overwrite is true.
// On failure, newly created backup members are removed.
func Backup(source Pair, suffix string, overwrite bool) (Pair, error) {
	if suffix == "" {
		return Pair{}, fmt.Errorf("archive: backup suffix is empty")
	}
	dest := Pair{ArchivePath: source.ArchivePath + suffix, UnpackedPath: source.UnpackedPath + suffix}
	return BackupTo(source, dest, overwrite)
}

// BackupTo copies source to an explicitly named destination pair. When
// dest.UnpackedPath is empty, dest.ArchivePath+".unpacked" is used. Existing
// destinations are never touched unless overwrite is true.
func BackupTo(source, dest Pair, overwrite bool) (Pair, error) {
	if err := validateSourcePair(source); err != nil {
		return Pair{}, err
	}
	if dest.ArchivePath == "" {
		return Pair{}, fmt.Errorf("archive: backup archive path is empty")
	}
	archivePath, err := filepath.Abs(dest.ArchivePath)
	if err != nil {
		return Pair{}, err
	}
	dest.ArchivePath = filepath.Clean(archivePath)
	if dest.UnpackedPath == "" {
		dest.UnpackedPath = dest.ArchivePath + ".unpacked"
	} else {
		unpackedPath, err := filepath.Abs(dest.UnpackedPath)
		if err != nil {
			return Pair{}, err
		}
		dest.UnpackedPath = filepath.Clean(unpackedPath)
	}
	if pairPathsAlias(source, dest) || samePath(dest.ArchivePath, dest.UnpackedPath) {
		return Pair{}, fmt.Errorf("archive: backup destination aliases source")
	}
	if err := os.MkdirAll(filepath.Dir(dest.ArchivePath), 0o755); err != nil {
		return Pair{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dest.UnpackedPath), 0o755); err != nil {
		return Pair{}, err
	}

	if !overwrite {
		if err := copyPairExclusive(source, dest); err != nil {
			return Pair{}, err
		}
		return dest, nil
	}

	stage, err := newStageFor(dest)
	if err != nil {
		return Pair{}, err
	}
	defer stage.Cleanup()
	if err := copyPair(source, stage.pair); err != nil {
		return Pair{}, err
	}
	if err := stage.Commit(); err != nil {
		return Pair{}, err
	}
	return dest, nil
}

// Stage is a sibling staging pair that can be committed over a target pair.
// Call Cleanup with defer immediately after construction. Cleanup is harmless
// after a successful commit.
type Stage struct {
	target        Pair
	pair          Pair
	committed     bool
	requireAbsent bool
}

// Fingerprint identifies the complete archive pair at a point in time. It is
// content-based so a same-size or timestamp-preserving concurrent write is
// still detected.
type Fingerprint [sha256.Size]byte

func (f Fingerprint) String() string { return hex.EncodeToString(f[:]) }

// PairFingerprint hashes the archive and every sidecar member, including path,
// mode, and symlink target. Callers can use it to guard a destructive commit.
func PairFingerprint(pair Pair) (Fingerprint, error) {
	if err := validateSourcePair(pair); err != nil {
		return Fingerprint{}, err
	}
	h := sha256.New()
	if err := hashPath(h, pair.ArchivePath, "archive"); err != nil {
		return Fingerprint{}, err
	}
	if _, err := os.Lstat(pair.UnpackedPath); err == nil {
		err = filepath.WalkDir(pair.UnpackedPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, relErr := filepath.Rel(pair.UnpackedPath, path)
			if relErr != nil {
				return relErr
			}
			return hashPath(h, path, filepath.ToSlash(filepath.Join("unpacked", rel)))
		})
		if err != nil {
			return Fingerprint{}, err
		}
	} else if !os.IsNotExist(err) {
		return Fingerprint{}, err
	} else {
		_, _ = io.WriteString(h, "unpacked:absent\x00")
	}
	var result Fingerprint
	copy(result[:], h.Sum(nil))
	return result, nil
}

func hashPath(h io.Writer, path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d\x00", label, info.Mode().String(), info.Mode(), info.Size())
	if info.Mode()&fs.ModeSymlink != 0 {
		link, err := os.Readlink(path)
		if err != nil {
			return err
		}
		_, _ = io.WriteString(h, link)
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	return errors.Join(copyErr, closeErr)
}

// NewStage reserves a unique archive path beside target. Writers should write
// the new archive to Pair().ArchivePath and, when needed, its unpacked content
// to Pair().UnpackedPath.
func NewStage(target Pair) (*Stage, error) {
	if err := validateSourcePair(target); err != nil {
		return nil, err
	}
	return newStageFor(target)
}

// NewOutputStage stages a new pair beside a destination that must not exist.
// This gives --output the same archive-plus-sidecar transaction boundary as an
// in-place rewrite.
func NewOutputStage(target Pair) (*Stage, error) {
	if target.ArchivePath == "" {
		return nil, fmt.Errorf("archive: output path is empty")
	}
	abs, err := filepath.Abs(target.ArchivePath)
	if err != nil {
		return nil, err
	}
	target.ArchivePath = filepath.Clean(abs)
	if target.UnpackedPath == "" {
		target.UnpackedPath = target.ArchivePath + ".unpacked"
	}
	for _, path := range []string{target.ArchivePath, target.UnpackedPath} {
		if _, err := os.Lstat(path); err == nil {
			return nil, &fs.PathError{Op: "output", Path: path, Err: fs.ErrExist}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(target.ArchivePath), 0o755); err != nil {
		return nil, err
	}
	stage, err := newStageFor(target)
	if err != nil {
		return nil, err
	}
	stage.requireAbsent = true
	return stage, nil
}

func newStageFor(target Pair) (*Stage, error) {
	dir := filepath.Dir(target.ArchivePath)
	f, err := os.CreateTemp(dir, ".asar-trim-stage-*.asar")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return nil, err
	}
	if err := os.Remove(name); err != nil {
		return nil, err
	}
	return &Stage{
		target: target,
		pair: Pair{
			ArchivePath:  name,
			UnpackedPath: name + ".unpacked",
		},
	}, nil
}

// Pair returns the paths to which staged output should be written.
func (s *Stage) Pair() Pair { return s.pair }

// Commit replaces the target archive and sidecar as one rollback-capable
// operation. A missing staged sidecar removes a stale target sidecar.
func (s *Stage) Commit() error {
	return s.commit(nil)
}

// CommitIfUnchanged commits only if target still matches the fingerprint taken
// before analysis/extraction. The comparison occurs inside the commit boundary,
// immediately before target paths are moved.
func (s *Stage) CommitIfUnchanged(expected Fingerprint) error {
	return s.commit(&expected)
}

func (s *Stage) commit(expected *Fingerprint) error {
	if s == nil {
		return fmt.Errorf("archive: nil stage")
	}
	if s.committed {
		return fmt.Errorf("archive: stage already committed")
	}
	if err := validateStagedPair(s.pair); err != nil {
		return err
	}
	if s.requireAbsent {
		for _, path := range []string{s.target.ArchivePath, s.target.UnpackedPath} {
			if _, err := os.Lstat(path); err == nil {
				return &fs.PathError{Op: "output", Path: path, Err: fs.ErrExist}
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	if expected != nil {
		actual, err := PairFingerprint(s.target)
		if err != nil {
			return fmt.Errorf("archive: source changed before commit: %w", err)
		}
		if actual != *expected {
			return fmt.Errorf("archive: source changed before commit (expected %s, found %s)", expected.String(), actual.String())
		}
	}
	if err := commitPair(s.pair, s.target); err != nil {
		return err
	}
	s.committed = true
	return nil
}

// Cleanup removes uncommitted staged output.
func (s *Stage) Cleanup() error {
	if s == nil || s.committed {
		return nil
	}
	archiveErr := os.Remove(s.pair.ArchivePath)
	if os.IsNotExist(archiveErr) {
		archiveErr = nil
	}
	return errors.Join(archiveErr, os.RemoveAll(s.pair.UnpackedPath))
}

func validateSourcePair(pair Pair) error {
	if pair.ArchivePath == "" || pair.UnpackedPath == "" {
		return fmt.Errorf("archive: incomplete archive pair")
	}
	st, err := os.Lstat(pair.ArchivePath)
	if err != nil {
		return err
	}
	if st.Mode()&fs.ModeSymlink != 0 || !st.Mode().IsRegular() {
		return fmt.Errorf("archive: archive is not a regular file")
	}
	return validateOptionalSidecar(pair.UnpackedPath)
}

func validateStagedPair(pair Pair) error {
	if err := validateSourcePair(pair); err != nil {
		return fmt.Errorf("archive: invalid staged pair: %w", err)
	}
	return nil
}

func validateOptionalSidecar(path string) error {
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&fs.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("archive: sidecar %q is not a real directory", path)
	}
	return nil
}

func copyPair(source, dest Pair) error {
	if err := copyRegularFile(source.ArchivePath, dest.ArchivePath, false); err != nil {
		return err
	}
	if _, err := os.Lstat(source.UnpackedPath); err == nil {
		if err := copyTree(source.UnpackedPath, dest.UnpackedPath, false); err != nil {
			_ = os.Remove(dest.ArchivePath)
			_ = os.RemoveAll(dest.UnpackedPath)
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func copyPairExclusive(source, dest Pair) error {
	if _, err := os.Lstat(dest.ArchivePath); err == nil {
		return &fs.PathError{Op: "backup", Path: dest.ArchivePath, Err: fs.ErrExist}
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Lstat(dest.UnpackedPath); err == nil {
		return &fs.PathError{Op: "backup", Path: dest.UnpackedPath, Err: fs.ErrExist}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := copyRegularFile(source.ArchivePath, dest.ArchivePath, true); err != nil {
		return err
	}
	if _, err := os.Lstat(source.UnpackedPath); err == nil {
		if err := os.Mkdir(dest.UnpackedPath, 0o755); err != nil {
			_ = os.Remove(dest.ArchivePath)
			return err
		}
		if err := copyTreeContents(source.UnpackedPath, dest.UnpackedPath); err != nil {
			_ = os.Remove(dest.ArchivePath)
			_ = os.RemoveAll(dest.UnpackedPath)
			return err
		}
	} else if !os.IsNotExist(err) {
		_ = os.Remove(dest.ArchivePath)
		return err
	}
	return nil
}

func copyRegularFile(source, dest string, exclusive bool) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if exclusive {
		flags |= os.O_EXCL
	}
	out, err := os.OpenFile(dest, flags, st.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return nil
}

func copyTree(source, dest string, exclusive bool) error {
	if exclusive {
		if err := os.Mkdir(dest, 0o755); err != nil {
			return err
		}
	} else if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	return copyTreeContents(source, dest)
}

func copyTreeContents(source, dest string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			return os.Mkdir(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyRegularFile(path, target, true)
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return fmt.Errorf("archive: unsupported sidecar member %q", path)
		}
	})
}

func commitPair(stage, target Pair) error {
	if err := recoverJournal(target.ArchivePath); err != nil {
		return err
	}
	return commitPairJournaled(stage, target)
}

type transactionJournal struct {
	Version        int    `json:"version"`
	State          string `json:"state"`
	Stage          Pair   `json:"stage"`
	Target         Pair   `json:"target"`
	Backup         Pair   `json:"backup"`
	HadArchive     bool   `json:"had_archive"`
	HadUnpacked    bool   `json:"had_unpacked"`
	HasNewUnpacked bool   `json:"has_new_unpacked"`
}

func journalPath(archivePath string) string { return archivePath + ".asar-trim-journal" }

func commitPairJournaled(stage, target Pair) error {
	archiveBackup, err := reserveSibling(target.ArchivePath, ".asar-trim-rollback-*.asar")
	if err != nil {
		return err
	}
	sideBackup, err := reserveSibling(target.ArchivePath, ".asar-trim-rollback-*.unpacked")
	if err != nil {
		return err
	}
	_, archiveErr := os.Lstat(target.ArchivePath)
	_, sideErr := os.Lstat(target.UnpackedPath)
	_, stagedSideErr := os.Lstat(stage.UnpackedPath)
	if archiveErr != nil && !os.IsNotExist(archiveErr) {
		return archiveErr
	}
	if sideErr != nil && !os.IsNotExist(sideErr) {
		return sideErr
	}
	if stagedSideErr != nil && !os.IsNotExist(stagedSideErr) {
		return stagedSideErr
	}
	j := transactionJournal{Version: 1, State: "prepared", Stage: stage, Target: target, Backup: Pair{ArchivePath: archiveBackup, UnpackedPath: sideBackup}, HadArchive: archiveErr == nil, HadUnpacked: sideErr == nil, HasNewUnpacked: stagedSideErr == nil}
	if err := writeJournal(journalPath(target.ArchivePath), j); err != nil {
		return err
	}
	rollback := func(commitErr error) error {
		recoverErr := recoverPrepared(j)
		removeErr := os.Remove(journalPath(target.ArchivePath))
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		return errors.Join(commitErr, recoverErr, removeErr)
	}
	if _, err := os.Lstat(target.ArchivePath); err == nil {
		if err := os.Rename(target.ArchivePath, archiveBackup); err != nil {
			return rollback(err)
		}
	} else if !os.IsNotExist(err) {
		return rollback(err)
	}
	if _, err := os.Lstat(target.UnpackedPath); err == nil {
		if err := os.Rename(target.UnpackedPath, sideBackup); err != nil {
			return rollback(err)
		}
	} else if !os.IsNotExist(err) {
		return rollback(err)
	}
	if err := os.Rename(stage.ArchivePath, target.ArchivePath); err != nil {
		return rollback(err)
	}
	if _, err := os.Lstat(stage.UnpackedPath); err == nil {
		if err := os.Rename(stage.UnpackedPath, target.UnpackedPath); err != nil {
			return rollback(err)
		}
	} else if !os.IsNotExist(err) {
		return rollback(err)
	}
	j.State = "installed"
	if err := writeJournal(journalPath(target.ArchivePath), j); err != nil {
		return rollback(err)
	}
	// Once installed is durable, cleanup failures are recoverable housekeeping,
	// not a failed commit. Resolve will finish cleanup from the journal.
	_ = recoverInstalled(j)
	_ = os.Remove(journalPath(target.ArchivePath))
	return nil
}

func reserveSibling(path, pattern string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}

func writeJournal(path string, journal transactionJournal) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".asar-trim-journal-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	encErr := json.NewEncoder(temp).Encode(journal)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(encErr, syncErr, closeErr); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		// Windows cannot atomically replace an existing journal with Rename.
		// At this installed-state transition, a crash in the fallback can only
		// orphan rollback files; both new target members are already present.
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			_ = os.Remove(name)
			return errors.Join(err, removeErr)
		}
		if retryErr := os.Rename(name, path); retryErr != nil {
			_ = os.Remove(name)
			return errors.Join(err, retryErr)
		}
	}
	return nil
}

func recoverJournal(archivePath string) error {
	path := journalPath(archivePath)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal transactionJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return err
	}
	if journal.Version != 1 || !validJournalPaths(journal, archivePath) {
		return fmt.Errorf("invalid transaction journal %q", path)
	}
	if journal.State == "installed" {
		err = recoverInstalled(journal)
	} else {
		err = recoverPrepared(journal)
	}
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func validJournalPaths(j transactionJournal, archivePath string) bool {
	if !samePath(j.Target.ArchivePath, archivePath) || !samePath(j.Target.UnpackedPath, archivePath+".unpacked") {
		return false
	}
	dir := filepath.Dir(archivePath)
	for _, path := range []string{j.Stage.ArchivePath, j.Stage.UnpackedPath, j.Backup.ArchivePath, j.Backup.UnpackedPath} {
		abs, err := filepath.Abs(path)
		if err != nil || !samePath(filepath.Dir(abs), dir) {
			return false
		}
	}
	return strings.HasPrefix(filepath.Base(j.Stage.ArchivePath), ".asar-trim-stage-") &&
		strings.HasPrefix(filepath.Base(j.Backup.ArchivePath), ".asar-trim-rollback-") &&
		strings.HasPrefix(filepath.Base(j.Backup.UnpackedPath), ".asar-trim-rollback-")
}

func recoverPrepared(j transactionJournal) error {
	var errs []error
	if j.HadUnpacked {
		if _, err := os.Lstat(j.Backup.UnpackedPath); err == nil {
			_ = os.RemoveAll(j.Target.UnpackedPath)
			errs = append(errs, os.Rename(j.Backup.UnpackedPath, j.Target.UnpackedPath))
		} else if !os.IsNotExist(err) {
			errs = append(errs, err)
		} else if _, targetErr := os.Lstat(j.Target.UnpackedPath); os.IsNotExist(targetErr) {
			errs = append(errs, fmt.Errorf("archive: cannot recover missing sidecar; rollback copy is also missing"))
		}
	} else {
		errs = append(errs, os.RemoveAll(j.Target.UnpackedPath))
	}
	if j.HadArchive {
		if _, err := os.Lstat(j.Backup.ArchivePath); err == nil {
			_ = os.Remove(j.Target.ArchivePath)
			errs = append(errs, os.Rename(j.Backup.ArchivePath, j.Target.ArchivePath))
		} else if !os.IsNotExist(err) {
			errs = append(errs, err)
		} else if _, targetErr := os.Lstat(j.Target.ArchivePath); os.IsNotExist(targetErr) {
			errs = append(errs, fmt.Errorf("archive: cannot recover missing archive; rollback copy is also missing"))
		}
	} else {
		errs = append(errs, os.Remove(j.Target.ArchivePath))
	}
	errs = append(errs, os.Remove(j.Stage.ArchivePath), os.RemoveAll(j.Stage.UnpackedPath))
	return joinIgnoringNotExist(errs...)
}

func recoverInstalled(j transactionJournal) error {
	if st, err := os.Lstat(j.Target.ArchivePath); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("archive: installed transaction target is missing or invalid; rollback files retained")
	}
	if j.HasNewUnpacked {
		if st, err := os.Lstat(j.Target.UnpackedPath); err != nil || !st.IsDir() {
			return fmt.Errorf("archive: installed transaction sidecar is missing or invalid; rollback files retained")
		}
	}
	return joinIgnoringNotExist(os.Remove(j.Backup.ArchivePath), os.RemoveAll(j.Backup.UnpackedPath), os.Remove(j.Stage.ArchivePath), os.RemoveAll(j.Stage.UnpackedPath))
}

func joinIgnoringNotExist(errs ...error) error {
	kept := errs[:0]
	for _, err := range errs {
		if err != nil && !os.IsNotExist(err) {
			kept = append(kept, err)
		}
	}
	return errors.Join(kept...)
}

func commitPairWithRename(stage, target Pair, rename func(string, string) error) error {
	archiveBackup, err := moveAside(target.ArchivePath, rename)
	if err != nil {
		return err
	}
	sideBackup, err := moveAside(target.UnpackedPath, rename)
	if err != nil {
		if archiveBackup != "" {
			_ = rename(archiveBackup, target.ArchivePath)
		}
		return err
	}

	archiveInstalled := false
	sideInstalled := false
	rollback := func(commitErr error) error {
		var errs []error
		if sideInstalled {
			errs = append(errs, os.RemoveAll(target.UnpackedPath))
		}
		if sideBackup != "" {
			errs = append(errs, rename(sideBackup, target.UnpackedPath))
		}
		if archiveInstalled {
			errs = append(errs, os.Remove(target.ArchivePath))
		}
		if archiveBackup != "" {
			errs = append(errs, rename(archiveBackup, target.ArchivePath))
		}
		return errors.Join(append([]error{commitErr}, errs...)...)
	}
	if err := rename(stage.ArchivePath, target.ArchivePath); err != nil {
		return rollback(err)
	}
	archiveInstalled = true
	if _, err := os.Lstat(stage.UnpackedPath); err == nil {
		if err := rename(stage.UnpackedPath, target.UnpackedPath); err != nil {
			return rollback(err)
		}
		sideInstalled = true
	} else if !os.IsNotExist(err) {
		return rollback(err)
	}
	var cleanup []error
	if archiveBackup != "" {
		cleanup = append(cleanup, os.Remove(archiveBackup))
	}
	if sideBackup != "" {
		cleanup = append(cleanup, os.RemoveAll(sideBackup))
	}
	return errors.Join(cleanup...)
}

func moveAside(path string, rename func(string, string) error) (string, error) {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".asar-trim-rollback-*")
	if err != nil {
		return "", err
	}
	backup := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(backup)
		return "", err
	}
	if err := os.Remove(backup); err != nil {
		return "", err
	}
	if err := rename(path, backup); err != nil {
		return "", err
	}
	return backup, nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}

func pairPathsAlias(source, dest Pair) bool {
	return samePath(source.ArchivePath, dest.ArchivePath) ||
		samePath(source.ArchivePath, dest.UnpackedPath) ||
		samePath(source.UnpackedPath, dest.ArchivePath) ||
		samePath(source.UnpackedPath, dest.UnpackedPath)
}
