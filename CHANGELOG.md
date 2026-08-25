# Changelog

Notable user-facing changes are documented here. This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2.0.0] - 2026-08-25

### Added

- Native, cross-platform command-line binaries for analyzing, optimizing, and
  generating startup-order hints for ASAR archives.
- Safe, balanced, and aggressive optimization profiles with explicit keep and
  remove overrides.
- Transactional archive and unpacked-sidecar replacement, optional backups,
  integrity verification, and dry-run analysis.
- Runtime access-log hints, target and dependency audits, locale pruning,
  duplicate detection, source-map handling, conservative asset minification,
  and bundler metadata recommendations.
- Opt-in Windows LZX compression with NTFS compression fallback.
- License and notice externalization, plus an interactive, explicitly dangerous
  private-installation-only removal mode.
- JSON reports suitable for CI and packaging pipelines.
- Binary releases for Windows, Linux, and macOS on AMD64 and ARM64, with
  SHA-256 checksums, per-archive SPDX SBOMs, and build-provenance attestations.

### Changed

- Development-only resources are removed by the safe profile while legal
  notices remain protected unless the explicitly dangerous removal workflow is
  acknowledged.
- Downloadable binaries are the supported distribution channel.
- Archive output, sidecars, generated hints, external artifacts, and Chromium
  locale pruning participate in staged commit and rollback workflows.

### Security

- Archive paths, symlinks, staged output, backup destinations, and destructive
  legal-file removal are validated before mutation.
- Source archives and unpacked sidecars are fingerprinted before replacement,
  and interrupted archive-pair commits are recovered through constrained
  transaction journals.

[2.0.0]: https://github.com/depthbomb/asar-trim/releases/tag/v2.0.0
