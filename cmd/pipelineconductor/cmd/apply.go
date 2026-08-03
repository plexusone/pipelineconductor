package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/plexusone/pipelineconductor/internal/collector"
	"github.com/plexusone/pipelineconductor/internal/compliance"
	"github.com/plexusone/pipelineconductor/internal/remediator"
	"github.com/plexusone/pipelineconductor/pkg/model"
)

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply compliant workflows to repositories and optionally commit/push",
	Long: `Apply compliant workflow files to repositories, commit changes, and optionally push.

This command combines remediation with git operations for batch workflow updates.
It generates workflow files, commits them, and can push to remote or create PRs.

Example:
  # Generate workflows and commit (no push)
  pipelineconductor apply --local ~/go/src/github.com --orgs plexusone --languages Go

  # Generate, commit, and push to remote
  pipelineconductor apply --local ~/go/src/github.com --orgs plexusone --languages Go --push

  # Generate, commit, push, and create PRs (requires gh CLI)
  pipelineconductor apply --local ~/go/src/github.com --orgs plexusone --languages Go --push --create-pr

  # Apply to specific repo only
  pipelineconductor apply --local ~/go/src/github.com --orgs plexusone --repo vibium-wcag --languages Go --push`,
	RunE: runApply,
}

func init() {
	rootCmd.AddCommand(applyCmd)

	applyCmd.Flags().String("local", "", "Base path for local filesystem scanning (required)")
	applyCmd.Flags().StringSliceP("languages", "l", nil, "Filter by languages (Go, TypeScript, Crystal)")
	applyCmd.Flags().StringP("ref-repo", "r", "plexusone/.github", "Reference workflow repository (owner/repo)")
	applyCmd.Flags().String("ref-branch", "main", "Branch in reference repo")
	applyCmd.Flags().String("repo", "", "Target specific repository name")
	applyCmd.Flags().Bool("dry-run", false, "Show what would be done without making changes")
	applyCmd.Flags().Bool("overwrite", false, "Overwrite existing workflow files")
	applyCmd.Flags().Bool("push", false, "Push commits to remote after applying")
	applyCmd.Flags().Bool("create-pr", false, "Create pull request after pushing (requires --push and gh CLI)")
	applyCmd.Flags().String("branch", "ci/update-workflows", "Branch name for commits (default: ci/update-workflows)")
	applyCmd.Flags().StringP("message", "m", "", "Commit message (default: auto-generated)")

	_ = applyCmd.MarkFlagRequired("local")
}

// ApplyResult contains the result of applying workflows to a repo.
type ApplyResult struct {
	Owner        string   `json:"owner"`
	Name         string   `json:"name"`
	FullName     string   `json:"fullName"`
	LocalPath    string   `json:"localPath"`
	FilesCreated []string `json:"filesCreated"`
	Committed    bool     `json:"committed"`
	Pushed       bool     `json:"pushed"`
	PRCreated    bool     `json:"prCreated"`
	PRURL        string   `json:"prUrl,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func runApply(cmd *cobra.Command, _ []string) error {
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
	push, _ := cmd.Flags().GetBool("push")
	createPR, _ := cmd.Flags().GetBool("create-pr")
	branchName, _ := cmd.Flags().GetString("branch")
	commitMsg, _ := cmd.Flags().GetString("message")
	verbose := viper.GetBool("verbose")

	if createPR && !push {
		return fmt.Errorf("--create-pr requires --push")
	}

	if commitMsg == "" {
		commitMsg = fmt.Sprintf("ci: add compliant workflows from %s", refRepo)
	}

	filter := model.RepoFilter{
		IncludeLanguages: languages,
	}

	coll := collector.NewLocalCollectorWithConfig(collector.LocalCollectorConfig{
		BasePath: localPath,
		Verbose:  verbose,
	})

	if verbose {
		fmt.Fprintf(os.Stderr, "Local scanning mode: %s\n", localPath)
		fmt.Fprintf(os.Stderr, "Reference repo: %s@%s\n", refRepo, refBranch)
		if dryRun {
			fmt.Fprintf(os.Stderr, "Dry-run mode: no changes will be made\n")
		}
	}

	repos, err := coll.ListReposMultiSource(ctx, orgs, users, filter)
	if err != nil {
		return fmt.Errorf("listing repositories: %w", err)
	}

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

	checker, err := compliance.NewChecker(coll, compliance.CheckerConfig{
		RefRepo:   refRepo,
		RefBranch: refBranch,
		Strict:    true,
		Verbose:   verbose,
	})
	if err != nil {
		return fmt.Errorf("creating checker: %w", err)
	}

	result, err := checker.CheckRepos(ctx, repos, languages)
	if err != nil {
		return fmt.Errorf("checking repos: %w", err)
	}

	gen := remediator.NewGenerator(remediator.GeneratorConfig{
		RefRepo:   refRepo,
		RefBranch: refBranch,
		DryRun:    dryRun,
		Verbose:   verbose,
	})

	var results []ApplyResult
	var totalFiles int

	for _, repoResult := range result.Repos {
		if len(repoResult.Missing) == 0 {
			continue
		}

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

		ar := ApplyResult{
			Owner:     repo.Owner,
			Name:      repo.Name,
			FullName:  repo.FullName,
			LocalPath: repo.LocalPath,
		}

		// Filter out existing files if not overwriting
		missingToApply := repoResult.Missing
		if !overwrite {
			var filtered []model.MissingWorkflow
			for _, m := range missingToApply {
				tmpl, ok := gen.GetTemplate(m.WorkflowType)
				if !ok {
					continue
				}
				existingPath := filepath.Join(repo.LocalPath, ".github", "workflows", tmpl.Filename)
				if _, err := os.Stat(existingPath); os.IsNotExist(err) {
					filtered = append(filtered, m)
				}
			}
			missingToApply = filtered
		}

		if len(missingToApply) == 0 {
			continue
		}

		fmt.Printf("\n=== %s ===\n", repo.FullName)

		// Generate workflow files
		generated, err := gen.GenerateForRepo(repo, missingToApply)
		if err != nil {
			ar.Error = err.Error()
			fmt.Printf("  Error generating files: %v\n", err)
			results = append(results, ar)
			continue
		}

		for _, f := range generated {
			ar.FilesCreated = append(ar.FilesCreated, f.RelativePath)
			fmt.Printf("  Created: %s\n", f.RelativePath)
		}
		totalFiles += len(generated)

		if dryRun {
			results = append(results, ar)
			continue
		}

		// Git operations
		if len(generated) > 0 {
			// Create branch if pushing
			if push {
				if err := gitCreateBranch(repo.LocalPath, branchName); err != nil {
					ar.Error = fmt.Sprintf("creating branch: %v", err)
					fmt.Printf("  Error: %v\n", err)
					results = append(results, ar)
					continue
				}
			}

			// Stage files
			var filesToAdd []string
			for _, f := range generated {
				filesToAdd = append(filesToAdd, f.RelativePath)
			}

			if err := gitAdd(repo.LocalPath, filesToAdd); err != nil {
				ar.Error = fmt.Sprintf("staging files: %v", err)
				fmt.Printf("  Error: %v\n", err)
				results = append(results, ar)
				continue
			}

			// Commit
			if err := gitCommit(repo.LocalPath, commitMsg); err != nil {
				ar.Error = fmt.Sprintf("committing: %v", err)
				fmt.Printf("  Error: %v\n", err)
				results = append(results, ar)
				continue
			}
			ar.Committed = true
			fmt.Printf("  Committed: %s\n", commitMsg)

			// Push
			if push {
				if err := gitPush(repo.LocalPath, branchName); err != nil {
					ar.Error = fmt.Sprintf("pushing: %v", err)
					fmt.Printf("  Error: %v\n", err)
					results = append(results, ar)
					continue
				}
				ar.Pushed = true
				fmt.Printf("  Pushed to: %s\n", branchName)

				// Create PR
				if createPR {
					prURL, err := ghCreatePR(repo.LocalPath, branchName, commitMsg)
					if err != nil {
						ar.Error = fmt.Sprintf("creating PR: %v", err)
						fmt.Printf("  Error creating PR: %v\n", err)
					} else {
						ar.PRCreated = true
						ar.PRURL = prURL
						fmt.Printf("  PR created: %s\n", prURL)
					}
				}
			}
		}

		results = append(results, ar)
	}

	// Summary
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Repositories processed: %d\n", len(results))
	fmt.Printf("Files created: %d\n", totalFiles)

	committed := 0
	pushed := 0
	prs := 0
	errors := 0
	for _, r := range results {
		if r.Committed {
			committed++
		}
		if r.Pushed {
			pushed++
		}
		if r.PRCreated {
			prs++
		}
		if r.Error != "" {
			errors++
		}
	}

	fmt.Printf("Committed: %d\n", committed)
	if push {
		fmt.Printf("Pushed: %d\n", pushed)
	}
	if createPR {
		fmt.Printf("PRs created: %d\n", prs)
	}
	if errors > 0 {
		fmt.Printf("Errors: %d\n", errors)
	}

	return nil
}

// Git helper functions

func gitCreateBranch(repoPath, branchName string) error {
	cmd := exec.Command("git", "checkout", "-B", branchName)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func gitAdd(repoPath string, files []string) error {
	args := append([]string{"add"}, files...)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func gitCommit(repoPath, message string) error {
	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func gitPush(repoPath, branchName string) error {
	cmd := exec.Command("git", "push", "-u", "origin", branchName)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func ghCreatePR(repoPath, branchName, title string) (string, error) {
	body := `## Summary

This PR adds compliant CI/CD workflows that use the organization's reusable workflows.

## Changes

- Added go-ci.yaml - Go CI pipeline
- Added go-lint.yaml - Go linting with golangci-lint
- Added go-sast-codeql.yaml - CodeQL security scanning

Generated by [PipelineConductor](https://github.com/plexusone/pipelineconductor)`

	cmd := exec.Command("gh", "pr", "create",
		"--title", title,
		"--body", body,
		"--base", "main",
		"--head", branchName,
	)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}

	// Extract PR URL from output
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "https://") {
			return line, nil
		}
	}

	return strings.TrimSpace(string(output)), nil
}
