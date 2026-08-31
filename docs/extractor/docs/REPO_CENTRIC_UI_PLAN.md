# Plan: repositories-only UI + generate diffmind.yaml from runs

> Status: proposed (not yet implemented). Captures the agreed design for the
> next dashboard iteration.

## Context

The dashboard still exposes global **Catalog** and **Runs** as top-level nav,
which is confusing: the product is repository-centric, so the UI should be *only*
a repositories home, and everything about a repository should live inside that
repository. Also, a repository that has extraction runs but no `diffmind.yaml`
yet has no way to bootstrap one — there must be a one-click "generate the file
from a run".

Locked decisions:
- **Top nav = repositories only, no sidebar.** A slim brand header (returns home)
  is the only chrome. Per-run detail stays full-bleed.
- **A repository's `diffmind.yaml` IS its catalog.** The graph renders the
  *resolved file*; the global `architecture.v1.json` becomes a behind-the-scenes
  detail (no user-facing global catalog page). Node editing is deferred to the
  inline YAML editor for now.
- **A repository page has two sections: Overview and Graph.** (File operations
  and runs fold into Overview; the graph + inline editor are the Graph tab.)
- **Auto-generate the file from a run the user picks** (single run, repeatable).

Outcome: open the app → repositories → open one → see its summary + runs, build
or update its `diffmind.yaml` from a chosen run with a diff preview, and view/edit
its architecture graph — all in one place. No global Catalog/Runs pages.

## Model shift (run → file, directly)

Today the file workflow routed run facts through the global catalog
(Read/Propose/Merge). The repo-centric flow sources the file **directly from a
run**: pick a run → build a `catalog.Document` from its artifacts → diff against
the repo's file → merge new entries in. The global catalog and `import-run` stay
server-side for back-compat/CLI but are no longer used by the UI.

## A. Backend (small additions, heavy reuse)

All in `internal/ui/handlers_archfile.go` + one helper in `internal/archfile/`:

1. **`archfile.WriteProposalDoc(doc catalog.Document, genPath string) error`** —
   writes `generatedHeader + Marshal(Document(doc))` to `genPath` via `WriteRaw`.
   Defensive: drop connections whose endpoints aren't both present in `doc` (run
   artifacts are normally consistent, but guard against a dangling ref so a later
   `Resolve` can't fail). Tiny wrapper over existing `Document`/`Marshal`/`WriteRaw`.

2. **`GET /api/architecture/file-graph?path=<abs>`** → resolve the file
   (`archfile.Resolve` + `archfile.ToModel`) and return
   `{exposures, dependencies, connections}` (model slices, already JSON-tagged with
   `id`/`from_exposure_id`/`to_dependency_id`) so `OutcomeGraph` can render it.
   Missing file → empty graph.

3. **`POST /api/architecture/run-proposal`** `{path, run_id}` → build a
   `catalog.Document` from the run (`catalog.LoadRun(<baseDir>/<run_id>, run_id)`
   → `ImportInput` → `catalog.Document{Exposures,Dependencies,Connections}`),
   `archfile.WriteProposalDoc(doc, genPath)` where `genPath =
   <dir(path)>/.diffmind.generated.yaml`, then return
   `archfile.MergePreview(path, genPath)` (add/skip diff vs the current file; when
   the file is absent every entry is "add"). The existing
   `POST /api/architecture/merge-file` applies it (`MergeIntoMain` creates the file
   if missing).

Register routes in `internal/ui/server.go`. Validate `run_id` like
`handleArchitectureImport` (base-name, completed). Reuse `fileRequest.clean()`.

## B. Frontend (Preact)

### Routing & shell — `lib/router.js`, `App.jsx`
- `parseRoute`: keep `/` → `repos`, `/repos/:id` → `repo`, `/runs/:id` → `detail`.
  **Remove `/catalog` and `/runs`.** Update `router.test.js`.
- `App.jsx`: delete the `Shell` sidebar. Render a slim **TopBrand** bar (brand →
  `/`) above the routed view for `repos`/`repo`; render `Detail` full-bleed with no
  TopBrand (unchanged). Keep `AuthBanner` + `ToastHost`.
- `components/Shell.jsx` → replace with a minimal `TopBrand` (or inline in App);
  remove the multi-section nav.

### Repositories overview — `views/RepositoriesOverview.jsx`
- Keep the card grid. Card: name, path, file-status badge, node/connection/run
  counts, last-run status; actions **Open** and **New Run**. When a repo has runs
  but no file, the file badge reads "no file — generate" and Open lands on the
  Overview where the generate panel is primary. Keep "Add repository" (folder
  picker). Remove the "Open catalog" header action.

### Repository detail — `views/RepositoryDetail.jsx` (two tabs)
- **Overview**: repo summary (file status/path, counts, last run); a
  **`RepoFileSync`** panel; the repo's **runs** (`RunsList lockedRepo={repo.path}`);
  **New Run** (existing `RunForm` in a `Modal`, prefilled `repo_path`).
- **Graph**: **`RepoGraph`** — the visual graph from `/api/architecture/file-graph`
  rendered via `OutcomeGraph`, plus the inline YAML editor. Empty state with a
  "Generate from a run" shortcut when no file exists.

### New components
- **`components/RepoFileSync.jsx`** (replaces the run-routed parts of
  `FileWorkflow`): a run picker (the repo's completed runs from `listRuns` filtered
  by `repo_path`), **Preview** (`runProposal(path, runID)` → reuse the add/skip diff
  rendering moved out of `FileWorkflow`), and **Apply** (`mergeArchitectureFile(path)`).
  Path defaults to `repo.file_path` or `<repo.path>/diffmind.yaml`; on first
  successful apply, `upsertRepo({path, file_path})` to remember it. A small "change
  location" affordance reuses `FsPicker`. The framing is "Generate discovery file"
  when no file exists, "Update from run" when it does.
- **`components/RepoGraph.jsx`**: fetch `getFileGraph(path)`; render `OutcomeGraph`
  with that data (read-only graph for now); below it the inline `<textarea>` editor
  (reuse `getRepoFile`/`putRepoFile` + server validation, the editor lifted from
  `FileWorkflow`).

### `lib/api.js`
Add `getFileGraph(path)` (`GET /api/architecture/file-graph?path=`) and
`runProposal(path, runID)` (`POST /api/architecture/run-proposal`). Reuse existing
`mergeArchitectureFile`, `getRepoFile`, `putRepoFile`, `upsertRepo`, `listRuns`.

### Retire
- Delete routing to and the files `views/Architecture.jsx` (global catalog editor +
  NodeEditor/ConnectionEditor) and `views/Home.jsx` (global runs page).
  `components/FileWorkflow.jsx` is superseded by `RepoFileSync` + `RepoGraph` —
  remove it. Keep `OutcomeGraph`, `RunsList`, `RunForm`, `FsPicker`, `components/ui/*`.
- CSS: drop `.shell*` rules; add a slim `.topbrand`; keep `.tabbar`, repo cards,
  `.fw-diff*` (reused by the sync panel), `.fw-editor*` (reused by RepoGraph).

## Critical files
- `internal/archfile/write.go` — `WriteProposalDoc`.
- `internal/ui/handlers_archfile.go`, `internal/ui/server.go` — `file-graph` +
  `run-proposal` endpoints/routes.
- `internal/ui/web/src/lib/router.js` (+ `router.test.js`), `App.jsx`,
  `components/Shell.jsx`→TopBrand, `lib/api.js`.
- `internal/ui/web/src/views/RepositoryDetail.jsx`, `RepositoriesOverview.jsx`.
- New: `components/RepoFileSync.jsx`, `components/RepoGraph.jsx`.
- Remove: `views/Architecture.jsx`, `views/Home.jsx`, `components/FileWorkflow.jsx`.
- `internal/ui/web/src/styles/global.css`.

## Verification
1. `go build ./... && go test ./...` green; add a UI handler test for
   `run-proposal` (generate into an empty file via merge-file, then a second run
   adds only new entries) and `file-graph` (returns the file's nodes).
2. `cd internal/ui/web && npm run build` succeeds; `npm test` (router cases updated;
   no routes for catalog/runs).
3. Manual (`diffmind ui`): landing is repositories only (no sidebar, no
   Catalog/Runs nav); open a repo with runs and no file → Overview's generate panel,
   pick a run → diff preview → Generate creates `<repo>/diffmind.yaml` (path
   remembered); Graph tab shows the graph from the file + inline editor saves with
   validation; pick another run → "Update from run" shows only new entries → merge
   appends them; New Run works from the repo; `/runs/:id` detail still full-bleed.
