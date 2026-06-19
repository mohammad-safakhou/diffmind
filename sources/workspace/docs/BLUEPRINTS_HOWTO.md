# Working with Blueprints

This explains what a blueprint does and how it helps DiffMind extract service
identities, runtime resources, and dependency targets for its resolver graph.

---

## First: what graph does this help?

DiffMind currently has two graph-shaped views with different jobs:

| Graph | Built from | Current role |
|-------|------------|--------------|
| Raw architecture graph | DiffMind exposures / dependencies / connections | Drill-down evidence and per-repo detail |
| Resolver graph (`graph.json`) | service identities + dependency targets | Cross-repo matching of services and concrete resources |

The raw DiffMind architecture graph can contain rich details. It may say "this
service has database operations" without knowing which production database
instance those operations hit. The resolver graph needs concrete identities and
resource identifiers to connect those findings across repositories.

Blueprints help extract those identities and resource identifiers from config
and infrastructure files.

---

## What a blueprint actually does

A blueprint is a recipe that reads files in a repo and produces resolver facts.
Three kinds of facts matter most:

- **aliases**: how other services address this service, such as DNS names, IAM
  roles, hostnames, Kubernetes service names, and ingress hosts.
- **resource instances**: concrete databases, caches, queues, topics, and
  streams with instance identifiers such as host, database name, queue ARN,
  account, and region.
- **dependency targets**: configured outbound URLs, queue names, DB hosts,
  cache hosts, and other target values that a service uses.

The resolver then matches dependency targets to known service aliases and
runtime resource instances ([resolver.go](../internal/resolver/resolver.go)).

The current MVP mostly uses string matching. The target design is stricter:
exact ARN/resource ID, host/FQDN, normalized service name, and only then fuzzy
containment as a last resort.

So the whole game is: **extract the aliases and concrete resource identifiers
that other services actually reference in config, infrastructure, and code.**

### Worked example

`ranking-service` calls `https://gateway-service.example.global/...`.
For that edge to appear, `gateway-service` must have an identity alias whose
value is `gateway-service.example.global` (or `gateway-service`). A
blueprint that reads its Helm ingress and emits that alias makes the edge light
up. Without it, the dependency lands in "unresolved".

---

## Anatomy of a blueprint

```jsonc
{
  "name": "helm-values-identity",
  "description": "Service identity from production Helm values",
  "version": "v1",

  // 1) WHICH repos this applies to.
  "applies_to": {
    "kind": "service_repo",            // service_repo | infra_repo
    "match": { "has_path": ".example" } // only repos that contain this path
  },

  // 2) WHAT to extract, and HOW.
  "extractions": [
    {
      "name": "production_identity",
      "source": { "glob": ".example/config/production/values.yaml" }, // file(s) to read
      "strategy": "llm",               // the LLM reads the file using the hint
      "prompt_hint": "From this Helm YAML, return JSON {\"iam_role\":\"...\",\"dns_aliases\":[\"x.example.global\"],\"queue_identifiers\":[\"...\"],\"database_connection\":[\"host:port/db\"]}. Look at iamRole, ingress host, and env vars (*_URL, *SQS*, *DB*).",

      // 3) WHERE each extracted field lands on the identity.
      "extract": [
        { "maps_to": "iam_role" },
        { "maps_to": "dns_aliases" },        // -> aliases the resolver matches on
        { "maps_to": "queue_identifiers" },  // -> resources
        { "maps_to": "database_connection" } // -> resources
      ]
    }
  ]
}
```

Three knobs you'll actually tune:

1. **`applies_to.match.has_path`** — must be a path that exists in the repo.
   For your repos that's `.example`. If it doesn't match, the blueprint never
   runs against that service (silent — no error).
2. **`source.glob`** — the file(s) holding the truth. Must be the real path in
   your repos (e.g. `.example/config/production/values.yaml`).
3. **`prompt_hint`** + **`extract[].maps_to`** — what to pull and where it goes.
   `dns_aliases` → aliases; `queue_identifiers` / `database_connection` →
   resources. Those are exactly what the resolver matches on.

`strategy: "llm"` means extraction calls your OpenCode model — so the
`opencode` block in your project/global config must point at a working server,
or extraction fails (and identities stay empty).

---

## How to add one (no code, just files)

You have ready-made examples in [`blueprints/`](../blueprints/). To install one
into your project:

```bash
cp blueprints/helm-values-identity.json \
   ~/.diffmind/projects/default/blueprints/helm-values-identity.json
```

or paste it into the **Blueprints** tab in the UI (it validates the JSON for
you). Then start a new run.

---

## The loop to get good edges

1. **Install a blueprint** whose `applies_to` matches your repos.
2. **Check the `match`/`glob` are real** — open one repo and confirm the path
   (`.example/config/production/values.yaml`) exists.
3. **Run it.** Watch the run's Progress drawer: extraction failures surface
   there as warnings (usually OpenCode connection or a bad glob).
4. **Inspect identities.** After the run, each service should have non-empty
   aliases/resources:
   ```bash
   cat ~/.diffmind/projects/default/runs/<run_id>/identities/<Service>.json
   ```
   If `aliases`/`resources` are still `null`, the blueprint didn't match or the
   LLM returned nothing — fix `has_path` / `glob` / `prompt_hint`.
5. **Read the resolver graph.** `edges` should climb and `unresolved` should
   drop. Anything still unresolved tells you which alias is missing — add it to
   the blueprint's extraction and re-run.

### Reading "unresolved" as a to-do list

Each unresolved entry is `service → target (reason)`. The `target` is the raw
string a service referenced that matched no identity. If you see
`ranking-service → gateway-service`, that's telling you
`gateway-service` is missing a `gateway-service` alias. Add it. Targets
like `black` / `flake8` / `global` are noise from richer DiffMind artifacts —
ignore those; they'll never resolve and that's fine.

---

## TL;DR for your situation

- DiffMind architecture findings are useful drill-down evidence, but they are
  not enough when they miss concrete instances.
- DiffMind's resolver graph depends on extracted service aliases,
  DB/cache/queue instances, and outbound targets.
- If the resolver graph is empty or weak, first check whether those facts were
  extracted.
- Drop `helm-values-identity.json` or a deterministic field-path blueprint into
  the project, make sure `has_path` and `glob` match your repos, confirm
  OpenCode is reachable if using LLM extraction, and re-run.

See [CONFIGURING_A_PROJECT.md](CONFIGURING_A_PROJECT.md) for the full config
reference (OpenCode, repos, runs).
