# Security policy

## Supported versions

| Version | Security fixes |
| --- | --- |
| 2.x | Supported |
| Earlier versions | Unsupported |

Only the latest 2.x release is guaranteed to receive a security fix. Upgrade
before reporting a problem that may already be resolved.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use the repository's
[private security advisory form](https://github.com/depthbomb/asar-trim/security/advisories/new)
and include:

- The affected version and operating system
- A minimal archive or reproduction, with confidential application data
  removed
- The expected and observed behavior
- The security impact and any known workarounds

You should receive an acknowledgement within seven days. After triage, the
maintainers will share the assessment and, when applicable, a remediation and
coordinated-disclosure plan. Please allow a reasonable period for a fix and
release before publishing details.

## Scope

Path traversal, unsafe symlink handling, archive corruption, integrity-check
bypasses, and unintended filesystem mutation are in scope. Incorrectly trimmed
application files without a security consequence are ordinary bug reports.
Questions about third-party license obligations are not security reports.

Never attach a proprietary application archive unless you are authorized to
share it.

## Release authenticity

Official binaries are attached to this repository's GitHub Releases and are
built by its release workflow. Verify both the published SHA-256 checksum and
the GitHub artifact attestation as described in `README.md`. Treat a failed
checksum or attestation as a potential supply-chain incident and report it
privately through the advisory form above.
