# Architecture

`hf-sync` is a CLI tool and Go library for mirroring git repositories to HuggingFace Hub.

## Design Principles

1. **Plan before execute** — Every sync computes a complete action plan before any mutations.
   This enables dry-run mode and ensures predictable behavior.

2. **Native git for heavy lifting** — Uses the system `git` binary + `git-lfs` for
   clone, LFS migration, and push. go-git is used only for lightweight ref probing
   (ls-remote). This gives us full LFS support and stable performance on large repos.

3. **HuggingFace-native** — Understands HF repo types (model, dataset, space),
   auto-creates repositories, handles token auth, auto-migrates large files to LFS,
   and manages default branch settings via the HF API.

4. **Automation-friendly** — JSON output, exit codes, config files for batch operations.

## Package Layout

```
hf-sync/
├── main.go                     # Entry point
├── cmd/                        # CLI commands (cobra)
│   ├── root.go                 # Root command, global flags, config init
│   ├── sync.go                 # sync command
│   ├── plan.go                 # plan command (dry-run)
│   ├── batch.go                # batch sync from config
│   ├── init.go                 # create HF repo
│   └── version.go              # version command
├── internal/
│   ├── engine/                 # Core sync engine
│   │   ├── engine.go           # Probe → Plan → Execute pipeline
│   │   └── engine_test.go      # Unit tests for planning logic
│   └── hfapi/                  # HuggingFace Hub API client
│       ├── client.go           # HTTP client, repo CRUD, URL construction
│       └── client_test.go      # Tests with httptest servers
├── docs/
│   └── architecture.md         # This file
├── .github/workflows/
│   ├── ci.yaml                 # Test + lint on PR/push
│   └── release.yaml            # goreleaser on tag push
├── .goreleaser.yaml            # Cross-platform release config
├── .golangci.yaml              # Linter config
├── Makefile                    # Dev commands
├── hf-sync.example.yaml        # Example config file
└── go.mod
```

## Sync Pipeline

The sync engine follows a three-stage pipeline:

```
┌─────────┐     ┌──────────┐     ┌─────────┐
│  PROBE  │ ──→ │   PLAN   │ ──→ │ EXECUTE │
└─────────┘     └──────────┘     └─────────┘
```

### Stage 1: Probe

- List refs on the source remote (branches + tags)
- List refs on the target HuggingFace remote
- Both use lightweight `go-git` ls-remote (in-memory, no object transfer)
- Runs concurrently for source and target

### Stage 2: Plan

- Compare source refs against target refs
- Compute actions: create, update, delete, skip
- Apply filters: branch list, tag toggle, force policy, prune policy
- Handle freshly-created HF repos: schedule deletion of auto-init refs
- Output: ordered list of `RefAction` structs

### Stage 3: Execute

- Ensure target HF repository exists (create if needed via HF API)
- `git clone --mirror` source into a temp bare repo (native git)
- `git lfs fetch --all` existing LFS objects from source
- `git lfs migrate import --everything --above=10mb` to convert large files to LFS
- Push create/update refspecs first
- If deleting the current default branch: call HF API to switch default
- Push delete refspecs
- `git lfs push --all` to upload LFS objects to target

## HuggingFace Integration

HuggingFace Hub repositories are standard git repos accessed over HTTPS:
- Models: `https://huggingface.co/{user}/{repo}`
- Datasets: `https://huggingface.co/datasets/{user}/{repo}`
- Spaces: `https://huggingface.co/spaces/{user}/{repo}`

Authentication uses Bearer tokens in HTTP Basic auth (username is ignored).
Large files (>10MB) must use Git LFS — HuggingFace enforces this server-side.

## Resource Model

- Ref probing uses in-memory go-git (no disk I/O, minimal memory)
- Clone + push uses a temporary bare repo on disk (cleaned up after each sync)
- Disk usage scales with repository size; memory stays low
- LFS migration rewrites history locally — source repo is never modified
- For very large repos, consider syncing specific branches only to limit clone size

## Why Not Fork git-sync?

`entireio/git-sync` is an excellent tool for generic remote-to-remote mirroring.
We chose to build `hf-sync` as a separate focused tool because:

1. **Different value prop** — git-sync is provider-agnostic; we are HuggingFace-native
2. **LFS migration** — We auto-rewrite large files (>10 MiB) to LFS pointers before
   pushing. git-sync has no equivalent; HF would reject those pushes outright.
3. **HF API integration** — Auto repo creation, dataset/space types, default branch
   management via API — these are HF-specific concerns that don't belong upstream.
4. **Native git + git-lfs** — We shell out to git for clone/push (go-git has no LFS
   support). git-sync uses go-git end-to-end, which won't work for HF's LFS requirements.
5. **Maintenance** — Smaller, focused codebase is easier to maintain and extend

We share the same philosophy: plan-first, typed output, automation-friendly.
go-git is used only for lightweight ref probing; all heavy git operations use the
native git binary.
