package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/plexusone/pipelineconductor/internal/collector"
	"github.com/plexusone/pipelineconductor/internal/compliance"
	"github.com/plexusone/pipelineconductor/internal/dashboard"
	"github.com/plexusone/pipelineconductor/internal/policy"
	"github.com/plexusone/pipelineconductor/internal/report"
	"github.com/plexusone/pipelineconductor/pkg/model"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check workflow compliance across repositories",
	Long: `Check workflow compliance across repositories against reference workflows.

Scans public repositories from GitHub organizations and/or users, filters by
language (Go, TypeScript, Crystal), and checks if they use the required
reusable workflows from a reference repository.

Example:
  pipelineconductor check --orgs agentplexus --users grokify --languages Go
  pipelineconductor check --users grokify --languages Go,TypeScript -f markdown
  pipelineconductor check --orgs myorg --ref-repo owner/.github --strict

Local filesystem scanning (no GitHub token required):
  pipelineconductor check --local ~/go/src/github.com --orgs plexusone --languages Go
  pipelineconductor check --local . --orgs myorg --ref-repo plexusone/.github

Generate a dashboard alongside JSON output:
  pipelineconductor check --users grokify --languages Go -o results.json --dashboard dashboard.json`,
	RunE: runCheck,
}

func init() {
	rootCmd.AddCommand(checkCmd)

	checkCmd.Flags().StringSliceP("users", "u", nil, "GitHub users to scan")
	checkCmd.Flags().StringSliceP("languages", "l", nil, "Filter by languages (Go, TypeScript, Crystal)")
	checkCmd.Flags().StringP("ref-repo", "r", "grokify/.github", "Reference workflow repository (owner/repo)")
	checkCmd.Flags().String("ref-branch", "main", "Branch in reference repo")
	checkCmd.Flags().StringP("output", "o", "", "Output file path (default: stdout)")
	checkCmd.Flags().StringP("format", "f", "json", "Output format: json, markdown, html")
	checkCmd.Flags().Bool("strict", false, "Require exact reusable workflow usage")
	checkCmd.Flags().Bool("include-archived", false, "Include archived repositories")
	checkCmd.Flags().Bool("include-forks", false, "Include forked repositories")
	checkCmd.Flags().StringP("dashboard", "d", "", "Generate Dashforge dashboard JSON to this path")
	checkCmd.Flags().String("data-url", "", "Data URL for dashboard (default: relative path to output file)")
	checkCmd.Flags().String("local", "", "Scan local filesystem instead of GitHub API (path to base directory)")
	checkCmd.Flags().String("policies", "", "Path to Cedar policy files or directory for evaluation")
	checkCmd.Flags().String("policy-action", "merge", "Action to evaluate (merge, build, deploy, release)")
	checkCmd.Flags().Bool("fail-on-deny", false, "Exit with error if any policy denies the action")

	_ = viper.BindPFlag("users", checkCmd.Flags().Lookup("users"))
	_ = viper.BindPFlag("languages", checkCmd.Flags().Lookup("languages"))
	_ = viper.BindPFlag("ref-repo", checkCmd.Flags().Lookup("ref-repo"))
	_ = viper.BindPFlag("ref-branch", checkCmd.Flags().Lookup("ref-branch"))
}

func runCheck(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	// Get configuration
	localPath, _ := cmd.Flags().GetString("local")
	token := viper.GetString("github_token")

	// Only require token for GitHub API mode
	if localPath == "" && token == "" {
		return fmt.Errorf("GitHub token is required (--github-token or GITHUB_TOKEN env var), or use --local for filesystem scanning")
	}

	orgs, _ := cmd.Flags().GetStringSlice("orgs")
	if len(orgs) == 0 {
		orgs = viper.GetStringSlice("orgs")
	}
	users, _ := cmd.Flags().GetStringSlice("users")
	if len(users) == 0 {
		users = viper.GetStringSlice("users")
	}
	if len(orgs) == 0 && len(users) == 0 {
		return fmt.Errorf("at least one organization (--orgs) or user (--users) is required")
	}

	languages, _ := cmd.Flags().GetStringSlice("languages")
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
	outputFile, _ := cmd.Flags().GetString("output")
	format, _ := cmd.Flags().GetString("format")
	strict, _ := cmd.Flags().GetBool("strict")
	includeArchived, _ := cmd.Flags().GetBool("include-archived")
	includeForks, _ := cmd.Flags().GetBool("include-forks")
	verbose := viper.GetBool("verbose")

	// Build filter
	filter := model.RepoFilter{
		IncludeArchived:  includeArchived,
		IncludeForks:     includeForks,
		IncludeLanguages: languages,
	}

	// Create collector (local or GitHub)
	var coll collector.Collector
	if localPath != "" {
		// Expand home directory
		if localPath == "~" || len(localPath) > 1 && localPath[:2] == "~/" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("expanding home directory: %w", err)
			}
			localPath = filepath.Join(home, localPath[1:])
		}

		localColl := collector.NewLocalCollectorWithConfig(collector.LocalCollectorConfig{
			BasePath: localPath,
			Verbose:  verbose,
		})
		coll = localColl

		if verbose {
			fmt.Fprintf(os.Stderr, "Local scanning mode: %s\n", localPath)
		}
	} else {
		ghColl, err := collector.NewGitHubCollector(ctx, token)
		if err != nil {
			return fmt.Errorf("creating GitHub collector: %w", err)
		}
		coll = ghColl
	}

	if verbose {
		if len(orgs) > 0 {
			fmt.Fprintf(os.Stderr, "Scanning organizations: %v\n", orgs)
		}
		if len(users) > 0 {
			fmt.Fprintf(os.Stderr, "Scanning users: %v\n", users)
		}
		fmt.Fprintf(os.Stderr, "Languages: %v\n", languages)
		fmt.Fprintf(os.Stderr, "Reference repo: %s@%s\n", refRepo, refBranch)
	}

	// List repositories
	repos, err := coll.ListReposMultiSource(ctx, orgs, users, filter)
	if err != nil {
		return fmt.Errorf("listing repositories: %w", err)
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "Found %d repositories\n", len(repos))
	}

	// Create compliance checker
	checker, err := compliance.NewChecker(coll, compliance.CheckerConfig{
		RefRepo:   refRepo,
		RefBranch: refBranch,
		Strict:    strict,
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

	// Update config with actual orgs/users used
	result.Config.Orgs = orgs
	result.Config.Users = users

	// Evaluate Cedar policies if requested
	policiesPath, _ := cmd.Flags().GetString("policies")
	policyAction, _ := cmd.Flags().GetString("policy-action")
	failOnDeny, _ := cmd.Flags().GetBool("fail-on-deny")

	var policyResults []PolicyEvalResult
	if policiesPath != "" {
		policyResults, err = evaluatePolicies(ctx, coll, result, policiesPath, policyAction, refRepo, verbose)
		if err != nil {
			return fmt.Errorf("evaluating policies: %w", err)
		}

		// Check for denials if fail-on-deny is set
		if failOnDeny {
			deniedCount := 0
			for _, pr := range policyResults {
				if !pr.Allowed {
					deniedCount++
				}
			}
			if deniedCount > 0 {
				// Still output the results before failing
				outputPolicyResults(policyResults, verbose)
				return fmt.Errorf("%d repositories denied by policy", deniedCount)
			}
		}

		// Output policy results if verbose
		if verbose {
			outputPolicyResults(policyResults, verbose)
		}
	}

	// Generate output
	var output []byte
	switch format {
	case "json":
		output, err = json.MarshalIndent(result, "", "  ")
	case "markdown", "md":
		formatter := &report.CheckMarkdownFormatter{}
		output, err = formatter.Format(result)
	case "html":
		formatter := &report.CheckHTMLFormatter{}
		output, err = formatter.Format(result)
	default:
		return fmt.Errorf("unsupported format: %s (supported: json, markdown, html)", format)
	}

	if err != nil {
		return fmt.Errorf("generating output: %w", err)
	}

	// Write output
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

	// Generate dashboard if requested
	dashboardFile, _ := cmd.Flags().GetString("dashboard")
	if dashboardFile != "" {
		dataURL, _ := cmd.Flags().GetString("data-url")
		if dataURL == "" {
			// Default: relative path from dashboard to data file
			if outputFile != "" {
				// Calculate relative path from dashboard dir to output file
				dashboardDir := filepath.Dir(dashboardFile)
				relPath, err := filepath.Rel(dashboardDir, outputFile)
				if err != nil {
					relPath = outputFile
				}
				dataURL = "./" + relPath
			} else {
				dataURL = "./data.json"
			}
		}

		db := dashboard.GenerateComplianceDashboard(result, dataURL)
		dbOutput, err := json.MarshalIndent(db, "", "  ")
		if err != nil {
			return fmt.Errorf("generating dashboard: %w", err)
		}

		if err := os.WriteFile(dashboardFile, dbOutput, 0600); err != nil {
			return fmt.Errorf("writing dashboard file: %w", err)
		}
		if verbose {
			fmt.Fprintf(os.Stderr, "Dashboard written to: %s\n", dashboardFile)
		}
	}

	return nil
}

// PolicyEvalResult contains the result of policy evaluation for a repository.
type PolicyEvalResult struct {
	RepoName string   `json:"repoName"`
	Action   string   `json:"action"`
	Allowed  bool     `json:"allowed"`
	Reasons  []string `json:"reasons,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

// evaluatePolicies evaluates Cedar policies against compliance results.
func evaluatePolicies(ctx context.Context, coll collector.Collector, result *model.CheckResult, policiesPath, action, refRepo string, verbose bool) ([]PolicyEvalResult, error) {
	// Create policy engine
	engine := policy.NewEngine()

	// Load policies
	if err := loadPolicies(engine, policiesPath); err != nil {
		return nil, fmt.Errorf("loading policies: %w", err)
	}

	// Create context builder
	builder := policy.NewContextBuilder(nil)

	var results []PolicyEvalResult

	for _, repoResult := range result.Repos {
		if repoResult.Skipped || repoResult.Error != "" {
			continue
		}

		// Get workflows for the repo
		repo := model.Repo{
			Owner:     repoResult.Owner,
			Name:      repoResult.Name,
			FullName:  repoResult.FullName,
			Languages: repoResult.Languages,
			HTMLURL:   repoResult.HTMLURL,
		}

		workflows, err := coll.GetWorkflows(ctx, repo)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "Warning: failed to get workflows for %s: %v\n", repo.FullName, err)
			}
			workflows = nil
		}

		// Build policy context with compliance data
		policyCtx := builder.BuildFromComplianceResult(repoResult, workflows, refRepo)

		// Evaluate policy
		evalResult := engine.Evaluate(policyCtx, action)

		results = append(results, PolicyEvalResult{
			RepoName: repoResult.FullName,
			Action:   action,
			Allowed:  evalResult.Allowed,
			Reasons:  evalResult.Reasons,
			Errors:   evalResult.Errors,
		})
	}

	return results, nil
}

// loadPolicies loads Cedar policies from a file or directory.
func loadPolicies(engine *policy.Engine, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("accessing policy path: %w", err)
	}

	if info.IsDir() {
		// Load all .cedar files from directory
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("reading policy directory: %w", err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if filepath.Ext(name) != ".cedar" {
				continue
			}

			filePath := filepath.Join(path, name)
			content, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading policy file %s: %w", filePath, err)
			}

			if err := engine.AddPolicy(name, content); err != nil {
				return fmt.Errorf("adding policy %s: %w", name, err)
			}
		}
	} else {
		// Load single file
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading policy file: %w", err)
		}

		if err := engine.AddPolicy(filepath.Base(path), content); err != nil {
			return fmt.Errorf("adding policy: %w", err)
		}
	}

	return nil
}

// outputPolicyResults prints policy evaluation results.
func outputPolicyResults(results []PolicyEvalResult, _ bool) {
	fmt.Fprintln(os.Stderr, "\n=== Policy Evaluation Results ===")

	allowed := 0
	denied := 0

	for _, r := range results {
		status := "ALLOWED"
		if !r.Allowed {
			status = "DENIED"
			denied++
		} else {
			allowed++
		}
		fmt.Fprintf(os.Stderr, "  %s: %s (%s)\n", r.RepoName, status, r.Action)
		for _, reason := range r.Reasons {
			fmt.Fprintf(os.Stderr, "    - Policy: %s\n", reason)
		}
		for _, e := range r.Errors {
			fmt.Fprintf(os.Stderr, "    - Error: %s\n", e)
		}
	}

	fmt.Fprintf(os.Stderr, "\nSummary: %d allowed, %d denied\n", allowed, denied)
}
