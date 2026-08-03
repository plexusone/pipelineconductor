package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/plexusone/pipelineconductor/internal/collector"
	"github.com/plexusone/pipelineconductor/internal/compliance"
	"github.com/plexusone/pipelineconductor/internal/remediator"
	"github.com/plexusone/pipelineconductor/pkg/model"
)

var remediateCmd = &cobra.Command{
	Use:   "remediate",
	Short: "Generate compliant workflow files for non-compliant repositories",
	Long: `Generate compliant workflow files for repositories that are missing required workflows.

Scans local repositories and generates workflow files that call the reference
reusable workflows from the organization's .github repository.

Example:
  # Dry-run to see what would be generated
  pipelineconductor remediate --local ~/go/src/github.com --orgs plexusone --languages Go --dry-run

  # Generate workflows for all non-compliant repos
  pipelineconductor remediate --local ~/go/src/github.com --orgs plexusone --languages Go

  # Generate workflows for a specific repo
  pipelineconductor remediate --local ~/go/src/github.com --orgs plexusone --repo vibium-wcag --languages Go

  # Use a different reference repo
  pipelineconductor remediate --local ~/go/src/github.com --orgs myorg --ref-repo myorg/.github`,
	RunE: runRemediate,
}

func init() {
	rootCmd.AddCommand(remediateCmd)

	remediateCmd.Flags().String("local", "", "Base path for local filesystem scanning (required)")
	remediateCmd.Flags().StringSliceP("languages", "l", nil, "Filter by languages (Go, TypeScript, Crystal)")
	remediateCmd.Flags().StringP("ref-repo", "r", "plexusone/.github", "Reference workflow repository (owner/repo)")
	remediateCmd.Flags().String("ref-branch", "main", "Branch in reference repo")
	remediateCmd.Flags().String("repo", "", "Target specific repository name")
	remediateCmd.Flags().Bool("dry-run", false, "Show what would be generated without writing files")
	remediateCmd.Flags().Bool("overwrite", false, "Overwrite existing workflow files")
	remediateCmd.Flags().StringP("output", "o", "", "Output remediation report to file")
	remediateCmd.Flags().StringP("format", "f", "text", "Output format: text, json")

	_ = remediateCmd.MarkFlagRequired("local")

	_ = viper.BindPFlag("languages", remediateCmd.Flags().Lookup("languages"))
}

// RemediationReport contains the results of a remediation run.
type RemediationReport struct {
	DryRun          bool                    `json:"dryRun"`
	RefRepo         string                  `json:"refRepo"`
	RefBranch       string                  `json:"refBranch"`
	TotalRepos      int                     `json:"totalRepos"`
	RemediatedRepos int                     `json:"remediatedRepos"`
	FilesGenerated  int                     `json:"filesGenerated"`
	Repos           []RepoRemediationResult `json:"repos"`
}

// RepoRemediationResult contains remediation results for a single repo.
type RepoRemediationResult struct {
	Owner     string                     `json:"owner"`
	Name      string                     `json:"name"`
	FullName  string                     `json:"fullName"`
	LocalPath string                     `json:"localPath"`
	Files     []remediator.GeneratedFile `json:"files"`
	Error     string                     `json:"error,omitempty"`
}

func runRemediate(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	localPath, _ := cmd.Flags().GetString("local")
	if localPath == "" {
		return fmt.Errorf("--local flag is required")
	}

	// Expand home directory
	if localPath == "~" || len(localPath) > 1 && localPath[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("expanding home directory: %w", err)
		}
		localPath = filepath.Join(home, localPath[1:])
	}

	orgs := viper.GetStringSlice("orgs")
	users := viper.GetStringSlice("users")
	if len(orgs) == 0 && len(users) == 0 {
		return fmt.Errorf("at least one organization (--orgs) or user (--users) is required")
	}

	languages, _ := cmd.Flags().GetStringSlice("languages")
	if len(languages) == 0 {
		languages = viper.GetStringSlice("languages")
	}
	if len(languages) == 0 {
		return fmt.Errorf("at least one language is required (--languages)")
	}

	// Validate languages
	for _, lang := range languages {
		if !compliance.IsLanguageSupported(lang) {
			return fmt.Errorf("unsupported language: %s (supported: %v)", lang, compliance.SupportedLanguages())
		}
	}

	refRepo, _ := cmd.Flags().GetString("ref-repo")
	refBranch, _ := cmd.Flags().GetString("ref-branch")
	targetRepo, _ := cmd.Flags().GetString("repo")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	overwrite, _ := cmd.Flags().GetBool("overwrite")
	outputFile, _ := cmd.Flags().GetString("output")
	format, _ := cmd.Flags().GetString("format")
	verbose := viper.GetBool("verbose")

	// Build filter
	filter := model.RepoFilter{
		IncludeLanguages: languages,
	}

	// Create local collector
	coll := collector.NewLocalCollectorWithConfig(collector.LocalCollectorConfig{
		BasePath: localPath,
		Verbose:  verbose,
	})

	if verbose {
		fmt.Fprintf(os.Stderr, "Local scanning mode: %s\n", localPath)
		fmt.Fprintf(os.Stderr, "Reference repo: %s@%s\n", refRepo, refBranch)
		if dryRun {
			fmt.Fprintf(os.Stderr, "Dry-run mode: no files will be written\n")
		}
	}

	// List repositories
	repos, err := coll.ListReposMultiSource(ctx, orgs, users, filter)
	if err != nil {
		return fmt.Errorf("listing repositories: %w", err)
	}

	// Filter to specific repo if requested
	if targetRepo != "" {
		var filtered []model.Repo
		for _, r := range repos {
			if r.Name == targetRepo {
				filtered = append(filtered, r)
			}
		}
		repos = filtered
		if len(repos) == 0 {
			return fmt.Errorf("repository %s not found", targetRepo)
		}
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "Found %d repositories\n", len(repos))
	}

	// Create compliance checker to find non-compliant repos
	checker, err := compliance.NewChecker(coll, compliance.CheckerConfig{
		RefRepo:   refRepo,
		RefBranch: refBranch,
		Strict:    true,
		Verbose:   verbose,
	})
	if err != nil {
		return fmt.Errorf("creating checker: %w", err)
	}

	// Run compliance check
	result, err := checker.CheckRepos(ctx, repos, languages)
	if err != nil {
		return fmt.Errorf("checking repos: %w", err)
	}

	// Create generator
	gen := remediator.NewGenerator(remediator.GeneratorConfig{
		RefRepo:   refRepo,
		RefBranch: refBranch,
		DryRun:    dryRun,
		Verbose:   verbose,
	})

	// Generate remediation report
	report := RemediationReport{
		DryRun:     dryRun,
		RefRepo:    refRepo,
		RefBranch:  refBranch,
		TotalRepos: len(repos),
	}

	// Process non-compliant repos
	for _, repoResult := range result.Repos {
		if len(repoResult.Missing) == 0 && repoResult.ComplianceLevel == model.ComplianceLevelFull {
			continue // Skip fully compliant repos
		}

		// Find the repo with local path
		var repo model.Repo
		for _, r := range repos {
			if r.FullName == repoResult.FullName {
				repo = r
				break
			}
		}

		if repo.LocalPath == "" {
			continue
		}

		// Skip if no missing workflows (partial compliance due to non-reusable)
		if len(repoResult.Missing) == 0 {
			continue
		}

		repoRemResult := RepoRemediationResult{
			Owner:     repo.Owner,
			Name:      repo.Name,
			FullName:  repo.FullName,
			LocalPath: repo.LocalPath,
		}

		// Check for existing files if not overwriting
		if !overwrite {
			var filteredMissing []model.MissingWorkflow
			for _, m := range repoResult.Missing {
				tmpl, ok := gen.GetTemplate(m.WorkflowType)
				if !ok {
					continue
				}
				existingPath := filepath.Join(repo.LocalPath, ".github", "workflows", tmpl.Filename)
				if _, err := os.Stat(existingPath); os.IsNotExist(err) {
					filteredMissing = append(filteredMissing, m)
				} else if verbose {
					fmt.Fprintf(os.Stderr, "Skipping %s (file exists, use --overwrite to replace)\n", existingPath)
				}
			}
			repoResult.Missing = filteredMissing
		}

		if len(repoResult.Missing) == 0 {
			continue
		}

		// Generate workflow files
		generated, err := gen.GenerateForRepo(repo, repoResult.Missing)
		if err != nil {
			repoRemResult.Error = err.Error()
		} else {
			repoRemResult.Files = generated
			report.FilesGenerated += len(generated)
		}

		report.Repos = append(report.Repos, repoRemResult)
		if len(repoRemResult.Files) > 0 || repoRemResult.Error != "" {
			report.RemediatedRepos++
		}
	}

	// Output report
	var output []byte
	switch format {
	case "json":
		output, err = json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling report: %w", err)
		}
	case "text":
		output = []byte(formatTextReport(report, dryRun))
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}

	if outputFile != "" {
		if err := os.WriteFile(outputFile, output, 0600); err != nil {
			return fmt.Errorf("writing output file: %w", err)
		}
		if verbose {
			fmt.Fprintf(os.Stderr, "Report written to: %s\n", outputFile)
		}
	} else {
		fmt.Println(string(output))
	}

	return nil
}

func formatTextReport(report RemediationReport, dryRun bool) string {
	var sb strings.Builder

	action := "Generated"
	if dryRun {
		action = "Would generate"
	}

	sb.WriteString(fmt.Sprintf("Remediation Report\n"))
	sb.WriteString(fmt.Sprintf("==================\n\n"))
	sb.WriteString(fmt.Sprintf("Reference: %s@%s\n", report.RefRepo, report.RefBranch))
	sb.WriteString(fmt.Sprintf("Dry Run: %v\n", report.DryRun))
	sb.WriteString(fmt.Sprintf("Total Repos: %d\n", report.TotalRepos))
	sb.WriteString(fmt.Sprintf("Repos Remediated: %d\n", report.RemediatedRepos))
	sb.WriteString(fmt.Sprintf("Files %s: %d\n\n", action, report.FilesGenerated))

	for _, repo := range report.Repos {
		sb.WriteString(fmt.Sprintf("## %s\n", repo.FullName))
		sb.WriteString(fmt.Sprintf("   Path: %s\n", repo.LocalPath))

		if repo.Error != "" {
			sb.WriteString(fmt.Sprintf("   Error: %s\n", repo.Error))
		} else {
			for _, f := range repo.Files {
				status := "created"
				if f.WouldOverwrite {
					status = "overwritten"
				}
				if dryRun {
					status = "would be " + status
				}
				sb.WriteString(fmt.Sprintf("   - %s (%s)\n", f.RelativePath, status))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
