package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tf-triage/tf-triage/pkg/llm"
	"github.com/tf-triage/tf-triage/pkg/parser"
)

// ---------------------------------------------------------------------------
// Redteam flags
// ---------------------------------------------------------------------------

var (
	redteamFile     string
	redteamProvider string
	redteamModel    string
	redteamOutput   string
)

// ---------------------------------------------------------------------------
// Redteam system prompt — offensive security persona
// ---------------------------------------------------------------------------

const redteamSystemPrompt = `You are an elite Offensive Security Engineer and Red Team Operator specializing in cloud infrastructure exploitation.

Your mission: Analyze the provided Terraform/OpenTofu plan and identify exploitable multi-resource attack chains that a malicious actor could leverage BEFORE this infrastructure is deployed.

Think like an attacker. Do not just list findings — connect them into actionable exploitation paths.

Generate a Markdown report with the following exact structure:

## 🚨 Exploitation Summary

Assign a critical risk score: LOW, MEDIUM, HIGH, or CRITICAL.
Provide a 2-3 sentence executive explanation of the overall threat landscape. Mention the most dangerous combination of misconfigurations and what an attacker could achieve (data exfiltration, lateral movement, privilege escalation, etc.).

## ⛓️ The Vulnerability Chain

Present a chronological, step-by-step attack narrative showing how an adversary could combine multiple configuration weaknesses to compromise the environment. Each step must:
- Reference specific resource addresses from the plan
- Explain what the attacker does at that stage
- Explain what access or capability they gain
- Show how it enables the next step in the chain

Format as a numbered list. Be specific about ports, CIDR ranges, IAM actions, bucket names, and resource relationships.

## 💻 Proof of Concept (PoC)

Provide actionable verification commands that simulate the initial attack vector. These should be safe reconnaissance or validation commands (using curl, aws cli, nmap, etc.) that a security team could run to verify the exposure. Format as code blocks with shell syntax.

Rules:
- Be aggressive and thorough. Assume the attacker is skilled and patient.
- Connect findings across resources — isolated findings are less valuable than chains.
- Reference resource addresses directly (e.g., aws_security_group.web, aws_iam_role.app_role).
- If the plan is genuinely secure, state that explicitly with justification.
- Do NOT include preamble, conversational text, or sign-off. Output ONLY the Markdown report.
- PoC commands must be safe for verification (no destructive operations).`

// ---------------------------------------------------------------------------
// Redteam command
// ---------------------------------------------------------------------------

var redteamCmd = &cobra.Command{
	Use:   "redteam",
	Short: "Offensive security threat modeling of Terraform/OpenTofu plans",
	Long: `Shifts tf-triage into Red Team mode — analyzing your infrastructure plan
for exploitable multi-resource attack chains, privilege escalation paths,
and lateral movement opportunities.

The output is a structured threat report with exploitation narratives and
proof-of-concept verification commands.

Examples:
  terraform plan -json | tf-triage redteam
  tf-triage redteam -f plan.json -p deepseek
  tf-triage redteam -f plan.json -p ollama -o threat-report.md`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runRedteam,
}

func init() {
	redteamCmd.Flags().StringVarP(&redteamFile, "file", "f", "", "Path to terraform plan JSON file (defaults to stdin)")
	redteamCmd.Flags().StringVarP(&redteamProvider, "provider", "p", "ollama", "LLM provider: 'ollama', 'groq', 'deepseek', 'gemini', 'anthropic', or 'openai'")
	redteamCmd.Flags().StringVarP(&redteamModel, "model", "m", "", "LLM model override")
	redteamCmd.Flags().StringVarP(&redteamOutput, "output", "o", "tf-redteam-report.md", "File path to save the threat report")
}

// ---------------------------------------------------------------------------
// Core execution
// ---------------------------------------------------------------------------

func runRedteam(cmd *cobra.Command, args []string) error {
	provider := strings.ToLower(redteamProvider)
	model := resolveModel(provider, redteamModel)

	apiKey, err := resolveAPIKey(provider)
	if err != nil {
		return err
	}

	// Step 1: Ingest plan data (reuse existing ingestion logic)
	planData, err := ingest(redteamFile)
	if err != nil {
		return err
	}
	if len(planData) == 0 {
		return fmt.Errorf("no input received\n\n  Hint: pipe a plan or use --file:\n    terraform plan -json | tf-triage redteam\n    tf-triage redteam -f plan.json")
	}

	// Step 2: Parse and validate (reuse existing parser)
	plan, err := parser.Parse(planData)
	if err != nil {
		return wrapParserError(err)
	}

	// Step 3: Token-optimize (reuse existing optimizer)
	optimized := parser.Optimize(plan)

	// Step 4: Create provider with offensive security prompt override
	llmProvider, err := llm.NewProvider(llm.Config{
		Provider:     provider,
		Model:        model,
		APIKey:       apiKey,
		SystemPrompt: redteamSystemPrompt,
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
	report := renderRedteamReport(markdown)
	return writeOutput(report, redteamOutput)
}

func renderRedteamReport(markdown string) string {
	var sb strings.Builder

	content := strings.TrimSpace(markdown)
	if !strings.HasPrefix(content, "#") {
		sb.WriteString("## tf-triage redteam: Offensive Security Threat Report\n\n")
	}
	sb.WriteString(content)
	sb.WriteString("\n\n---\n*Generated by [tf-triage redteam](https://github.com/balmha/tf-triage) — Automated Red Team Analysis*\n")

	return sb.String()
}
