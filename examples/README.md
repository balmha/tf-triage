# CI/CD Examples

Ready-to-use pipeline configurations for running `tf-triage` in your CI/CD environment.

## Available Examples

| File | Platform | Provider | Description |
|------|----------|----------|-------------|
| [github-actions.yml](github-actions.yml) | GitHub Actions | DeepSeek (cloud) | Standard workflow with cloud LLM |
| [github-actions-ollama.yml](github-actions-ollama.yml) | GitHub Actions | Ollama (local) | Fully self-contained, no API keys |
| [gitlab-ci.yml](gitlab-ci.yml) | GitLab CI | DeepSeek (cloud) | Merge request pipeline |
| [bitbucket-pipelines.yml](bitbucket-pipelines.yml) | Bitbucket Pipelines | DeepSeek (cloud) | Pull request pipeline |

## Quick Start

1. Copy the relevant example file into your repo's CI config location:
   - GitHub: `.github/workflows/tf-triage.yml`
   - GitLab: Include in your `.gitlab-ci.yml`
   - Bitbucket: Include in your `bitbucket-pipelines.yml`

2. Set the required secrets/variables:
   - **LLM provider key** (skip if using Ollama): `DEEPSEEK_API_KEY`, `GROQ_API_KEY`, `GEMINI_API_KEY`, etc.
   - **Platform token** for posting comments:
     - GitHub: `GITHUB_TOKEN` (automatic in Actions)
     - GitLab: `GITLAB_TOKEN` (create a Project Access Token with `api` scope)
     - Bitbucket: `BITBUCKET_TOKEN` (create an App Password with PR write access)

3. Update the `working-directory` / `TF_DIR` to match your Terraform directory.

## The Pattern

All examples follow the same three-step pattern:

```bash
# 1. Generate the plan
terraform plan -json > plan.json

# 2. Analyze with tf-triage
tf-triage --file plan.json --provider deepseek

# 3. Post as PR comment
tf-triage comment
```

## Choosing a Provider

| Need | Recommended Provider |
|------|---------------------|
| No API keys, fully private | `ollama` |
| Cost-efficient cloud | `deepseek` |
| Fast + free tier | `groq` |
| Google ecosystem | `gemini` |
| Best analysis quality | `anthropic` |

## Notes

- The `tf-triage comment` step is idempotent — it updates existing comments instead of creating duplicates.
- For the Ollama example, ensure your runner has at least 4GB RAM available for the model.
- All examples trigger only on pull requests that modify `.tf` or `.tfvars` files.
