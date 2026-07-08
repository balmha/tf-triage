# tf-triage

Semantic architectural and security analysis of Terraform/OpenTofu plans, powered by LLMs.

`tf-triage` ingests `terraform plan -json` (or `tofu plan -json`) output, runs it through an LLM for architectural and security review, and produces a Markdown report ready to post as a Pull Request comment.

## Installation

### Homebrew (macOS)

```bash
brew tap balmha/tap
brew install tf-triage
```

### Linux / macOS (one-liner)

```bash
curl -sSfL https://raw.githubusercontent.com/balmha/tf-triage/main/install.sh | bash
```

Optionally pin a version or change the install directory:

```bash
VERSION=v0.1.0 INSTALL_DIR=~/.local/bin curl -sSfL https://raw.githubusercontent.com/balmha/tf-triage/main/install.sh | bash
```

### Go install

```bash
go install github.com/balmha/tf-triage@latest
```

### Build from source

```bash
git clone https://github.com/balmha/tf-triage.git
cd tf-triage
go build -o tf-triage .
```

## Usage

```bash
# Pipe from terraform (uses Anthropic by default)
terraform plan -json | tf-triage

# Use OpenAI with a specific model
tf-triage -f plan.json -p openai -m gpt-4o

# Write report to a file
tofu plan -json | tf-triage -o report.md
```

## Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--file` | `-f` | stdin | Path to terraform plan JSON file |
| `--provider` | `-p` | `anthropic` | LLM provider (`anthropic` or `openai`) |
| `--model` | `-m` | auto | Model override (defaults: `claude-3-5-sonnet` / `gpt-4o`) |
| `--output` | `-o` | stdout | Path to write the markdown report |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | API key for Anthropic (Claude) |
| `OPENAI_API_KEY` | API key for OpenAI |
| `TF_TRIAGE_MODEL` | Default model (overridden by `--model` flag) |

## How It Works

1. **Ingest** — Reads plan JSON from stdin or a file.
2. **Parse** — Validates schema (`format_version`, `resource_changes`) and extracts changes.
3. **Optimize** — Strips metadata, skips no-ops, computes diffs to minimize token usage.
4. **Analyze** — Sends the optimized payload to the LLM with a security-focused system prompt.
5. **Render** — Outputs formatted Markdown to stdout or a file.

## Report Sections

The LLM produces a structured report with:

- **Executive Summary** — Plain-English explanation of what the change accomplishes.
- **Security & Architectural Audit** — Defense-in-depth analysis, IAM risks, exposure gaps.
- **Blast Radius Assessment** — LOW / MEDIUM / HIGH classification with justification.

## Project Structure

```
tf-triage/
├── go.mod
├── main.go
├── cmd/
│   └── root.go         # Cobra command and flag definitions
├── pkg/
│   ├── parser/
│   │   └── parser.go   # Plan parsing and token optimization
│   └── llm/
│       └── client.go   # LLM client orchestration and prompt management
└── README.md
```

## License

MIT
