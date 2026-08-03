# Changelog

This page provides a summary of releases. For detailed changes with commit links, see the [full CHANGELOG](https://github.com/plexusone/pipelineconductor/blob/main/CHANGELOG.md).

## Releases

### [v0.3.0](releases/v0.3.0.md) - 2026-08-02

**Highlights:**

- `pipelineconductor` CLI now published as part of the repository and installable via `go install`
- Migrated off `go-github` onto `gogithub` v0.17.0's version-isolated `clientv1.Client`
- Dashboard dependency renamed from `dashforge` to `uiforge` v0.4.0

### [v0.2.0](releases/v0.2.0.md) - 2026-04-25

**Highlights:**

- Workflow compliance checking against reference repositories
- Local filesystem collector for scanning without GitHub API
- GitHub Action for CI/CD integration
- Automated workflow remediation with template generation
- Dashforge dashboard generation for visual compliance reporting

**Breaking Changes:**

- Module path migrated from `github.com/grokify/pipelineconductor` to `github.com/plexusone/pipelineconductor`

### [v0.1.0](releases/v0.1.0.md) - 2026-01-31

**Highlights:**

- Multi-org GitHub repository scanning with Cedar policy-as-code evaluation
- Four output formats (JSON, Markdown, SARIF, CSV) with GitHub Security integration
- Built-in rate limiting with exponential backoff for large-scale scans

## Versioning

PipelineConductor follows [Semantic Versioning](https://semver.org/). Given a version `MAJOR.MINOR.PATCH`:

- **MAJOR**: Breaking changes
- **MINOR**: New features (backwards compatible)
- **PATCH**: Bug fixes (backwards compatible)

During the `0.x` phase, minor versions may include breaking changes as the API stabilizes.
