# PipelineConductor

**Orchestrate and harmonize multi-repo CI/CD pipelines with policy-driven automation.**

PipelineConductor is a tool for managing CI/CD pipeline consistency across hundreds of repositories. It scans repositories, evaluates them against Cedar policies, generates compliance reports, and can automatically remediate violations via pull requests.

## Key Features

- **Multi-org Scanning** - Scan repositories across multiple GitHub organizations in a single command
- **Policy-as-Code** - Define CI/CD policies using [Cedar](https://www.cedarpolicy.com/), a fast and expressive policy language
- **Profile System** - Named configurations for different project types (default, modern, legacy)
- **Multiple Report Formats** - Generate JSON, SARIF, Markdown, and CSV reports
- **GitHub Security Integration** - SARIF output integrates with GitHub's Security tab
- **Automated Remediation** - Create pull requests to fix policy violations (coming soon)

## Why PipelineConductor?

Managing CI/CD consistency across many repositories is challenging:

| Challenge | PipelineConductor Solution |
|-----------|---------------------------|
| Inconsistent CI configs | Policy-based enforcement |
| Outdated Go versions | Automated version checking |
| Missing security checks | Branch protection policies |
| Manual auditing | Automated compliance reports |
| Scattered configurations | Centralized policy management |

## Quick Example

```bash
# Set your GitHub token
export GITHUB_TOKEN=ghp_your_token_here

# Scan your organization
pipelineconductor scan --orgs myorg --format markdown

# Output:
# # Compliance Report
#
# ## Summary
# | Metric | Value |
# |--------|-------|
# | Total Repos | 42 |
# | Compliant | 38 |
# | Non-Compliant | 4 |
# | Compliance Rate | 90.5% |
```

## Architecture

```
┌────────────────────────────────────────────────────────────────┐
│                      PipelineConductor CLI                     │
├────────────────────────────────────────────────────────────────┤
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐  │
│  │  Collectors  │  │    Policy    │  │       Reports        │  │
│  │ - GitHub API │  │    Engine    │  │ - JSON, Markdown     │  │
│  │ - (GitLab)   │  │ - Cedar      │  │ - SARIF, CSV         │  │
│  └──────────────┘  └──────────────┘  └──────────────────────┘  │
│                            │                                   │
│                    ┌───────┴────────┐                          │
│                    │   pkg/model    │                          │
│                    └────────────────┘                          │
└────────────────────────────────────────────────────────────────┘
```

## Getting Started

Ready to get started? Head to the [Installation](installation.md) guide or jump straight to the [Quick Start](quickstart.md).

## License

PipelineConductor is released under the MIT License.
