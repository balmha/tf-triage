# CI/CD Examples

Ready-to-use pipeline configurations for running `tf-triage` in your CI/CD environment.

## Available Examples

| File | Platform | Provider | Description |
|------|----------|----------|-------------|
| [github-actions.yml](github-actions.yml) | GitHub Actions | Groq (cloud) | Standard workflow with cloud LLM |
| [github-actions-ollama.yml](github-actions-ollama.yml) | GitHub Actions | Ollama (local) | Fully self-contained, no API keys |
| [gitlab-ci.yml](gitlab-ci.yml) | GitLab CI | Groq (cloud) | Merge request pipeline |
| [bitbucket-pipelines.yml](bitbucket-pipelines.yml) | Bitbucket Pipelines | Groq (cloud) | Pull request pipeline |

## Quick Start

1. Copy the relevant example file into your repo's CI config location:
   - GitHub: `.github/workflows/tf-triage.yml`
   - GitLab: Include in your `.gitlab-ci.yml`
   - Bitbucket: Include in your `bitbucket-pipelines.yml`

2. Set the required secrets/variables:
   - **LLM provider key** (skip if using Ollama): `GROQ_API_KEY`, `DEEPSEEK_API_KEY`, `GEMINI_API_KEY`, etc.
   - **Platform token** for posting comments:
     - GitHub: `GITHUB_TOKEN` (automatic in Actions)
     - GitLab: `GITLAB_TOKEN` (create a Project Access Token with `api` scope)
     - Bitbucket: `BITBUCKET_TOKEN` (create an App Password with PR write access)

3. Update the `working-directory` / `TF_DIR` to match your Terraform directory.

## Choosing a Provider

| Need | Recommended Provider |
|------|---------------------|
| No API keys, fully private | `ollama` |
| Fast + free tier | `groq` |
| Cost-efficient cloud | `deepseek` |
| Google ecosystem | `gemini` |
| Best analysis quality | `anthropic` |

## Notes

- All examples use `terraform show -json` (the full plan schema) for maximum detail. You can also pipe `terraform plan -json` directly, but it provides less attribute-level context to the LLM.
- The `tf-triage comment` step is idempotent — it updates existing comments instead of creating duplicates.
- For the Ollama example, ensure your runner has at least 4GB RAM available for the model.
