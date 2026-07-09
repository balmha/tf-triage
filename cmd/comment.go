package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Signature for deduplication (hidden HTML comment appended to posts)
// ---------------------------------------------------------------------------

const commentSignature = "<!-- tf-triage-comment -->"

// ---------------------------------------------------------------------------
// Flag variables
// ---------------------------------------------------------------------------

var (
	commentInput    string
	commentPlatform string
	commentPR       string
)

// ---------------------------------------------------------------------------
// Comment command
// ---------------------------------------------------------------------------

var commentCmd = &cobra.Command{
	Use:   "comment",
	Short: "Post the tf-triage report as a PR/MR comment",
	Long: `Reads a tf-triage markdown report and posts it as a comment on an active
Pull Request (GitHub), Merge Request (GitLab), or Pull Request (Bitbucket).

The platform is auto-detected from CI environment variables, or can be set
explicitly with --platform. Existing tf-triage comments are updated in place
to avoid duplicate spam.

Examples:
  tf-triage comment
  tf-triage comment -i custom-report.md --platform github --pr 42
  tf-triage comment --platform gitlab`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runComment,
}

func init() {
	commentCmd.Flags().StringVarP(&commentInput, "input", "i", "tf-triage-results.md", "Path to the markdown report file")
	commentCmd.Flags().StringVar(&commentPlatform, "platform", "", "Target platform: 'github', 'gitlab', or 'bitbucket' (auto-detected if omitted)")
	commentCmd.Flags().StringVar(&commentPR, "pr", "", "Pull/Merge request number (auto-detected from CI env if omitted)")
}

// ---------------------------------------------------------------------------
// Core execution
// ---------------------------------------------------------------------------

func runComment(cmd *cobra.Command, args []string) error {
	// Step 1: Read the report file
	content, err := os.ReadFile(commentInput)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("report file not found: %s\n\n  Hint: run 'tf-triage' first to generate the report, or specify a path with --input", commentInput)
		}
		return fmt.Errorf("cannot read report file %q: %w", commentInput, err)
	}

	if len(bytes.TrimSpace(content)) == 0 {
		return fmt.Errorf("report file is empty: %s", commentInput)
	}

	// Append signature for deduplication
	body := string(content) + "\n\n" + commentSignature

	// Step 2: Detect platform
	platform := resolvePlatform(commentPlatform)
	if platform == "" {
		return fmt.Errorf("could not detect CI platform\n\n  Hint: set --platform explicitly (github, gitlab, or bitbucket)\n  Or run inside a CI environment (GitHub Actions, GitLab CI, Bitbucket Pipelines)")
	}

	// Step 3: Route to the correct poster
	switch platform {
	case "github":
		return postGitHub(body)
	case "gitlab":
		return postGitLab(body)
	case "bitbucket":
		return postBitbucket(body)
	default:
		return fmt.Errorf("unsupported platform %q; use github, gitlab, or bitbucket", platform)
	}
}

// ---------------------------------------------------------------------------
// Platform detection
// ---------------------------------------------------------------------------

func resolvePlatform(explicit string) string {
	if explicit != "" {
		return strings.ToLower(explicit)
	}

	if os.Getenv("GITHUB_ACTIONS") == "true" {
		return "github"
	}
	if os.Getenv("GITLAB_CI") != "" {
		return "gitlab"
	}
	if os.Getenv("BITBUCKET_BUILD_NUMBER") != "" {
		return "bitbucket"
	}

	return ""
}

// ---------------------------------------------------------------------------
// GitHub
// ---------------------------------------------------------------------------

func postGitHub(body string) error {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN environment variable must be set to post comments in GitHub Actions\n\n  Hint: add 'permissions: pull-requests: write' to your workflow")
	}

	repo := os.Getenv("GITHUB_REPOSITORY") // "owner/repo"
	if repo == "" {
		return fmt.Errorf("GITHUB_REPOSITORY environment variable is not set")
	}

	prNumber := commentPR
	if prNumber == "" {
		prNumber = extractGitHubPRNumber()
	}
	if prNumber == "" {
		return fmt.Errorf("could not determine PR number\n\n  Hint: use --pr flag to specify the pull request number explicitly")
	}

	// Check for existing comment and update if found
	existingID := findExistingGitHubComment(token, repo, prNumber)
	if existingID != "" {
		return updateGitHubComment(token, repo, existingID, body)
	}

	// Create new comment
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%s/comments", repo, prNumber)
	payload := map[string]string{"body": body}

	resp, err := doAPIRequest("POST", url, map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/vnd.github+json",
	}, payload)
	if err != nil {
		return fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API error (HTTP %d): %s", resp.StatusCode, truncateAPIBody(respBody, 300))
	}

	fmt.Println("✔ Comment posted successfully on GitHub PR #" + prNumber)
	return nil
}

func extractGitHubPRNumber() string {
	// GITHUB_REF_NAME can be "15/merge" for PR events
	refName := os.Getenv("GITHUB_REF_NAME")
	if parts := strings.Split(refName, "/"); len(parts) >= 1 {
		// Check if the first part is numeric
		if isNumeric(parts[0]) {
			return parts[0]
		}
	}

	// GITHUB_REF can be "refs/pull/15/merge"
	ref := os.Getenv("GITHUB_REF")
	if strings.HasPrefix(ref, "refs/pull/") {
		parts := strings.Split(ref, "/")
		if len(parts) >= 3 {
			return parts[2]
		}
	}

	return ""
}

func findExistingGitHubComment(token, repo, prNumber string) string {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%s/comments?per_page=100", repo, prNumber)

	resp, err := doAPIRequest("GET", url, map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/vnd.github+json",
	}, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	defer resp.Body.Close()

	var comments []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return ""
	}

	for _, c := range comments {
		if strings.Contains(c.Body, commentSignature) {
			return fmt.Sprintf("%d", c.ID)
		}
	}
	return ""
}

func updateGitHubComment(token, repo, commentID, body string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/comments/%s", repo, commentID)
	payload := map[string]string{"body": body}

	resp, err := doAPIRequest("PATCH", url, map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/vnd.github+json",
	}, payload)
	if err != nil {
		return fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API error updating comment (HTTP %d): %s", resp.StatusCode, truncateAPIBody(respBody, 300))
	}

	fmt.Println("✔ Existing tf-triage comment updated on GitHub PR")
	return nil
}

// ---------------------------------------------------------------------------
// GitLab
// ---------------------------------------------------------------------------

func postGitLab(body string) error {
	token := os.Getenv("GITLAB_TOKEN")
	if token == "" {
		token = os.Getenv("CI_JOB_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("GITLAB_TOKEN environment variable must be set to post comments in GitLab CI\n\n  Hint: set GITLAB_TOKEN as a CI/CD variable with 'api' scope, or use CI_JOB_TOKEN if permissions allow")
	}

	projectID := os.Getenv("CI_PROJECT_ID")
	if projectID == "" {
		return fmt.Errorf("CI_PROJECT_ID environment variable is not set")
	}

	mrIID := commentPR
	if mrIID == "" {
		mrIID = os.Getenv("CI_MERGE_REQUEST_IID")
	}
	if mrIID == "" {
		return fmt.Errorf("could not determine Merge Request IID\n\n  Hint: use --pr flag or ensure CI_MERGE_REQUEST_IID is set (requires merge_request_event pipeline)")
	}

	// Check for existing comment and update if found
	existingID := findExistingGitLabComment(token, projectID, mrIID)
	if existingID != "" {
		return updateGitLabComment(token, projectID, mrIID, existingID, body)
	}

	// Create new comment
	url := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/merge_requests/%s/notes", projectID, mrIID)
	payload := map[string]string{"body": body}

	resp, err := doAPIRequest("POST", url, map[string]string{
		"PRIVATE-TOKEN": token,
	}, payload)
	if err != nil {
		return fmt.Errorf("GitLab API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitLab API error (HTTP %d): %s", resp.StatusCode, truncateAPIBody(respBody, 300))
	}

	fmt.Println("✔ Comment posted successfully on GitLab MR !" + mrIID)
	return nil
}

func findExistingGitLabComment(token, projectID, mrIID string) string {
	url := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/merge_requests/%s/notes?per_page=100", projectID, mrIID)

	resp, err := doAPIRequest("GET", url, map[string]string{
		"PRIVATE-TOKEN": token,
	}, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	defer resp.Body.Close()

	var notes []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&notes); err != nil {
		return ""
	}

	for _, n := range notes {
		if strings.Contains(n.Body, commentSignature) {
			return fmt.Sprintf("%d", n.ID)
		}
	}
	return ""
}

func updateGitLabComment(token, projectID, mrIID, noteID, body string) error {
	url := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/merge_requests/%s/notes/%s", projectID, mrIID, noteID)
	payload := map[string]string{"body": body}

	resp, err := doAPIRequest("PUT", url, map[string]string{
		"PRIVATE-TOKEN": token,
	}, payload)
	if err != nil {
		return fmt.Errorf("GitLab API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitLab API error updating note (HTTP %d): %s", resp.StatusCode, truncateAPIBody(respBody, 300))
	}

	fmt.Println("✔ Existing tf-triage comment updated on GitLab MR !" + mrIID)
	return nil
}

// ---------------------------------------------------------------------------
// Bitbucket
// ---------------------------------------------------------------------------

func postBitbucket(body string) error {
	token := os.Getenv("BITBUCKET_TOKEN")
	if token == "" {
		return fmt.Errorf("BITBUCKET_TOKEN environment variable must be set to post comments in Bitbucket Pipelines\n\n  Hint: set BITBUCKET_TOKEN as a repository variable with an App Password or Repository Access Token")
	}

	workspace := os.Getenv("BITBUCKET_WORKSPACE")
	if workspace == "" {
		return fmt.Errorf("BITBUCKET_WORKSPACE environment variable is not set")
	}

	repoSlug := os.Getenv("BITBUCKET_REPO_SLUG")
	if repoSlug == "" {
		return fmt.Errorf("BITBUCKET_REPO_SLUG environment variable is not set")
	}

	prID := commentPR
	if prID == "" {
		prID = os.Getenv("BITBUCKET_PR_ID")
	}
	if prID == "" {
		return fmt.Errorf("could not determine Pull Request ID\n\n  Hint: use --pr flag or ensure BITBUCKET_PR_ID is set")
	}

	// Check for existing comment and update if found
	existingID := findExistingBitbucketComment(token, workspace, repoSlug, prID)
	if existingID != "" {
		return updateBitbucketComment(token, workspace, repoSlug, prID, existingID, body)
	}

	// Create new comment
	url := fmt.Sprintf("https://api.bitbucket.org/2.0/repositories/%s/%s/pullrequests/%s/comments", workspace, repoSlug, prID)
	payload := map[string]interface{}{
		"content": map[string]string{
			"raw": body,
		},
	}

	resp, err := doAPIRequest("POST", url, map[string]string{
		"Authorization": "Bearer " + token,
	}, payload)
	if err != nil {
		return fmt.Errorf("Bitbucket API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Bitbucket API error (HTTP %d): %s", resp.StatusCode, truncateAPIBody(respBody, 300))
	}

	fmt.Println("✔ Comment posted successfully on Bitbucket PR #" + prID)
	return nil
}

func findExistingBitbucketComment(token, workspace, repoSlug, prID string) string {
	url := fmt.Sprintf("https://api.bitbucket.org/2.0/repositories/%s/%s/pullrequests/%s/comments?pagelen=100", workspace, repoSlug, prID)

	resp, err := doAPIRequest("GET", url, map[string]string{
		"Authorization": "Bearer " + token,
	}, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		Values []struct {
			ID      int `json:"id"`
			Content struct {
				Raw string `json:"raw"`
			} `json:"content"`
		} `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}

	for _, c := range result.Values {
		if strings.Contains(c.Content.Raw, commentSignature) {
			return fmt.Sprintf("%d", c.ID)
		}
	}
	return ""
}

func updateBitbucketComment(token, workspace, repoSlug, prID, commentID, body string) error {
	url := fmt.Sprintf("https://api.bitbucket.org/2.0/repositories/%s/%s/pullrequests/%s/comments/%s", workspace, repoSlug, prID, commentID)
	payload := map[string]interface{}{
		"content": map[string]string{
			"raw": body,
		},
	}

	resp, err := doAPIRequest("PUT", url, map[string]string{
		"Authorization": "Bearer " + token,
	}, payload)
	if err != nil {
		return fmt.Errorf("Bitbucket API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Bitbucket API error updating comment (HTTP %d): %s", resp.StatusCode, truncateAPIBody(respBody, 300))
	}

	fmt.Println("✔ Existing tf-triage comment updated on Bitbucket PR #" + prID)
	return nil
}

// ---------------------------------------------------------------------------
// Shared HTTP helper
// ---------------------------------------------------------------------------

func doAPIRequest(method, url string, headers map[string]string, payload interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func truncateAPIBody(data []byte, maxLen int) string {
	s := string(data)
	if len(s) > maxLen {
		return s[:maxLen] + "... (truncated)"
	}
	return s
}
