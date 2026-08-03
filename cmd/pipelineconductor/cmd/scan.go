package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/plexusone/pipelineconductor/internal/collector"
	"github.com/plexusone/pipelineconductor/internal/policy"
	"github.com/plexusone/pipelineconductor/internal/report"
	"github.com/plexusone/pipelineconductor/pkg/model"
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan repositories for compliance",
	Long: `Scan repositories across one or more GitHub organizations,
evaluate them against policies, and generate a compliance report.

Example:
  pipelineconductor scan --orgs myorg --output report.json
  pipelineconductor scan --orgs org1,org2 --profile modern --format markdown
  pipelineconductor scan --orgs myorg --policy-dir ./policies`,
	RunE: runScan,
}

func init() {
	rootCmd.AddCommand(scanCmd)

	scanCmd.Flags().StringP("output", "o", "", "Output file path (default: stdout)")
	scanCmd.Flags().StringP("format", "f", "json", "Output format: json, markdown, sarif, csv")
	scanCmd.Flags().Bool("include-archived", false, "Include archived repositories")
	scanCmd.Flags().Bool("include-forks", false, "Include forked repositories")
	scanCmd.Flags().StringSlice("languages", nil, "Filter by languages (e.g., Go,Python)")
	scanCmd.Flags().StringSlice("topics", nil, "Filter by topics")
	scanCmd.Flags().String("policy-dir", "", "Directory containing Cedar policy files")
	scanCmd.Flags().Bool("builtin-policies", true, "Use built-in policies")
	scanCmd.Flags().Bool("evaluate-policies", true, "Evaluate Cedar policies")

	_ = viper.BindPFlag("output", scanCmd.Flags().Lookup("output"))
	_ = viper.BindPFlag("format", scanCmd.Flags().Lookup("format"))
}

func runScan(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	startTime := time.Now()

	// Get configuration
	token := viper.GetString("github_token")
	if token == "" {
		return fmt.Errorf("GitHub token is required (--github-token or GITHUB_TOKEN env var)")
	}

	orgs := viper.GetStringSlice("orgs")
	if len(orgs) == 0 {
		return fmt.Errorf("at least one organization is required (--orgs)")
	}

	verbose := viper.GetBool("verbose")
	outputFile := viper.GetString("output")
	format := viper.GetString("format")
	profileName := viper.GetString("profile")
	policyDir, _ := cmd.Flags().GetString("policy-dir")
	useBuiltinPolicies, _ := cmd.Flags().GetBool("builtin-policies")
	evaluatePolicies, _ := cmd.Flags().GetBool("evaluate-policies")

	// Build filter
	filter := model.RepoFilter{
		IncludeArchived:  viper.GetBool("include-archived"),
		IncludeForks:     viper.GetBool("include-forks"),
		IncludeLanguages: viper.GetStringSlice("languages"),
		IncludeTopics:    viper.GetStringSlice("topics"),
	}

	// Initialize policy engine
	var engine *policy.Engine
	var profileMgr *policy.ProfileManager
	var profile *model.Profile

	if evaluatePolicies {
		engine = policy.NewEngine()
		loader := policy.NewLoader(engine)

		// Load built-in policies
		if useBuiltinPolicies {
			if err := loader.LoadBuiltinPolicies(); err != nil {
				return fmt.Errorf("loading built-in policies: %w", err)
			}
			if verbose {
				fmt.Fprintln(os.Stderr, "Loaded built-in policies")
			}
		}

		// Load policies from directory
		if policyDir != "" {
			if err := loader.LoadFromDirectory(policyDir); err != nil {
				return fmt.Errorf("loading policies from %s: %w", policyDir, err)
			}
			if verbose {
				fmt.Fprintf(os.Stderr, "Loaded policies from: %s\n", policyDir)
			}
		}

		// Initialize profile manager
		profileMgr = policy.NewProfileManager()
		profileMgr.LoadBuiltinProfiles()
		profile = profileMgr.GetOrDefault(profileName)

		if verbose {
			fmt.Fprintf(os.Stderr, "Using profile: %s\n", profile.Name)
		}
	}

	// Create collector
	ghCollector, err := collector.NewGitHubCollector(ctx, token)
	if err != nil {
		return fmt.Errorf("creating GitHub collector: %w", err)
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "Scanning organizations: %v\n", orgs)
	}

	// List repositories
	repos, err := ghCollector.ListRepos(ctx, orgs, filter)
	if err != nil {
		return fmt.Errorf("listing repositories: %w", err)
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "Found %d repositories\n", len(repos))
	}

	// Build result
	result := model.ComplianceResult{
		Timestamp: time.Now(),
		Config: model.ScanConfig{
			Orgs:       orgs,
			PolicyRepo: viper.GetString("policy_repo"),
			Profile:    profileName,
			Filter:     filter,
		},
	}

	// Context builder for policy evaluation
	var ctxBuilder *policy.ContextBuilder
	if evaluatePolicies && profile != nil {
		ctxBuilder = policy.NewContextBuilder(profile)
	}

	// Scan each repository
	for _, repo := range repos {
		repoStartTime := time.Now()

		if verbose {
			fmt.Fprintf(os.Stderr, "Scanning: %s\n", repo.FullName)
		}

		repoResult := model.RepoResult{
			Repo:      repo,
			Compliant: true,
		}

		// Get workflows
		workflows, err := ghCollector.GetWorkflows(ctx, repo)
		if err != nil {
			repoResult.Warnings = append(repoResult.Warnings, model.Warning{
				Code:    "workflow-fetch-failed",
				Message: fmt.Sprintf("Could not fetch workflows: %v", err),
			})
		}

		// Check for workflow existence
		if len(workflows) == 0 {
			repoResult.Violations = append(repoResult.Violations, model.Violation{
				Policy:      "ci/workflow-required",
				Rule:        "has-workflow",
				Message:     "No CI/CD workflow found",
				Severity:    model.SeverityHigh,
				Remediation: "Create a .github/workflows/ci.yml file",
			})
			repoResult.Compliant = false
		}

		// Get branch protection
		bp, err := ghCollector.GetBranchProtection(ctx, repo, repo.DefaultBranch)
		if err != nil {
			repoResult.Warnings = append(repoResult.Warnings, model.Warning{
				Code:    "branch-protection-check-failed",
				Message: fmt.Sprintf("Could not check branch protection: %v", err),
			})
		} else if !bp.Enabled {
			repoResult.Violations = append(repoResult.Violations, model.Violation{
				Policy:      "security/branch-protection",
				Rule:        "protection-enabled",
				Message:     fmt.Sprintf("Branch protection not enabled on %s", repo.DefaultBranch),
				Severity:    model.SeverityMedium,
				Remediation: "Enable branch protection in repository settings",
			})
			repoResult.Compliant = false
		}

		// Evaluate Cedar policies
		if evaluatePolicies && ctxBuilder != nil && engine != nil {
			policyCtx := ctxBuilder.Build(repo, workflows, bp)

			// Evaluate all actions
			evalResults := engine.EvaluateAll(policyCtx)
			for _, evalResult := range evalResults {
				if !evalResult.Allowed {
					if violation := evalResult.ToViolation(); violation != nil {
						repoResult.Violations = append(repoResult.Violations, *violation)
						repoResult.Compliant = false
					}
				}
			}

			// Validate against profile
			if profile != nil {
				profileViolations := policy.ValidateRepoAgainstProfile(policyCtx, profile)
				repoResult.Violations = append(repoResult.Violations, profileViolations...)
				if len(profileViolations) > 0 {
					repoResult.Compliant = false
				}
			}
		}

		repoResult.ScanTimeMs = time.Since(repoStartTime).Milliseconds()
		result.Repos = append(result.Repos, repoResult)
	}

	// Calculate summary
	for _, r := range result.Repos {
		if r.Skipped {
			result.Summary.Skipped++
		} else if r.Error != "" {
			result.Summary.Errors++
		} else if r.Compliant {
			result.Summary.CompliantRepos++
		} else {
			result.Summary.NonCompliant++
		}
	}
	result.Summary.TotalRepos = len(result.Repos)
	if result.Summary.TotalRepos > 0 {
		result.Summary.ComplianceRate = float64(result.Summary.CompliantRepos) / float64(result.Summary.TotalRepos) * 100
	}
	result.ScanDurationMs = time.Since(startTime).Milliseconds()

	// Output result using report builder
	reportFormat, err := report.ParseFormat(format)
	if err != nil {
		return fmt.Errorf("invalid format: %w", err)
	}

	builder := report.NewBuilder()
	output, err := builder.Generate(&result, reportFormat)
	if err != nil {
		return fmt.Errorf("generating report: %w", err)
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
