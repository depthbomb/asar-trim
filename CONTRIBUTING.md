# Contributing

Issues and pull requests are welcome. For vulnerabilities, follow
[SECURITY.md](SECURITY.md) instead of opening a public issue.

## Development setup

Install the Go version declared in `go.mod`, clone the repository, then run:

```console
go mod download
go test ./...
go vet ./...
go build ./cmd/asar-trim
```

Keep changes focused and add tests for observable behavior. Archive mutations
should be tested with generated fixtures or disposable copies—never with an
installed application's only archive. Tests must not depend on private local
paths, network access, or a particular host operating system unless guarded by
an appropriate Go build constraint.

Before submitting a pull request:

1. Run `gofmt` on changed Go files.
2. Run `go test ./...` and `go vet ./...`.
3. Run `go test -race ./...` on a platform supported by Go's race detector when
   changing concurrent or filesystem code.
4. Update documentation and `CHANGELOG.md` for user-facing behavior.
5. Confirm `git diff --check` reports no whitespace errors.

Avoid silent destructive behavior. New removal rules should be analysis-first,
conservative by default, and backed by a keep override or an explicit opt-in
when runtime use cannot be disproved. Preserve legal notices unless the user
selects an existing explicit legal-file workflow.

By contributing, you agree that your contribution is licensed under the
repository's MIT license.

## Release expectations

Maintainers release from `vMAJOR.MINOR.PATCH` tags after CI succeeds. The
release workflow, rather than a developer workstation, must produce all public
binaries. A complete release contains the supported platform archives,
`checksums.txt`, per-archive SPDX SBOMs, and GitHub artifact attestations.

Before the first public 2.x release, enable GitHub's immutable-releases setting
for the repository. Protect the default branch and release tags so only
reviewed commits that passed CI can be tagged. The tag-triggered workflow
repeats tests on Windows, Linux, and macOS before it receives permission to
publish assets.

Do not replace assets under an existing version tag. Publish a new patch
version when rebuilding is necessary, and document user-visible changes in
`CHANGELOG.md` before tagging.
