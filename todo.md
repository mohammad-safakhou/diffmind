Below is a **step-by-step implementation plan** for the *Repo Understanding Agent* core. Each step includes:

* **What to build**
* **How to do it technically**
* **Tools**
* **Definition of Done (DoD)** so you can say “this step is finished”

---

## Step 1 — Repo Snapshot + Artifact Store

### Build

A component that can take `(repo_url | local_path, ref)` and produce an immutable **snapshot** with file inventory + hashes + stored artifacts.

### How

1. Fetch source:

* If remote: `git clone --filter=blob:none` (partial clone) + `git checkout <ref>`
* If local: copy into a workspace (or treat as readonly with hash scan)

2. Create file inventory:

* Walk repo tree, compute:

  * path
  * size
  * sha256 (content hash)
  * file type (by extension + magic)
  * classification (source/config/doc/binary/vendor/generated)

3. Store artifacts:

* Save snapshot metadata in Postgres
* Save raw files (or zipped tarball) in object storage keyed by `(repo, commit, file_hash)`

### Tools

* Git CLI / libgit2
* SHA256 hashing (Go stdlib / Python)
* Object store: **MinIO** (S3-compatible, open-source)
* DB: Postgres

### DoD

* Given a repo+ref, you can reproduce the **exact file set** and hashes later.
* Re-running on same ref produces identical snapshot ID + inventory.
* Large repos don’t time out because you avoid full blob downloads when possible.

---

## Step 2 — Repo Classification (Multi-label) + Capability Scan (Cheap)

### Build

A fast scanner that outputs:

* `RepoProfile{labels[], confidence, evidence}`
* `RepoCapabilities{languages, build tools, CI, IaC, API specs, migrations, containers}`

### How

1. Rule-based detection:

* File signatures:

  * Build: `go.mod`, `package.json`, `pom.xml`, `build.gradle`, `Cargo.toml`, etc.
  * CI: `.github/workflows/*`, `.gitlab-ci.yml`, `Jenkinsfile`
  * IaC: `helm/Chart.yaml`, `*.tf`, `kustomization.yaml`, `*.yaml` under `k8s/`
  * API specs: `openapi*.yaml`, `*.proto`, `asyncapi.*`
* Count files by extension to infer languages.

2. Produce evidence:

* Each detected capability includes “why” = file path(s)

3. Classification:

* Weighted scoring:

  * “infra repo” if >X% YAML/TF + Helm charts + no runtime entrypoints
  * “service repo” if entrypoint + server patterns + Dockerfile + deploy manifests
  * allow multi-label (monorepo + service + infra, etc.)

### Tools

* Plain filesystem scanning
* YAML/JSON parsers for metadata extraction

### DoD

* For 20 diverse repos, scanner produces sensible labels + capability list and always includes evidence paths.
* Runs in seconds without parsing ASTs.

---

## Step 3 — Universal Parsing Layer (AST/CST Artifacts)

### Build

A parser pipeline that creates per-file parse artifacts for:

* source code (multi-language)
* structured configs

### How

1. Structured configs:

* YAML/JSON/TOML/INI/XML → parse into canonical JSON tree + preserve file spans if possible

2. Source code:

* Use Tree-sitter to parse supported languages.
* Store:

  * syntax tree (or compressed representation)
  * node-to-span mapping
  * basic symbols (function/class names, imports) via simple queries

3. Fallback:

* If parser missing: store file as “text” and allow regex analyzers + LLM later.

4. Artifact storage:

* Save parse artifacts in object store by `(snapshot_id, file_hash, parser_version)`

### Tools

* **Tree-sitter** (plus language grammars)
* YAML/JSON/TOML libs
* Optional: sqlite/duckdb local cache for parse outputs during a run

### DoD

* ≥90% of text files in typical repos produce a parse artifact (AST or structured config tree).
* Parse artifacts are reproducible by snapshot + parser version.
* You can retrieve “span for node” reliably for evidence.

---

## Step 4 — Evidence Model + Fact Schema (Non-negotiable core contract)

### Build

A strict schema for everything you extract:

* `Fact` (node/edge/attribute)
* `Evidence` (file span + content hash + optional AST path)
* `Provenance` (analyzer id/version, deterministic vs inferred)

### How

1. Evidence record must include:

* snapshot_id
* file_path
* start_line/col, end_line/col
* snippet_hash (hash of extracted snippet text)
* optional: ast_node_id / query name

2. Fact record must include:

* type (e.g., RuntimeUnit, Endpoint, ConfigKey, DTO, Migration)
* attributes map (typed)
* references to evidence IDs
* confidence score
* provenance

3. Validate all analyzer outputs against schema before persistence.

### Tools

* JSON Schema / Protobuf for contracts
* Strong typing in Go (or Rust) for internal model
* Migration tooling for Postgres schema

### DoD

* Any analyzer output without evidence is rejected.
* Facts can be rendered back to “show me the exact code span that caused this”.

---

## Step 5 — Deterministic Analyzers v1 (High ROI, Buildless)

### Build

A set of deterministic analyzers that work on parse artifacts + config trees.

### How (minimum set)

1. **Runtime Unit Detector**

* Detect entrypoints:

  * Go: `package main` + `func main`
  * Node: `package.json` scripts, `index.js`, framework boot files
  * Java: `public static void main`, SpringBootApplication
  * Python: `if __name__ == "__main__":`
* Detect containers: Dockerfile `CMD/ENTRYPOINT`, exposed ports

2. **HTTP Server Interface Extractor**

* Framework pattern matching:

  * Spring: `@RequestMapping`, `@GetMapping`, etc.
  * Express/Fastify: `app.get("/path", ...)`
  * Gin/Echo/Fiber: router registration patterns
* Output: method, path, handler location

3. **HTTP Client Call Extractor**

* Detect common libs:

  * Go: `http.NewRequest`, `client.Do`
  * JS: `fetch`, axios
  * Java: RestTemplate/WebClient/Feign patterns
* Extract method + URL/path when literal; otherwise extract expression + config key usage

4. **Config Key Extractor**

* Find reads of env/config:

  * `os.Getenv("X")`, viper keys, Spring `@Value`, node `process.env.X`
* Parse config files and bind keys (best-effort)

5. **CI/IaC Extractor**

* GitHub Actions: jobs, steps, env exports, secrets usage
* Helm/K8s: env vars, configMap refs, service names, ports
* Terraform: resources, outputs, variables

### Tools

* Tree-sitter queries per framework/language (start small, expand)
* YAML/JSON parsers
* Regex fallback for unknown frameworks

### DoD

* On a known service repo, you can list:

  * runnable units
  * inbound endpoints (if any)
  * outbound HTTP calls (if any)
  * config keys referenced
  * CI steps + deploy env injection points
* Every item links to evidence spans.

---

## Step 6 — Consolidation & De-dup (Canonical Repo Bundle)

### Build

A “fact normalizer” that merges analyzer outputs into canonical entities.

### How

* Merge strategy:

  * Same endpoint detected twice → same canonical Endpoint entity (same method+path+unit root)
  * Same config key referenced in multiple places → one ConfigKey entity with multiple evidence
* Attach everything to a **RuntimeUnit** where possible.
* Create Repo Intelligence Bundle JSON from canonical entities.

### Tools

* Deterministic merging rules (stable IDs)
* Stable ID generation:

  * `sha256(repo_id + snapshot_id + entity_type + natural_key)`

### DoD

* Bundle contains no duplicates for obvious entities.
* IDs are stable across runs on same snapshot.
* Bundle is “UI-ready” without additional interpretation.

---

## Step 7 — LLM Augmentation (Agent Mode, Bounded + Auditable)

### Build

An optional agent that improves coverage when deterministic analyzers are insufficient.

### How

1. Evidence pack builder:

* For a target task (e.g., “extract routes”), select only:

  * relevant files (router/controller files)
  * their parse summaries
  * related config files
  * extracted hints from deterministic pass
* Hard cap: file count / token budget.

2. Prompt contract:

* LLM must output structured Facts matching your schema
* Must reference evidence IDs (not “I think it’s here”)

3. Safety rails:

* LLM facts are marked `inferred=true` unless backed by deterministic evidence
* Confidence default lower unless strong evidence

### Tools

* Any LLM + tool wrapper
* JSON-schema constrained decoding (strongly recommended)
* Trace logs of prompts/responses stored as artifacts

### DoD

* When enabled, it increases extracted interface/config details on unknown frameworks.
* No fact is persisted without evidence references.
* You can audit exactly what the LLM saw.

---

## Step 8 — Persistence (Open-source storage)

### Build

Persist:

* operational metadata (runs)
* artifacts (snapshots, parse outputs, logs)
* extracted bundle + facts + evidence

### How

* Postgres:

  * runs, snapshots, file inventory, analyzer versions
  * fact/evidence indexes for search
* Object store (MinIO):

  * raw snapshot tar, parse artifacts, prompt logs
* Optional graph DB later; for repo-layer you can start with relational + JSONB and add graph DB after.

### Tools

* Postgres (JSONB + GIN indexes)
* MinIO

### DoD

* You can query: “show me all endpoints in this snapshot” fast.
* You can fetch evidence snippet for any fact.
* You can diff bundles between two snapshots.

---

## Step 9 — “Finished Step” Acceptance Tests (Repo-level)

### Build

A test harness that proves the engine works on unknown repos.

### How

* Curate a corpus:

  * 5 service repos (different languages)
  * 3 infra repos
  * 2 CI-only repos
  * 2 junk/script repos
* Assertions:

  * snapshot reproducibility
  * classification reasonable
  * at least one meaningful entity extracted where applicable
  * evidence always present
  * no crashes on weird files

### Tools

* Golden-file tests (store expected bundle summaries)
* Integration test runner in CI

### DoD

* Corpus passes consistently.
* Regression diffs are explainable (analyzer versioning).

---

If you want, I can turn this into a **checklist + folder/module structure** (Go packages, interfaces, job orchestration) so an engineer can start implementing immediately.

