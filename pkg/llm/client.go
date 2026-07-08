// Package llm provides a provider-agnostic interface for sending Terraform plan
// analysis requests to LLM APIs (Anthropic Claude, OpenAI GPT).
//
// It uses a factory pattern to instantiate the correct provider client based on
// configuration, and enforces timeout contexts on all HTTP calls.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tf-triage/tf-triage/pkg/parser"
)

// ---------------------------------------------------------------------------
// Typed errors
// ---------------------------------------------------------------------------

// ErrUnsupportedProvider indicates the requested provider is not implemented.
var ErrUnsupportedProvider = errors.New("unsupported LLM provider")

// ErrAPIKeyMissing indicates the required API key was not provided.
var ErrAPIKeyMissing = errors.New("API key not configured")

// ErrAPITimeout indicates the LLM API did not respond within the deadline.
var ErrAPITimeout = errors.New("LLM API request timed out")

// ErrAPIFailure indicates the LLM API returned a non-200 response.
var ErrAPIFailure = errors.New("LLM API returned an error")

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// Config holds all settings needed to call an LLM provider.
type Config struct {
	Provider string // "anthropic" or "openai"
	Model    string // Model identifier (e.g., "claude-3-5-sonnet-20241022")
	APIKey   string // Provider API key
	Timeout  time.Duration // HTTP request timeout (default: 120s)
}

// DefaultTimeout is applied when Config.Timeout is zero.
const DefaultTimeout = 120 * time.Second

// ---------------------------------------------------------------------------
// Provider interface & factory
// ---------------------------------------------------------------------------

// Provider defines the contract for an LLM backend.
type Provider interface {
	// Analyze sends the optimized plan to the LLM and returns Markdown output.
	Analyze(ctx context.Context, plan *parser.OptimizedPlan) (string, error)
}

// NewProvider is the factory function that returns the appropriate Provider
// implementation based on the config.
func NewProvider(cfg Config) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: set the appropriate environment variable for provider %q",
			ErrAPIKeyMissing, cfg.Provider)
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	switch strings.ToLower(cfg.Provider) {
	case "anthropic":
		return &anthropicProvider{
			apiKey: cfg.APIKey,
			model:  cfg.Model,
			client: &http.Client{Timeout: timeout},
		}, nil

	case "openai":
		return &openaiProvider{
			apiKey: cfg.APIKey,
			model:  cfg.Model,
			client: &http.Client{Timeout: timeout},
		}, nil

	default:
		return nil, fmt.Errorf("%w: %q (supported: anthropic, openai)",
			ErrUnsupportedProvider, cfg.Provider)
	}
}

// ---------------------------------------------------------------------------
// System prompt (shared across providers)
// ---------------------------------------------------------------------------

const systemPrompt = `You are an elite Cloud Security Architect and Platform Engineer.

Objective: Analyze the provided Terraform/OpenTofu plan changes.

Generate a clean Markdown report with the following three sections:

## Executive Summary
Provide a 2-3 sentence explanation of what this infrastructure change accomplishes in plain English.
For example: "This plan provisions public-facing web servers and locks down backend databases."
Focus on conveying the semantic intent of the change to a non-expert reviewer.

## Security & Architectural Audit
Highlight potential risks with a focus on:
- Defense-in-depth gaps
- IAM role privilege escalation or over-scoping
- Over-exposed S3 buckets or security groups (0.0.0.0/0 ingress, public ACLs)
- Missing encryption at rest (EBS, RDS, S3, DynamoDB)
- Lacking resource-level boundaries or blast radius containment
- Absent logging, monitoring, or audit trails

Do not just cite static rules. Evaluate the semantic intent of the resource layout and flag where the architecture diverges from security best practices. Reference resource addresses directly.

## Blast Radius Assessment
Classify the overall impact as LOW, MEDIUM, or HIGH:
- LOW: Additive changes only, no destructive actions, minimal scope.
- MEDIUM: Updates to existing resources, moderate scope, or changes to networking/IAM.
- HIGH: Destructive changes (delete/recreate), structural migrations, high-cost modifications, or broad permission changes.

Provide a brief justification for the classification.

Rules:
- Be concise, specific, and actionable.
- Use Markdown formatting: headers, bullet points, and code blocks where appropriate.
- If no security issues are found, explicitly state that the plan looks safe.
- Do NOT include any preamble, conversational text, or sign-off. Output ONLY the Markdown report.`

// buildUserMessage serializes the optimized plan into the user prompt.
func buildUserMessage(plan *parser.OptimizedPlan) (string, error) {
	payload, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("failed to serialize plan for LLM: %w", err)
	}
	return fmt.Sprintf("Analyze the following Terraform/OpenTofu plan changes:\n\n```json\n%s\n```", string(payload)), nil
}

// ---------------------------------------------------------------------------
// Anthropic (Claude) Messages API
// ---------------------------------------------------------------------------

const anthropicURL = "https://api.anthropic.com/v1/messages"

type anthropicProvider struct {
	apiKey string
	model  string
	client *http.Client
}

type anthropicReq struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system"`
	Messages  []anthropicMsg `json:"messages"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *anthropicProvider) Analyze(ctx context.Context, plan *parser.OptimizedPlan) (string, error) {
	userMsg, err := buildUserMessage(plan)
	if err != nil {
		return "", err
	}

	reqBody := anthropicReq{
		Model:     p.model,
		MaxTokens: 4096,
		System:    systemPrompt,
		Messages:  []anthropicMsg{{Role: "user", Content: userMsg}},
	}

	respBytes, err := p.doRequest(ctx, reqBody)
	if err != nil {
		return "", err
	}

	var resp anthropicResp
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "", fmt.Errorf("failed to decode Anthropic response: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("%w: [%s] %s", ErrAPIFailure, resp.Error.Type, resp.Error.Message)
	}
	if len(resp.Content) == 0 {
		return "", fmt.Errorf("%w: Anthropic returned empty content", ErrAPIFailure)
	}

	var sb strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), nil
}

func (p *anthropicProvider) doRequest(ctx context.Context, body interface{}) ([]byte, error) {
	return doPost(ctx, p.client, anthropicURL, map[string]string{
		"Content-Type":      "application/json",
		"x-api-key":         p.apiKey,
		"anthropic-version": "2023-06-01",
	}, body)
}

// ---------------------------------------------------------------------------
// OpenAI Chat Completions API
// ---------------------------------------------------------------------------

const openaiURL = "https://api.openai.com/v1/chat/completions"

type openaiProvider struct {
	apiKey string
	model  string
	client *http.Client
}

type openaiReq struct {
	Model    string      `json:"model"`
	Messages []openaiMsg `json:"messages"`
}

type openaiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (p *openaiProvider) Analyze(ctx context.Context, plan *parser.OptimizedPlan) (string, error) {
	userMsg, err := buildUserMessage(plan)
	if err != nil {
		return "", err
	}

	reqBody := openaiReq{
		Model: p.model,
		Messages: []openaiMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMsg},
		},
	}

	respBytes, err := p.doRequest(ctx, reqBody)
	if err != nil {
		return "", err
	}

	var resp openaiResp
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "", fmt.Errorf("failed to decode OpenAI response: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("%w: [%s] %s", ErrAPIFailure, resp.Error.Type, resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("%w: OpenAI returned no choices", ErrAPIFailure)
	}

	return resp.Choices[0].Message.Content, nil
}

func (p *openaiProvider) doRequest(ctx context.Context, body interface{}) ([]byte, error) {
	return doPost(ctx, p.client, openaiURL, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + p.apiKey,
	}, body)
}

// ---------------------------------------------------------------------------
// Shared HTTP helper with context support
// ---------------------------------------------------------------------------

func doPost(ctx context.Context, client *http.Client, url string, headers map[string]string, payload interface{}) ([]byte, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to build HTTP request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: the request exceeded the configured timeout", ErrAPITimeout)
		}
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w (HTTP %d): %s", ErrAPIFailure, resp.StatusCode, truncateBody(data, 500))
	}

	return data, nil
}

// truncateBody limits error response bodies to avoid flooding the terminal.
func truncateBody(data []byte, maxLen int) string {
	s := string(data)
	if len(s) > maxLen {
		return s[:maxLen] + "... (truncated)"
	}
	return s
}
