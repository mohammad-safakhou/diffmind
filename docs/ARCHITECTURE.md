# Architecture

DiffMind has three internal layers behind one command and one persistent home.

1. The extractor reads one source repository and emits a deterministic
   `diffmind.service.v1` document with observations and evidence.
2. Deterministic knowledge packs translate organization-specific file
   conventions into service identities and resolution aliases.
3. The workspace owns projects and repositories, schedules extraction runs,
   and resolves service documents into a cross-repository graph.
4. The web UI exposes project setup, run progress, graph exploration, and
   impact analysis through the workspace API.

The protocol package is the boundary between extraction and graph assembly.
It contains no I/O policy beyond document encoding, schema generation, and
validation.

## Local data

```text
~/.diffmind/
├── config.json
├── diffmind-packs.lock
├── packs/
├── runs/
└── projects/
    └── <project-id>/
        ├── project.json
        ├── repos/
        ├── packs/
        └── runs/
```

Set `DIFFMIND_HOME` to relocate the entire tree. Repository analysis is
deterministic by default; generated facts should always retain concrete source
evidence.

## Knowledge precedence

Identity is assembled deterministically in this order:

1. A repository-owned `.diffmind/service.yaml` override.
2. Matching project and globally installed packs, ordered by explicit priority.
3. The repository name as a safe fallback.

Equal-priority packs that derive different service names fail the run instead
of silently choosing one. Each graph run records
`knowledge_pack_set_digest`, a digest of the exact pack content used.
