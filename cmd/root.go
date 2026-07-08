package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tf-triage/tf-triage/pkg/llm"
	"github.com/tf-triage/tf-triage/pkg/parser"
)

// ---------------------------------------------------------------------------
// Flag variables
// ---------------------------------------------------------------------------

var (
	flagFile     string
	flagProvider string
	flagModel    string
	flagOutput   string
)

// ---------------------------------------------------------------------------
// Root command
// ---------------------------------------------------------------------------

var rootCmd = &cobra.Command{
	Use:   "tf-triage",
	Short: "Semantic architectural and security analysis of Terraform/OpenTofu plans",
	Long: `tf-triage ingests a Terraform or OpenTofu plan (JSON format), performs
semantic architectural and security analysis using an LLM, and outputs
a clean Markdown report suitable for Git Pull Request comments.

Examples:
  terraform plan -json | tf-triage
  tf-triage -f plan.json -p groq
  tf-triage -f plan.json -p deepseek -m deepseek-v4-pro
  tf-triage -f plan.json -p openai -m gpt-4o
  tofu plan -json | tf-triage -o report.md`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          run,
}

func init() {
	rootCmd.Flags().StringVarP(&flagFile, "file", "f", "", "Path to terraform plan JSON file (defaults to stdin)")
	rootCmd.Flags().StringVarP(&flagProvider, "provider", "p", "ollama", "LLM provider: 'ollama', 'groq', 'deepseek', 'anthropic', or 'openai'")
	rootCmd.Flags().StringVarP(&flagModel, "model", "m", "", "LLM model override (defaults per provider: llama3.2 / llama-3.3-70b-versatile / deepseek-v4-flash / claude-3-5-sonnet / gpt-4o)")
	rootCmd.Flags().StringVarP(&flagOutput, "output", "o", "", "Path to write markdown report (defaults to stdout)")
}

// SetVersion configures the version string displayed by --version.
func SetVersion(version, commit string) {
	rootCmd.Version = fmt.Sprintf("%s (commit: %s)", version, commit)
}

// Execute is the main entry point called from main.go.
func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Core pipeline
// ---------------------------------------------------------------------------

func run(cmd *cobra.Command, args []string) error {
	provider := strings.ToLower(flagProvider)
	model := resolveModel(provider, flagModel)

	// Resolve API key with actionable error
	apiKey, err := resolveAPIKey(provider)
	if err != nil {
		return err
	}

	// Step 1: Ingest plan data
	planData, err := ingest(flagFile)
	if err != nil {
		return err
	}
	if len(planData) == 0 {
		return fmt.Errorf("no input received\n\n  Hint: pipe a plan or use --file:\n    terraform plan -json | tf-triage\n    tf-triage -f plan.json")
	}

	// Step 2: Parse and validate
	plan, err := parser.Parse(planData)
	if err != nil {
		return wrapParserError(err)
	}

	// Step 3: Token-optimize
	optimized := parser.Optimize(plan)

	// Step 4: Create provider and analyze
	llmProvider, err := llm.NewProvider(llm.Config{
		Provider: provider,
		Model:    model,
		APIKey:   apiKey,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize LLM provider: %w", err)
	}

	ctx := context.Background()
	markdown, err := llmProvider.Analyze(ctx, optimized)
	if err != nil {
		return wrapLLMError(err)
	}

	// Step 5: Render and output
	report := renderReport(markdown)
	return writeOutput(report, flagOutput)
}

// ---------------------------------------------------------------------------
// Ingestion
// ---------------------------------------------------------------------------

func ingest(path string) ([]byte, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("file not found: %s\n\n  Hint: check the path and try again", path)
			}
			if os.IsPermission(err) {
				return nil, fmt.Errorf("permission denied reading: %s", path)
			}
			return nil, fmt.Errorf("cannot read file %q: %w", path, err)
		}
		return data, nil
	}

	// Check if stdin is a pipe (not a terminal)
	info, _ := os.Stdin.Stat()
	if (info.Mode() & os.ModeCharDevice) != 0 {
		return nil, nil
	}

	return io.ReadAll(bufio.NewReader(os.Stdin))
}

// ---------------------------------------------------------------------------
// Configuration resolution
// ---------------------------------------------------------------------------

func resolveModel(provider, flagModel string) string {
	if flagModel != "" {
		return flagModel
	}
	if env := os.Getenv("TF_TRIAGE_MODEL"); env != "" {
		return env
	}
	switch provider {
	case "ollama":
		return "llama3.2"
	case "groq":
		return "llama-3.3-70b-versatile"
	case "deepseek":
		return "deepseek-v4-flash"
	case "anthropic":
		return "claude-3-5-sonnet-20241022"
	case "openai":
		return "gpt-4o"
	default:
		return ""
	}
}

func resolveAPIKey(provider string) (string, error) {
	switch provider {
	case "ollama":
		// Ollama runs locally, no API key required
		return "", nil

	case "groq":
		if key := os.Getenv("GROQ_API_KEY"); key != "" {
			return key, nil
		}
		return "", fmt.Errorf("GROQ_API_KEY environment variable is not set\n\n  Hint: export GROQ_API_KEY=gsk_... (free tier at https://console.groq.com)")

	case "deepseek":
		if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
			return key, nil
		}
		return "", fmt.Errorf("DEEPSEEK_API_KEY environment variable is not set\n\n  Hint: export DEEPSEEK_API_KEY=sk-... (get one at https://platform.deepseek.com)")

	case "anthropic":
		if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			return key, nil
		}
		return "", fmt.Errorf("ANTHROPIC_API_KEY is not set\n\n  Hint: export ANTHROPIC_API_KEY=sk-ant-...")

	case "openai":
		if key := os.Getenv("OPENAI_API_KEY"); key != "" {
			return key, nil
		}
		return "", fmt.Errorf("OPENAI_API_KEY is not set\n\n  Hint: export OPENAI_API_KEY=sk-...")

	default:
		return "", fmt.Errorf("unsupported provider %q\n\n  Supported: ollama, groq, deepseek, anthropic, openai", provider)
	}
}

// ---------------------------------------------------------------------------
// Output rendering
// ---------------------------------------------------------------------------

func renderReport(markdown string) string {
	var sb strings.Builder

	content := strings.TrimSpace(markdown)
	if !strings.HasPrefix(content, "#") {
		sb.WriteString("## tf-triage: Infrastructure Plan Analysis\n\n")
	}
	sb.WriteString(content)
	sb.WriteString("\n\n---\n*Generated by [tf-triage](https://github.com/tf-triage/tf-triage)*\n")

	return sb.String()
}

func writeOutput(report, path string) error {
	if path != "" {
		if err := os.WriteFile(path, []byte(report), 0644); err != nil {
			return fmt.Errorf("failed to write report to %q: %w", path, err)
		}
		fmt.Fprintf(os.Stderr, "Report written to %s\n", path)
		return nil
	}

	fmt.Print(report)
	return nil
}

// ---------------------------------------------------------------------------
// Error wrapping for actionable CLI messages
// ---------------------------------------------------------------------------

func wrapParserError(err error) error {
	switch {
	case errors.Is(err, parser.ErrStreamingFormat):
		return err

	case errors.Is(err, parser.ErrMalformedJSON):
		return fmt.Errorf("%w\n\n  Hint: ensure you're passing the output of 'terraform show -json' or 'terraform plan -json'", err)

	case errors.Is(err, parser.ErrNotTerraformPlan):
		return fmt.Errorf("%w\n\n  Hint: the input must be JSON output from 'terraform plan -json' or 'tofu plan -json'", err)

	case errors.Is(err, parser.ErrNoChanges):
		return fmt.Errorf("%w — nothing to analyze", err)

	default:
		return fmt.Errorf("plan parsing failed: %w", err)
	}
}

func wrapLLMError(err error) error {
	switch {
	case errors.Is(err, llm.ErrOllamaConnRefused):
		return err

	case errors.Is(err, llm.ErrAPITimeout):
		return fmt.Errorf("%w\n\n  Hint: the LLM took too long to respond; try again or use a faster model", err)

	case errors.Is(err, llm.ErrAPIFailure):
		return fmt.Errorf("LLM analysis failed: %w", err)

	default:
		return fmt.Errorf("analysis failed: %w", err)
	}
}
