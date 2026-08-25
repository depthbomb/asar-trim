package archive

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveFileAndResourcesDirectory(t *testing.T) {
	root := t.TempDir()
	resources := filepath.Join(root, "resources")
	mustMkdir(t, resources)
	archivePath := filepath.Join(resources, "app.asar")
	mustWrite(t, archivePath, "archive")
	mustMkdir(t, archivePath+".unpacked")

	fromFile, err := Resolve(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	fromDir, err := Resolve(resources)
	if err != nil {
		t.Fatal(err)
	}
	if fromFile != fromDir || fromFile.ArchivePath != archivePath {
		t.Fatalf("file=%+v directory=%+v", fromFile, fromDir)
	}
}

func TestResolveRejectsUnexpectedInputs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "not-asar.zip"), "x")
	if _, err := Resolve(filepath.Join(root, "not-asar.zip")); err == nil {
		t.Fatal("accepted non-ASAR file")
	}
	if _, err := Resolve(root); err == nil {
		t.Fatal("accepted resources directory without app.asar")
	}
	archivePath := filepath.Join(root, "app.asar")
	mustWrite(t, archivePath, "x")
	mustWrite(t, archivePath+".unpacked", "not a directory")
	if _, err := Resolve(archivePath); err == nil {
		t.Fatal("accepted non-directory sidecar")
	}
}

func TestBackupDoesNotOverwriteByDefault(t *testing.T) {
	pair := makePair(t, "original", "native")
	backup, err := Backup(pair, ".bak", false)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, backup.ArchivePath, "original")
	assertFile(t, filepath.Join(backup.UnpackedPath, "addon.node"), "native")
	mustWrite(t, backup.ArchivePath, "keep")
	if _, err := Backup(pair, ".bak", false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second backup error = %v", err)
	}
	assertFile(t, backup.ArchivePath, "keep")
}

func TestBackupOverwriteReplacesPair(t *testing.T) {
	pair := makePair(t, "new archive", "new native")
	backup := Pair{ArchivePath: pair.ArchivePath + ".bak", UnpackedPath: pair.UnpackedPath + ".bak"}
	mustWrite(t, backup.ArchivePath, "old archive")
	mustMkdir(t, backup.UnpackedPath)
	mustWrite(t, filepath.Join(backup.UnpackedPath, "stale"), "stale")
	if _, err := Backup(pair, ".bak", true); err != nil {
		t.Fatal(err)
	}
	assertFile(t, backup.ArchivePath, "new archive")
	assertFile(t, filepath.Join(backup.UnpackedPath, "addon.node"), "new native")
	if _, err := os.Stat(filepath.Join(backup.UnpackedPath, "stale")); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar member survived: %v", err)
	}
}

func TestBackupToCustomPathAndDefaultSidecar(t *testing.T) {
	pair := makePair(t, "archive", "native")
	destArchive := filepath.Join(t.TempDir(), "nested", "postman-backup.asar")
	backup, err := BackupTo(pair, Pair{ArchivePath: destArchive}, false)
	if err != nil {
		t.Fatal(err)
	}
	if backup.ArchivePath != destArchive || backup.UnpackedPath != destArchive+".unpacked" {
		t.Fatalf("backup paths = %+v", backup)
	}
	assertFile(t, backup.ArchivePath, "archive")
	assertFile(t, filepath.Join(backup.UnpackedPath, "addon.node"), "native")
	if _, err := BackupTo(pair, Pair{ArchivePath: destArchive}, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second custom backup error = %v", err)
	}
}

func TestBackupToRejectsSourceAliases(t *testing.T) {
	pair := makePair(t, "archive", "native")
	tests := []Pair{
		{ArchivePath: pair.ArchivePath},
		{ArchivePath: pair.UnpackedPath},
		{ArchivePath: filepath.Join(t.TempDir(), "backup.asar"), UnpackedPath: pair.ArchivePath},
	}
	for _, dest := range tests {
		if _, err := BackupTo(pair, dest, true); err == nil {
			t.Fatalf("accepted alias destination %+v", dest)
		}
	}
}

func TestStageCommitReplacesPairAndRemovesStaleSidecar(t *testing.T) {
	target := makePair(t, "old", "old native")
	stage, err := NewStage(target)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Cleanup()
	mustWrite(t, stage.Pair().ArchivePath, "new")
	if err := stage.Commit(); err != nil {
		t.Fatal(err)
	}
	assertFile(t, target.ArchivePath, "new")
	if _, err := os.Stat(target.UnpackedPath); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar survived: %v", err)
	}
}

func TestStageCleanupIsIdempotent(t *testing.T) {
	target := makePair(t, "old", "old native")
	stage, err := NewStage(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := stage.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestOutputStageCommitsArchiveAndSidecarTogether(t *testing.T) {
	root := t.TempDir()
	target := Pair{ArchivePath: filepath.Join(root, "optimized.asar"), UnpackedPath: filepath.Join(root, "optimized.asar.unpacked")}
	stage, err := NewOutputStage(target)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Cleanup()
	mustWrite(t, stage.Pair().ArchivePath, "new archive")
	mustMkdir(t, stage.Pair().UnpackedPath)
	mustWrite(t, filepath.Join(stage.Pair().UnpackedPath, "addon.node"), "new native")
	if err := stage.Commit(); err != nil {
		t.Fatal(err)
	}
	assertFile(t, target.ArchivePath, "new archive")
	assertFile(t, filepath.Join(target.UnpackedPath, "addon.node"), "new native")
	if _, err := os.Stat(journalPath(target.ArchivePath)); !os.IsNotExist(err) {
		t.Fatalf("journal survived commit: %v", err)
	}
}

func TestOutputStageRefusesDestinationCreatedBeforeCommit(t *testing.T) {
	root := t.TempDir()
	target := Pair{ArchivePath: filepath.Join(root, "optimized.asar"), UnpackedPath: filepath.Join(root, "optimized.asar.unpacked")}
	stage, err := NewOutputStage(target)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Cleanup()
	mustWrite(t, stage.Pair().ArchivePath, "ours")
	mustWrite(t, target.ArchivePath, "concurrent")
	if err := stage.Commit(); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("commit error = %v", err)
	}
	assertFile(t, target.ArchivePath, "concurrent")
}

func TestCommitIfUnchangedRefusesConcurrentSourceChange(t *testing.T) {
	target := makePair(t, "old", "old native")
	fingerprint, err := PairFingerprint(target)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := NewStage(target)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Cleanup()
	mustWrite(t, stage.Pair().ArchivePath, "new")
	mustWrite(t, target.ArchivePath, "changed concurrently")
	if err := stage.CommitIfUnchanged(fingerprint); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("commit error = %v", err)
	}
	assertFile(t, target.ArchivePath, "changed concurrently")
}

func TestCommitIfUnchangedDetectsSidecarChange(t *testing.T) {
	target := makePair(t, "old", "old native")
	fingerprint, err := PairFingerprint(target)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := NewStage(target)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Cleanup()
	mustWrite(t, stage.Pair().ArchivePath, "new")
	mustWrite(t, filepath.Join(target.UnpackedPath, "addon.node"), "changed native")
	if err := stage.CommitIfUnchanged(fingerprint); err == nil {
		t.Fatal("sidecar mutation was not detected")
	}
	assertFile(t, target.ArchivePath, "old")
	assertFile(t, filepath.Join(target.UnpackedPath, "addon.node"), "changed native")
}

func TestRecoverPreparedJournalRestoresOldPair(t *testing.T) {
	target := makePair(t, "new partial", "new partial native")
	backupArchive := filepath.Join(filepath.Dir(target.ArchivePath), ".asar-trim-rollback-old.asar")
	backupSide := filepath.Join(filepath.Dir(target.ArchivePath), ".asar-trim-rollback-old.unpacked")
	mustWrite(t, backupArchive, "old")
	mustMkdir(t, backupSide)
	mustWrite(t, filepath.Join(backupSide, "addon.node"), "old native")
	stageArchive := filepath.Join(filepath.Dir(target.ArchivePath), ".asar-trim-stage-crash.asar")
	mustWrite(t, stageArchive, "orphan")
	j := transactionJournal{Version: 1, State: "prepared", Stage: Pair{ArchivePath: stageArchive, UnpackedPath: stageArchive + ".unpacked"}, Target: target, Backup: Pair{ArchivePath: backupArchive, UnpackedPath: backupSide}, HadArchive: true, HadUnpacked: true}
	if err := writeJournal(journalPath(target.ArchivePath), j); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(target.ArchivePath); err != nil {
		t.Fatal(err)
	}
	assertFile(t, target.ArchivePath, "old")
	assertFile(t, filepath.Join(target.UnpackedPath, "addon.node"), "old native")
	if _, err := os.Stat(stageArchive); !os.IsNotExist(err) {
		t.Fatalf("orphan stage survived: %v", err)
	}
}

func TestRecoverInstalledJournalKeepsNewPair(t *testing.T) {
	target := makePair(t, "new", "new native")
	backupArchive := filepath.Join(filepath.Dir(target.ArchivePath), ".asar-trim-rollback-old.asar")
	backupSide := filepath.Join(filepath.Dir(target.ArchivePath), ".asar-trim-rollback-old.unpacked")
	mustWrite(t, backupArchive, "old")
	mustMkdir(t, backupSide)
	stageArchive := filepath.Join(filepath.Dir(target.ArchivePath), ".asar-trim-stage-crash.asar")
	j := transactionJournal{Version: 1, State: "installed", Stage: Pair{ArchivePath: stageArchive, UnpackedPath: stageArchive + ".unpacked"}, Target: target, Backup: Pair{ArchivePath: backupArchive, UnpackedPath: backupSide}, HadArchive: true, HadUnpacked: true}
	if err := writeJournal(journalPath(target.ArchivePath), j); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(target.ArchivePath); err != nil {
		t.Fatal(err)
	}
	assertFile(t, target.ArchivePath, "new")
	if _, err := os.Stat(backupArchive); !os.IsNotExist(err) {
		t.Fatalf("rollback archive survived: %v", err)
	}
}

func TestCommitRollbackRestoresBothTargets(t *testing.T) {
	target := makePair(t, "old archive", "old native")
	stage := Pair{
		ArchivePath:  filepath.Join(filepath.Dir(target.ArchivePath), "stage.asar"),
		UnpackedPath: filepath.Join(filepath.Dir(target.ArchivePath), "stage.asar.unpacked"),
	}
	mustWrite(t, stage.ArchivePath, "new archive")
	mustMkdir(t, stage.UnpackedPath)
	mustWrite(t, filepath.Join(stage.UnpackedPath, "addon.node"), "new native")
	want := errors.New("injected sidecar failure")
	rename := func(old, new string) error {
		if old == stage.UnpackedPath && new == target.UnpackedPath {
			return want
		}
		return os.Rename(old, new)
	}
	if err := commitPairWithRename(stage, target, rename); !errors.Is(err, want) {
		t.Fatalf("commit error = %v", err)
	}
	assertFile(t, target.ArchivePath, "old archive")
	assertFile(t, filepath.Join(target.UnpackedPath, "addon.node"), "old native")
}

func makePair(t *testing.T, archive, side string) Pair {
	t.Helper()
	root := t.TempDir()
	pair := Pair{ArchivePath: filepath.Join(root, "app.asar"), UnpackedPath: filepath.Join(root, "app.asar.unpacked")}
	mustWrite(t, pair.ArchivePath, archive)
	mustMkdir(t, pair.UnpackedPath)
	mustWrite(t, filepath.Join(pair.UnpackedPath, "addon.node"), side)
	return pair
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
	}
}
