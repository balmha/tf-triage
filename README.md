<div align="center">

# tf-triage

**AI-powered security and architecture reviews for your Terraform plans.**

[![Release](https://img.shields.io/github/v/release/balmha/tf-triage?style=flat-square)](https://github.com/balmha/tf-triage/releases)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/go-%3E%3D1.21-00ADD8?style=flat-square&logo=go)](https://go.dev)

<img alt="tf-triage demo" src="./triage.gif" width="700" />

`tf-triage` analyzes your Terraform and OpenTofu plans using LLMs and generates
a security audit with blast radius assessment — all from a single command.

[Installation](#installation) •
[Providers](#providers) •
[Usage](#usage) •
[CI/CD Examples](./examples/)

</div>

---

To get started, [install](#installation) `tf-triage` and choose a [provider](#providers).
Run it locally with [Ollama](#using-ollama-local-private-free) (no API keys, fully private)
or use a [cloud provider](#using-cloud-providers) like DeepSeek, Groq, or Gemini for faster inference.
Pipe your plan and get a Markdown report ready to drop into a PR comment:

```
terraform plan -json | tf-triage
```

---

## Providers

tf-triage supports **6 LLM providers** out of the box. Pick the one that fits your workflow — from fully local and free to cloud-hosted powerhouses.

| Provider | Default Model | API Key Required | Notes |
|----------|--------------|:---:|-------|
| **ollama** | `llama3.2` | No | Local execution, no internet needed, completely free |
| **groq** | `llama-3.3-70b-versatile` | Yes | Cloud, generous free tier, very fast inference |
| **deepseek** | `deepseek-v4-flash` | Yes | Cloud, cost-efficient, strong reasoning |
| **gemini** | `gemini-1.5-flash` | Yes | Cloud, Google AI, free tier available |
| **anthropic** | `claude-3-5-sonnet` | Yes | Cloud, excellent analysis quality |
| **openai** | `gpt-4o` | Yes | Cloud, widely used, strong general performance |

### Using Ollama (local, private, free)

Ollama lets you run `tf-triage` entirely on your machine with zero external API calls. Your infrastructure plans never leave your laptop.

1. Install Ollama: https://ollama.com/download

2. Pull a model:
   ```bash
   ollama pull llama3.2
   ```

3. Run tf-triage (Ollama is the default provider):
   ```bash
   terraform plan -json | tf-triage
   ```

That's it. No API keys, no accounts, no network dependency. Great for air-gapped environments, sensitive infrastructure, or just keeping things simple.

You can use any Ollama model:
```bash
tf-triage -f plan.json -m mistral
tf-triage -f plan.json -m llama3.1:70b
tf-triage -f plan.json -m deepseek-r1
```

### Using cloud providers

For cloud providers, export the API key and pass `--provider`:

```bash
# Groq (free tier, fast)
export GROQ_API_KEY=gsk_...
terraform plan -json | tf-triage -p groq

# DeepSeek
export DEEPSEEK_API_KEY=sk-...
tf-triage -f plan.json -p deepseek

# Google Gemini
export GEMINI_API_KEY=...
tf-triage -f plan.json -p gemini -m gemini-1.5-pro

# Anthropic (Claude)
export ANTHROPIC_API_KEY=sk-ant-...
tf-triage -f plan.json -p anthropic

# OpenAI
export OPENAI_API_KEY=sk-...
tf-triage -f plan.json -p openai -m gpt-4o
```

---

## Installation

### Homebrew (macOS)

```bash
brew trust --formula balmha/tap/tf-triage
brew install balmha/tap/tf-triage
```

### Linux / macOS (one-liner)

```bash
curl -sSfL https://raw.githubusercontent.com/balmha/tf-triage/main/install.sh | bash
```

Optionally pin a version or change the install directory:

```bash
VERSION=v0.7.0 INSTALL_DIR=~/.local/bin curl -sSfL https://raw.githubusercontent.com/balmha/tf-triage/main/install.sh | bash
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

---

## Usage

tf-triage accepts input in two ways:

```bash
# Pipe directly from terraform plan
terraform plan -json | tf-triage

# Or point to a saved plan file
terraform plan -out=plan.tfplan
terraform show -json plan.tfplan > plan.json
tf-triage -f plan.json
```

### More examples

```bash
# Write report to a file instead of stdout
terraform plan -json | tf-triage -o report.md

# Use Groq for fast cloud inference
tf-triage -f plan.json -p groq

# Override the model
tf-triage -f plan.json -p gemini -m gemini-1.5-pro

# OpenTofu works the same way
tofu plan -json | tf-triage -p deepseek
```

---

## Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--file` | `-f` | stdin | Path to terraform plan JSON file |
| `--provider` | `-p` | `ollama` | LLM provider (`ollama`, `groq`, `deepseek`, `gemini`, `anthropic`, `openai`) |
| `--model` | `-m` | auto | Model name (auto-selects per provider if omitted) |
| `--output` | `-o` | stdout | Path to write the markdown report |
| `--version` | `-v` | | Print version and exit |

## Environment Variables

| Variable | Provider | Description |
|----------|----------|-------------|
| `GROQ_API_KEY` | groq | API key ([console.groq.com](https://console.groq.com)) |
| `DEEPSEEK_API_KEY` | deepseek | API key ([platform.deepseek.com](https://platform.deepseek.com)) |
| `GEMINI_API_KEY` | gemini | API key ([aistudio.google.com](https://aistudio.google.com/apikey)) |
| `ANTHROPIC_API_KEY` | anthropic | API key ([console.anthropic.com](https://console.anthropic.com)) |
| `OPENAI_API_KEY` | openai | API key ([platform.openai.com](https://platform.openai.com)) |
| `TF_TRIAGE_MODEL` | any | Default model override (lower priority than `--model` flag) |

---

## How It Works

1. **Ingest** — Reads plan JSON from stdin or a file. Supports both `terraform show -json` (full schema) and `terraform plan -json` (streaming format).
2. **Parse** — Validates the schema and extracts resource changes.
3. **Optimize** — Strips metadata, skips no-ops, computes attribute diffs to minimize token usage.
4. **Analyze** — Sends the optimized payload to the LLM with a security-focused system prompt.
5. **Render** — Outputs formatted Markdown to stdout or a file.

## Report Sections

The LLM produces a structured report with three sections:

- **Executive Summary** — 2-3 sentence plain-English explanation of what the change accomplishes.
- **Security & Architectural Audit** — Defense-in-depth analysis: IAM risks, public exposure, missing encryption, logging gaps.
- **Blast Radius Assessment** — LOW / MEDIUM / HIGH classification with justification.

---

## License

GPL-3.0
