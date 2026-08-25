# asar-trim

`asar-trim` analyzes and optimizes the ASAR archives shipped with Electron
applications.

The tool accepts either a direct path to an `.asar` file or the Electron
`resources` directory containing `app.asar`.

## Install

Downloadable release binaries are the supported distribution method. Download
the archive for your operating system and architecture from the
[GitHub Releases](https://github.com/depthbomb/asar-trim/releases) page, extract
it, and place `asar-trim` (or `asar-trim.exe`) somewhere on `PATH`.

Each release includes a SHA-256 checksum file. Verify the downloaded asset
before running it:

```console
# Linux and macOS
sha256sum --check checksums.txt --ignore-missing

# Windows PowerShell: compare this value with the asset's checksums.txt entry
$asset = ".\asar-trim_2.0.0_windows_amd64.zip"
Get-FileHash $asset -Algorithm SHA256
```

Release archives also have SPDX SBOMs and GitHub artifact attestations issued
by the release workflow. With the GitHub CLI installed, verify publisher
identity and build provenance before extraction:

```console
gh attestation verify ./asar-trim_2.0.0_linux_amd64.tar.gz \
  --repo depthbomb/asar-trim
```

Release binaries are not represented as manually signed local builds. The
GitHub attestation is the supported publisher-identity verification mechanism;
`checksums.txt` independently detects accidental or malicious byte changes.

Confirm the installed build with `asar-trim --version`.

Source installation is development-only and is not a supported distribution
channel.

## Supported systems

| System | Architectures | Support |
| --- | --- | --- |
| Windows | x86-64, ARM64 | Fully supported; LZX/NTFS compression is Windows-only |
| Linux | x86-64, ARM64 | Fully supported |
| macOS | x86-64, Apple silicon | Fully supported |
| Other Go targets | Source build | Best effort; not covered by release testing |

ASAR analysis and rewriting are platform-independent. Target pruning always
uses explicit `--target-*` values, so the host platform does not determine
what is removed.

## Commands

Analyze an archive without changing it:

```console
asar-trim analyze ./resources
asar-trim analyze ./resources/app.asar --format json
```

Optimize in place, optionally retaining a backup:

```console
asar-trim optimize ./resources --backup
```

Write a separate archive for application distribution:

```console
asar-trim optimize ./resources/app.asar --output ./dist/app.asar
```

Generate a startup ordering hint from statically discoverable module loads:

```console
asar-trim hint ./resources/app.asar --output ./startup.hint
```

For a higher-confidence ordering, start the packaged application with
`ELECTRON_LOG_ASAR_READS=1`, exercise its startup workflows, and merge the
resulting Electron logs:

```console
asar-trim hint ./resources/app.asar \
  --access-log ./main-process.log \
  --access-log ./renderer.log \
  --output ./startup.hint
```

Generate and apply a hint in one optimization run:

```console
asar-trim optimize ./resources/app.asar --generate-hint ./startup.hint
```

Run `asar-trim help analyze`, `asar-trim help hint`, or
`asar-trim help optimize` for the complete flag reference.

## Optimization policy

Both commands use the same planner, so the analysis describes the operations
the optimizer will perform. Analysis also detects identical payloads stored at
different offsets; rebuilding deduplicates them automatically. Three profiles
control how readily files are removed:

- `safe` (default) removes high-confidence dependency development resources,
  including tests, coverage, documentation, examples, source maps, TypeScript
  declarations, repository metadata, build caches, lockfiles, and common tool
  configuration. Legal notices remain protected.
- `balanced` includes common development-only files while retaining known
  runtime-sensitive data.
- `aggressive` removes a broad set of development artifacts and can remove
  files an unusual application loads at runtime.

Repeatable `--keep GLOB` and `--remove GLOB` flags customize the selected
profile. Keep rules take precedence. `--minify-json=false` disables JSON
compaction, and `--prune-package=false` keeps all `package.json` properties.

An Electron/Atom load-order hint can be supplied with `--hint-file FILE`.
Ordering can improve startup access patterns, but does not itself reduce the
archive's size. The `hint` command can generate one by following static
`require` and `import` references from the root package entry point. Use
repeatable `--entry PATH` options for additional startup entry points. Static
analysis cannot see computed or runtime-selected module loads, so generated
hints should be validated against the packaged application.

Runtime logs are merged in first-access order and filtered against the selected
archive. They can also drive an explicit unread-file audit. `--remove-unread`
requires at least one `--access-log` and is dangerous unless the traces cover
every application workflow and optional feature that must remain functional.

`optimize --generate-hint FILE` generates the hint and uses it in the same
rewrite. Add startup paths with repeatable `--hint-entry PATH`. Both commands
refuse to replace an existing hint unless their force option is supplied.

Optimization verifies ASAR integrity metadata by default. Use
`--verify=false` only when processing an archive whose existing integrity
metadata is known to be invalid. `--dry-run` makes `optimize` run the analyzer
without writing anything.

## Target, dependency, and locale pruning

Target-aware analysis never infers the target from the machine running the
tool. Supply it explicitly:

```console
asar-trim analyze app.asar \
  --target-platform win32 --target-arch x64

asar-trim optimize app.asar \
  --target-platform win32 --target-arch x64 \
  --prune-incompatible
```

This checks package `os`, `cpu`, and `libc` constraints and recognized native
prebuild layouts. Unknown binaries remain untouched. `--prune-extraneous`
removes package trees outside the root runtime dependency graph, while
`--omit-optional` removes optional-only dependency trees. Both are explicit
because packages can use dynamic resolution and optional features.

Repeatable `--locale NAME` options remove dependency locale files outside the
allowlist. With a resources-directory input, `--trim-electron-locales` also
removes unselected Chromium `.pak` files transactionally and refuses to remove
every available Chromium locale.

Analysis reports identical installed package trees separately from byte-level
payload duplicates. Package-tree deduplication is reported as a build-time
recommendation rather than rewriting Node's module layout in place.

## Source maps, assets, and build metadata

`--strip-sourcemap-sources` removes embedded `sourcesContent` while preserving
source-map mappings. `--externalize-source-maps DIRECTORY` removes maps from
the archive, strips matching `sourceMappingURL` directives, and writes the maps
to a separate directory using a staged commit.

The opt-in `--minify-js`, `--minify-css`, `--minify-html`, and `--minify-svg`
transformations are deliberately conservative and dependency-free. They are
not substitutes for parser-aware build-time bundling and must be tested against
the packaged application. JSON compaction remains enabled by default.

Repeatable `--bundler-metadata FILE` accepts esbuild metafiles and Webpack stats
to identify source trees that may duplicate emitted bundles. Use
`--write-recommendations FILE` to save structured packaging recommendations.

`--smart-unpack` places native modules and executable payloads in the unpacked
sidecar, improving compatibility with APIs that require physical paths; it is a
runtime optimization, not a size reduction. `--remove-empty-dirs` omits empty
directory entries left after trimming.

## Licenses and notices

Legal files remain protected by default. For distribution builds,
`--externalize-licenses FILE` consolidates recognized license and notice files
outside the archive instead of discarding them.

`--dangerously-remove-licenses` is intended only for privately modified local
installations. It removes recognized license files and common leading license
header comments. The command prints a compliance warning and requires the exact
interactive acknowledgement `REMOVE LICENSES`; missing or non-matching input
fails closed. It cannot be combined with license externalization.

## Windows filesystem compression

On Windows, `--windows-compress` applies transparent LZX compression to the
completed `.asar` through `WofSetFileDataLocation`. If WOF is unavailable, it
falls back to native NTFS compression through `FSCTL_SET_COMPRESSION`. No shell
command is launched, archive contents and hashes do not change, and the report
records whether `wof-lzx` or `ntfs` was used.

## Backups and temporary files

In-place optimization replaces the original archive. Pass `--backup` to retain
a backup, or `--backup-path FILE` to choose its location. Existing backups are
not overwritten unless `--force-backup` is supplied. If the archive has an
`app.asar.unpacked` sidecar, it is treated as part of the archive operation.

Temporary extraction uses a unique working directory. `--work-dir DIRECTORY`
chooses its parent/location and `--keep-work-dir` retains it for inspection.

## Safety notes

Always analyze first and test the resulting Electron application. Static file
rules cannot prove that an application will not load an unusual source,
documentation, map, or metadata file at runtime; this is especially important
with the `balanced` and `aggressive` profiles.

Rewriting an ASAR changes its header hash. Applications that enable Electron's
ASAR integrity checks or platform code signing may require their integrity
metadata and signatures to be regenerated after optimization. `asar-trim`
reports the new archive state but does not patch or re-sign an Electron
executable.

Do not run experiments against an installed application's only copy. Copy
both `app.asar` and `app.asar.unpacked` (when present), optimize the copy, then
validate application startup and important workflows.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for the contribution workflow and
[SECURITY.md](SECURITY.md) for private vulnerability reporting.

```console
go test ./...
go vet ./...
go build ./cmd/asar-trim
```
