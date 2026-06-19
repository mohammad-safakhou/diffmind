# Configuring a DiffMind Project

A step-by-step helper for setting up a project after you've created it.
DiffMind builds a cross-service dependency graph from your repos. To do that it
needs to know **three things**: where to find an LLM (OpenCode), which repos
belong to the project, and which blueprints describe how to extract identity
from those repos.

This guide walks through each one. Examples use the project you already
created (`default` / `DEFAULT`).

---

## 1. Where everything lives

DiffMind keeps all its data under one home directory — **not** in your code
repos. The default is `~/.diffmind/` (override with the `DIFFMIND_HOME` env var).

```
~/.diffmind/
  config.json                         # global defaults (optional)
  projects/<project_id>/
    project.json                      # the project itself
    blueprints/<blueprint_id>.json    # extraction blueprints
    repos/<repo_id>/repo.json         # one folder per attached repo
    runs/<run_id>/                    # graph-build output (manifest, graph, identities)
```

Your project right now:

```
~/.diffmind/projects/default/
  project.json          -> { "id": "default", "name": "DEFAULT" }   # bare
  repos/example/repo.json
  blueprints/           # empty
  runs/                 # empty
```

> Most of this is meant to be edited through the **web UI** (`diffmind` with no
> args launches it at http://127.0.0.1:8090). Editing the JSON files by hand
> works too — the UI just reads and writes these same files. The CLI is
> read-only: `diffmind list projects` and `diffmind list runs --project default`.

---

## 2. Configuration layers and precedence

Three layers, each overriding the one above it:

| Layer   | File                                  | Sets                                    |
|---------|---------------------------------------|-----------------------------------------|
| Global  | `~/.diffmind/config.json`              | `opencode`, `search_roots`              |
| Project | `projects/<id>/project.json`          | `opencode`, `search_roots`, `instruction` |
| Repo    | `projects/<id>/repos/<id>/repo.json`  | `blueprint_ids`, `instruction`          |

Rules used at run time:

- **OpenCode**: project settings are merged *over* the global ones (only the
  fields you set in the project override global; the rest fall through).
- **Instruction**: the repo's `instruction` wins; if empty, the project's is used.
- **Blueprints**: if a repo lists explicit `blueprint_ids`, only those apply.
  Otherwise *all* project blueprints are candidates and each one's `applies_to`
  rule decides whether it matches the repo.

---

## 3. Step 1 — Connect an LLM (OpenCode)

DiffMind uses an OpenCode LLM server to read config/Helm files and extract each
service's identity. Set this once globally so every project inherits it.

Create `~/.diffmind/config.json`:

```json
{
  "opencode": {
    "base_url": "http://localhost:4096",
    "provider_id": "openai",
    "model_id": "gpt-5.4-mini",
    "variant": "medium",
    "timeout": 600
  }
}
```

Field reference:

| Field         | Meaning                                  | Default                  |
|---------------|------------------------------------------|--------------------------|
| `base_url`    | OpenCode server URL                      | `http://localhost:3000`  |
| `provider_id` | LLM provider id (e.g. `openai`)          | —                        |
| `model_id`    | Model name                               | —                        |
| `variant`     | Reasoning/effort variant                 | `medium`                 |
| `timeout`     | Per-request timeout, seconds             | `120`                    |
| `username` / `password` | Basic-auth creds, if needed    | —                        |

**Credentials:** prefer environment variables over putting them in JSON:

```bash
export OPENCODE_SERVER_USERNAME=...
export OPENCODE_SERVER_PASSWORD=...
```

You can override any of these per-project by adding an `opencode` block to
`project.json` — useful if one project needs a bigger model.

---

## 4. Step 2 — Attach your repos

Each repo is a folder under `projects/default/repos/<repo_id>/repo.json`.
Yours currently looks like:

```json
{
  "id": "example",
  "name": "example",
  "path": "/Users/developer/repos/example",
  "kind": "service_repo"
}
```

| Field           | Meaning                                                        |
|-----------------|---------------------------------------------------------------|
| `path`          | Absolute path to the repo on disk (DiffMind never modifies it) |
| `kind`          | `service_repo` or `infra_repo`                                |
| `blueprint_ids` | *(optional)* restrict which blueprints apply to this repo     |
| `instruction`   | *(optional)* repo-specific extraction instruction             |

Tip: one DiffMind repo entry should point at **one service repo**, not a parent
folder containing many. Your `example` entry points at `~/repos/example`, which
looks like a directory of many services — add one repo entry per service
instead (e.g. `checkout-service`, `gateway-service`, …), each with
its own `path`. The easiest way is the **Repos** panel in the web UI.

---

## 5. Step 3 — Add blueprints

A **blueprint** tells DiffMind *what to extract* from a repo and *how*. Without
at least one matching blueprint, a run produces an empty graph.

Blueprints live in `projects/default/blueprints/<id>.json`. There are ready-made
examples in the repo's [`blueprints/`](../blueprints/) folder you can copy:

- `helm-values-identity.json` — extract identity from `.example` Helm values
- `example-helm-fieldpath.json`
- `terraform-resources.json`

Anatomy of a blueprint (`helm-values-identity.json`):

```json
{
  "name": "helm-values-identity",
  "description": "Extract service identity from production Helm values",
  "version": "v1",
  "applies_to": {
    "kind": "service_repo",
    "match": { "has_path": ".example" }
  },
  "extractions": [
    {
      "name": "production_identity",
      "source": { "glob": ".example/config/production/values.yaml" },
      "strategy": "llm",
      "prompt_hint": "From this Helm values YAML, extract service identity as JSON ...",
      "extract": [
        { "maps_to": "iam_role" },
        { "maps_to": "dns_aliases" },
        { "maps_to": "queue_identifiers" },
        { "maps_to": "database_connection" }
      ]
    }
  ]
}
```

| Field         | Meaning                                                          |
|---------------|------------------------------------------------------------------|
| `applies_to`  | Which repos this blueprint runs against (`kind` + `match` rules) |
| `match.has_path` | Only match repos that contain this path                       |
| `extractions` | One or more things to pull out                                   |
| `source.glob` | File(s) to read                                                  |
| `strategy`    | `llm` (the LLM reads the file using `prompt_hint`)               |
| `extract[].maps_to` | The identity field each result populates                   |

To install one: copy it into your project's blueprint folder, e.g.

```bash
cp blueprints/helm-values-identity.json \
   ~/.diffmind/projects/default/blueprints/helm-values-identity.json
```

(or paste it in the UI's blueprint editor). Adjust `match.has_path` and
`source.glob` to fit how *your* services store config.

---

## 6. Step 4 — Build the graph (a run)

Once OpenCode + repos + blueprints are set, start a run from the UI. A run:

1. Reads each repo with its matching blueprints (optionally pairing with a
   **DiffMind** artifact run — see below).
2. Extracts each service's identity (IAM role, DNS, queues, DBs, service URLs).
3. Cross-links them into a dependency graph.

Output lands in `projects/default/runs/<run_id>/`:

```
manifest.json      # run status, repos used, service/edge counts
graph.json         # the dependency graph
identities/        # <service>.json per service
events.jsonl       # progress log
```

Check results from the CLI any time:

```bash
diffmind list runs --project default
```

---

## 7. Optional — DiffMind artifacts

DiffMind can enrich extraction with **DiffMind** runs (a sibling tool that
indexes a repo's code). At run time you bind each repo to a specific DiffMind
run id; DiffMind finds those under `~/.diffmind/runs/` (override with
`DIFFMIND_HOME`). If you're not using DiffMind yet, skip this — blueprints alone
produce a graph.

```json
"diffmind": {
  "binary_path": "diffmind"
}
```

---

## Quick checklist

- [ ] `~/.diffmind/config.json` has a working `opencode` block
- [ ] OpenCode server reachable at `base_url`; creds via env if needed
- [ ] One repo entry **per service**, correct absolute `path` and `kind`
- [ ] At least one blueprint in the project whose `applies_to` matches your repos
- [ ] `blueprint.match.has_path` / `source.glob` match your repos' real layout
- [ ] Start a run from the UI, then `diffmind list runs --project default`

---

## Troubleshooting

| Symptom                         | Likely cause                                              |
|---------------------------------|-----------------------------------------------------------|
| Run fails: "no valid repos"     | No repos attached, or their `path` doesn't exist          |
| Empty graph / no identities     | No blueprint matches (`applies_to.match` / `kind` wrong)  |
| LLM timeouts or connection errors | `opencode.base_url` wrong or server down; raise `timeout` |
| Blueprint never fires           | `match.has_path` not present in the repo, or `kind` mismatch |
