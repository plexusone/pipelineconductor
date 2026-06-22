package collector

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v88/github"
	"github.com/grokify/gogithub/repo"
	"github.com/grokify/mogo/net/http/retryhttp"
	"golang.org/x/oauth2"

	"github.com/plexusone/pipelineconductor/pkg/model"
)

// GitHubCollector collects repository data from GitHub.
type GitHubCollector struct {
	client *github.Client
	logger *slog.Logger
}

// Options configures the GitHub collector.
type Options struct {
	// MaxRetries is the maximum number of retry attempts for rate-limited requests.
	MaxRetries int
	// Logger is used for logging rate limit events.
	Logger *slog.Logger
	// Verbose enables verbose logging of rate limit events.
	Verbose bool
}

// DefaultOptions returns sensible defaults for the collector.
func DefaultOptions() Options {
	return Options{
		MaxRetries: 5,
		Logger:     nil,
		Verbose:    false,
	}
}

// NewGitHubCollector creates a new GitHub collector with the given token.
func NewGitHubCollector(_ context.Context, token string) (*GitHubCollector, error) {
	return NewGitHubCollectorWithOptions(token, DefaultOptions())
}

// NewGitHubCollectorWithOptions creates a new GitHub collector with custom options.
func NewGitHubCollectorWithOptions(token string, opts Options) (*GitHubCollector, error) {
	// Create OAuth2 transport
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	oauthTransport := &oauth2.Transport{
		Source: ts,
		Base:   http.DefaultTransport,
	}

	// Create retry transport with GitHub-specific rate limit handling
	retryTransport := retryhttp.NewWithOptions(
		retryhttp.WithTransport(oauthTransport),
		retryhttp.WithMaxRetries(opts.MaxRetries),
		retryhttp.WithInitialBackoff(1*time.Second),
		retryhttp.WithMaxBackoff(60*time.Second),
		retryhttp.WithShouldRetry(shouldRetryGitHub),
		retryhttp.WithOnRetry(makeOnRetryCallback(opts.Logger, opts.Verbose)),
	)

	httpClient := &http.Client{Transport: retryTransport}
	client, err := github.NewClient(github.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating GitHub client: %w", err)
	}
	return &GitHubCollector{
		client: client,
		logger: opts.Logger,
	}, nil
}

// shouldRetryGitHub determines if a GitHub API request should be retried.
// It handles both primary rate limits (X-RateLimit-Remaining=0) and
// secondary rate limits (403/429 with Retry-After header).
func shouldRetryGitHub(resp *http.Response, err error) bool {
	// Retry on connection errors
	if err != nil {
		return true
	}
	if resp == nil {
		return false
	}

	// GitHub returns 403 for rate limits, 429 for abuse detection
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		// Check for rate limit headers
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return true
		}
		// Check for Retry-After (secondary rate limit)
		if resp.Header.Get("Retry-After") != "" {
			return true
		}
		// Check for rate limit error message in response
		if resp.Header.Get("X-RateLimit-Limit") != "" {
			return true
		}
	}

	// Retry on server errors
	switch resp.StatusCode {
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}

	return false
}

// makeOnRetryCallback creates a callback that logs retry attempts.
func makeOnRetryCallback(logger *slog.Logger, verbose bool) func(int, *http.Request, *http.Response, error, time.Duration) {
	return func(attempt int, req *http.Request, resp *http.Response, _ error, backoff time.Duration) {
		if logger == nil && !verbose {
			return
		}

		msg := fmt.Sprintf("Rate limited, retrying in %v (attempt %d)", backoff, attempt)
		if resp != nil {
			if remaining := resp.Header.Get("X-RateLimit-Remaining"); remaining != "" {
				msg = fmt.Sprintf("%s [remaining: %s]", msg, remaining)
			}
			if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
				if resetTime, err := strconv.ParseInt(reset, 10, 64); err == nil {
					resetAt := time.Unix(resetTime, 0)
					msg = fmt.Sprintf("%s [resets: %s]", msg, resetAt.Format(time.RFC3339))
				}
			}
		}

		if logger != nil {
			logger.Info(msg,
				slog.Int("attempt", attempt),
				slog.Duration("backoff", backoff),
				slog.String("url", req.URL.String()),
			)
		}
	}
}

// NewGitHubCollectorWithClient creates a collector with a custom GitHub client.
func NewGitHubCollectorWithClient(client *github.Client) *GitHubCollector {
	return &GitHubCollector{client: client}
}

// ListRepos returns repositories for the specified organizations.
func (c *GitHubCollector) ListRepos(ctx context.Context, orgs []string, filter model.RepoFilter) ([]model.Repo, error) {
	var repos []model.Repo

	for _, org := range orgs {
		orgRepos, err := c.listOrgRepos(ctx, org, filter)
		if err != nil {
			return nil, fmt.Errorf("listing repos for org %s: %w", org, err)
		}
		repos = append(repos, orgRepos...)
	}

	return repos, nil
}

// ListUserRepos returns public repositories for the specified users.
func (c *GitHubCollector) ListUserRepos(ctx context.Context, users []string, filter model.RepoFilter) ([]model.Repo, error) {
	var repos []model.Repo

	for _, user := range users {
		userRepos, err := c.listUserRepos(ctx, user, filter)
		if err != nil {
			return nil, fmt.Errorf("listing repos for user %s: %w", user, err)
		}
		repos = append(repos, userRepos...)
	}

	return repos, nil
}

// ListReposMultiSource returns repositories from both orgs and users.
func (c *GitHubCollector) ListReposMultiSource(ctx context.Context, orgs, users []string, filter model.RepoFilter) ([]model.Repo, error) {
	var allRepos []model.Repo

	// Collect from organizations
	if len(orgs) > 0 {
		orgRepos, err := c.ListRepos(ctx, orgs, filter)
		if err != nil {
			return nil, err
		}
		allRepos = append(allRepos, orgRepos...)
	}

	// Collect from users
	if len(users) > 0 {
		userRepos, err := c.ListUserRepos(ctx, users, filter)
		if err != nil {
			return nil, err
		}
		allRepos = append(allRepos, userRepos...)
	}

	// Deduplicate by full name
	seen := make(map[string]bool)
	var dedupedRepos []model.Repo
	for _, r := range allRepos {
		if !seen[r.FullName] {
			seen[r.FullName] = true
			dedupedRepos = append(dedupedRepos, r)
		}
	}

	return dedupedRepos, nil
}

func (c *GitHubCollector) listUserRepos(ctx context.Context, user string, filter model.RepoFilter) ([]model.Repo, error) {
	opts := &github.RepositoryListByUserOptions{
		Type:        "owner",
		Sort:        "updated",
		ListOptions: github.ListOptions{PerPage: 100},
	}

	var repos []model.Repo
	for {
		ghRepos, resp, err := c.client.Repositories.ListByUser(ctx, user, opts)
		if err != nil {
			return nil, err
		}

		for _, gr := range ghRepos {
			r := convertGitHubRepo(gr)
			if r.Matches(filter) {
				repos = append(repos, r)
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return repos, nil
}

func (c *GitHubCollector) listOrgRepos(ctx context.Context, org string, filter model.RepoFilter) ([]model.Repo, error) {
	ghRepos, err := repo.ListOrgRepos(ctx, c.client, org)
	if err != nil {
		return nil, err
	}

	var repos []model.Repo
	for _, gr := range ghRepos {
		r := convertGitHubRepo(gr)
		if r.Matches(filter) {
			repos = append(repos, r)
		}
	}

	return repos, nil
}

// GetWorkflows returns workflow files for a repository.
func (c *GitHubCollector) GetWorkflows(ctx context.Context, repo model.Repo) ([]model.Workflow, error) {
	workflows, _, err := c.client.Actions.ListWorkflows(ctx, repo.Owner, repo.Name, &github.ListOptions{PerPage: 100})
	if err != nil {
		return nil, fmt.Errorf("listing workflows: %w", err)
	}

	var result []model.Workflow
	for _, wf := range workflows.Workflows {
		workflow := model.Workflow{
			Name:  wf.GetName(),
			Path:  wf.GetPath(),
			State: wf.GetState(),
		}

		// Fetch workflow content
		content, err := c.GetFileContent(ctx, repo, wf.GetPath())
		if err == nil {
			workflow.Content = content
		}

		result = append(result, workflow)
	}

	return result, nil
}

// GetBranchProtection returns branch protection settings.
func (c *GitHubCollector) GetBranchProtection(ctx context.Context, repo model.Repo, branch string) (*model.BranchProtection, error) {
	protection, resp, err := c.client.Repositories.GetBranchProtection(ctx, repo.Owner, repo.Name, branch)
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return &model.BranchProtection{Branch: branch, Enabled: false}, nil
		}
		return nil, fmt.Errorf("getting branch protection: %w", err)
	}

	bp := &model.BranchProtection{
		Branch:  branch,
		Enabled: true,
	}

	if protection.RequiredPullRequestReviews != nil {
		bp.RequireReviews = true
		bp.RequiredReviewers = protection.RequiredPullRequestReviews.RequiredApprovingReviewCount
	}

	if protection.RequiredStatusChecks != nil {
		bp.RequireStatusChecks = true
		if protection.RequiredStatusChecks.Contexts != nil {
			bp.RequiredStatusChecks = *protection.RequiredStatusChecks.Contexts
		}
	}

	if protection.EnforceAdmins != nil {
		bp.EnforceAdmins = protection.EnforceAdmins.Enabled
	}

	if protection.RequiredSignatures != nil && protection.RequiredSignatures.Enabled != nil {
		bp.RequireSignedCommits = *protection.RequiredSignatures.Enabled
	}

	if protection.AllowForcePushes != nil {
		bp.AllowForcePushes = protection.AllowForcePushes.Enabled
	}

	if protection.AllowDeletions != nil {
		bp.AllowDeletions = protection.AllowDeletions.Enabled
	}

	return bp, nil
}

// GetLatestWorkflowRun returns the most recent workflow run.
func (c *GitHubCollector) GetLatestWorkflowRun(ctx context.Context, repo model.Repo, workflowID int64) (*model.WorkflowRun, error) {
	runs, _, err := c.client.Actions.ListWorkflowRunsByID(ctx, repo.Owner, repo.Name, workflowID, &github.ListWorkflowRunsOptions{
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if err != nil {
		return nil, fmt.Errorf("listing workflow runs: %w", err)
	}

	if len(runs.WorkflowRuns) == 0 {
		return nil, nil
	}

	run := runs.WorkflowRuns[0]
	return &model.WorkflowRun{
		ID:         run.GetID(),
		WorkflowID: run.GetWorkflowID(),
		Name:       run.GetName(),
		Status:     run.GetStatus(),
		Conclusion: run.GetConclusion(),
		Branch:     run.GetHeadBranch(),
		HeadSHA:    run.GetHeadSHA(),
		CreatedAt:  run.GetCreatedAt().Time,
		UpdatedAt:  run.GetUpdatedAt().Time,
		HTMLURL:    run.GetHTMLURL(),
	}, nil
}

// GetFileContent returns the content of a file from a repository.
func (c *GitHubCollector) GetFileContent(ctx context.Context, repo model.Repo, path string) (string, error) {
	content, _, _, err := c.client.Repositories.GetContents(ctx, repo.Owner, repo.Name, path, nil)
	if err != nil {
		return "", fmt.Errorf("getting file content: %w", err)
	}

	if content.Content == nil {
		return "", fmt.Errorf("file content is nil")
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(*content.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("decoding content: %w", err)
	}

	return string(decoded), nil
}

// GetLanguages returns the languages used in a repository.
func (c *GitHubCollector) GetLanguages(ctx context.Context, repo model.Repo) ([]string, error) {
	langs, _, err := c.client.Repositories.ListLanguages(ctx, repo.Owner, repo.Name)
	if err != nil {
		return nil, fmt.Errorf("listing languages: %w", err)
	}

	var result []string
	for lang := range langs {
		result = append(result, lang)
	}
	return result, nil
}

func convertGitHubRepo(gr *github.Repository) model.Repo {
	repo := model.Repo{
		Owner:         gr.GetOwner().GetLogin(),
		Name:          gr.GetName(),
		FullName:      gr.GetFullName(),
		DefaultBranch: gr.GetDefaultBranch(),
		Visibility:    gr.GetVisibility(),
		Archived:      gr.GetArchived(),
		Fork:          gr.GetFork(),
		HTMLURL:       gr.GetHTMLURL(),
		CloneURL:      gr.GetCloneURL(),
	}

	if gr.Language != nil {
		repo.PrimaryLanguage = *gr.Language
		repo.Languages = []string{*gr.Language}
	}

	repo.Topics = gr.Topics

	if gr.CreatedAt != nil {
		repo.CreatedAt = gr.CreatedAt.Time
	}
	if gr.UpdatedAt != nil {
		repo.UpdatedAt = gr.UpdatedAt.Time
	}
	if gr.PushedAt != nil {
		repo.PushedAt = gr.PushedAt.Time
	}

	return repo
}
