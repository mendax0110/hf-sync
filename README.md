# hf-sync

Mirror git repositories to HuggingFace Hub as datasets, models, or spaces.

`hf-sync` pushes refs from a source git remote directly to a HuggingFace repository
using smart HTTP and Git LFS. No local checkout required — it clones into memory,
plans the sync, and pushes only what's needed.

## Why This Exists

You have repositories on GitHub/GitLab containing datasets, model weights, or code
that should also live on HuggingFace Hub. Doing this manually means:

- Cloning locally, adding an HF remote, pushing — tedious for many repos
- Writing shell scripts around `git push` — no planning, no safety checks
- Using HF's upload API — loses git history and branch structure

`hf-sync` fills that gap: one command to mirror a git repo to HuggingFace with
full history, branches, tags, and automatic repository creation.

## Quick Start

```bash
# Install
go install github.com/mendax0110/hf-sync@latest

# Sync a single repo
export HF_TOKEN="hf_your_token_here"
hf-sync sync https://github.com/org/dataset.git myuser/my-dataset --repo-type dataset

# Preview changes without pushing
hf-sync plan https://github.com/org/dataset.git myuser/my-dataset

# Batch sync from config file
hf-sync batch --config hf-sync.yaml
```

## Commands

| Command   | Description                                      |
|-----------|--------------------------------------------------|
| `sync`    | Mirror source refs to HuggingFace target         |
| `plan`    | Preview sync actions without pushing             |
| `batch`   | Sync all repos defined in config file            |
| `init`    | Create a new HuggingFace repository              |
| `version` | Print version                                    |

## Authentication

hf-sync needs two tokens:

| Token | Flag | Env Var | Purpose |
|-------|------|---------|---------|
| HuggingFace | `--hf-token` | `HF_TOKEN` | Push to HF Hub, create repos |
| Source | `--source-token` | `GITSYNC_SOURCE_TOKEN` | Pull from private source repos |

Get your HuggingFace token at https://huggingface.co/settings/tokens

## Config File

For syncing multiple repositories, define them in `hf-sync.yaml`:

```yaml
defaults:
  repo_type: dataset
  private: false
  tags: true

repos:
  - source: https://github.com/myorg/dataset-a.git
    target: myuser/dataset-a

  - source: https://github.com/myorg/model-b.git
    target: myuser/model-b
    repo_type: model
    private: true
    branches:
      - main
      - release
```

Then run:
```bash
hf-sync batch --config hf-sync.yaml
```

## Sync Behavior

- **Auto-create**: Target HF repos are created automatically if they don't exist
- **Incremental**: Only pushes refs that have changed
- **Plan first**: All actions are computed before any mutations
- **LFS-aware**: Detects LFS issues and provides guidance
- **Tags**: Synced by default (disable with `--tags=false`)
- **Prune**: Optionally delete refs on target not present on source (`--prune`)
- **Force**: Allow non-fast-forward updates (`--force`)

## JSON Output

All commands support `--json` for machine-readable output:

```bash
hf-sync sync --json https://github.com/org/repo.git user/repo | jq .
```

```json
{
  "source": "https://github.com/org/repo.git",
  "target": "https://huggingface.co/datasets/user/repo",
  "actions": [
    {"ref": "refs/heads/main", "type": "update", "reason": "ref updated on source"}
  ],
  "created": 0,
  "updated": 1,
  "deleted": 0,
  "skipped": 0,
  "dry_run": false
}
```

## Installation

### From Source

```bash
git clone https://github.com/mendax0110/hf-sync.git
cd hf-sync
make build
```

### go install

```bash
go install github.com/mendax0110/hf-sync@latest
```

### Pre-built Binaries

Download from [Releases](https://github.com/mendax0110/hf-sync/releases).
Binaries available for Linux, macOS, and Windows (amd64 + arm64).

## Development

```bash
# Run tests
make test

# Run tests with coverage
make test-cover

# Lint
make lint

# Build
make build

# Dry-run release
make release-dry
```

## Release Process

1. Tag a version: `git tag v0.1.0`
2. Push the tag: `git push origin v0.1.0`
3. GitHub Actions runs goreleaser → binaries published to Releases

## Architecture

See [docs/architecture.md](docs/architecture.md) for detailed design documentation.

## License

MIT — see [LICENSE](LICENSE).
