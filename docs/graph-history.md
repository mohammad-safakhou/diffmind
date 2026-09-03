# Graph history, comparison, and tracing

All queries are read-only and use saved project graphs. They do not fetch
repositories, run an analyzer, call an LLM, or rewrite history. A saved graph is
static evidence, not a record of runtime traffic.

## Compare two versions

Open a project and select **Compare graphs**, choose a **Before** and **After**
snapshot, then **Compare**. The default choices are the two newest available
graphs. The screen lists added, removed, and modified facts; expand a row to
inspect before/after evidence. **Swap** reverses the selected direction; press
**Compare** to apply it. **Load older runs** and change-page controls expose
history beyond the first page. Comparison URLs pin both runs and are shareable
with other users who can access the same server.

The equivalent MCP sequence is:

```json
{"name":"list_graph_runs","arguments":{"project":"example","limit":100}}
{"name":"compare_graphs","arguments":{"project":"example","from":"RUN_A","to":"RUN_B","limit":100}}
```

Or use HTTP (include authentication headers on a shared server):

```text
GET /api/v1/projects/example/graph/runs?offset=0&limit=100
GET /api/v1/projects/example/graph/compare?from=RUN_A&to=RUN_B&offset=0&limit=100
```

Both IDs must identify completed runs in the same project. There is no implicit
“previous” or “latest” in a comparison. Comparing a run with itself returns no
changes. Missing/corrupt artifacts fail explicitly; the query never substitutes
a different run. Run history includes status, timestamps, artifact availability,
recorded counts, and pack digests. `graph_available` checks file presence and
completed status, not content validity. History is newest-first by start time,
then descending ID. Concurrent creation/deletion can shift offset-based history
pages; comparisons themselves stay pinned to the selected pair.

Run history and comparisons accept `offset >= 0` and `limit` from 1 to 500
(omitted or 0 means 100). Follow `next_offset` until absent. Comparison totals
and added/removed/modified counts cover all changes, not only the returned page.
The UI requests 50 changes per page. Pagination bounds response counts, not
artifact loading/comparison cost or the size of individual evidence objects.

### What counts as a change?

- Services: name, known status, team, component kind/type.
- Objects: service + category + kind + name; repeated occurrences are preserved
  as a multiset. A rename is a removal and addition, not an inferred rename.
- Local flows: service + endpoint names/types (IDs as fallback when names are
  absent); saved reachability, conditions, and DAG content remain evidence.
- Resources: graph ID, identity/ownership/details and database table operations;
  older specialized queue/database/scheduler records are also supported.
- External systems: name and kind.
- Relationships: source + destination + edge type, retaining all occurrences,
  labels, confidence, and object evidence.

Keys are JSON-encoded tuples to avoid delimiter collisions. Top-level generated
object/flow IDs, ports, layout, counters, checkout paths, and freshness do not
produce changes. Nested IDs, source lines, ordered evidence arrays, and flow
content are retained: evidence changes can produce a modified fact even when
the topology is unchanged. Duplicate service/resource/external identities are
rejected rather than silently overwriting facts.

Responses include changed top-level fact fields and full before/after values,
repository artifact references that changed, and each run's recorded pack
digest. These are context, **not proof of why a change happened**. Historical
snapshots without pack digests remain queryable. No source diff, causal
explanation, runtime reachability, or universal extraction coverage is implied.

## Find a dependency path

```json
{"name":"find_dependency_path","arguments":{"project":"example","run":"RUN_A","from":"gateway","to":"db:products","depth":6}}
```

```text
GET /api/v1/projects/example/graph/path?run=RUN_A&from=gateway&to=db%3Aproducts&depth=6
```

Use exact service names, external node names, or resource graph IDs. The result
contains one deterministic shortest **directed** path and the saved edge
details. Direction follows the graph: incoming dependencies are not reversed.
A node's path to itself has zero hops. `depth` defaults to 6, maximum 20.

- `found`: a path exists in this saved graph; it is not an execution trace.
- `not_found`: the search exhausted reachable nodes without a path.
- `limited`: a depth, 10,000 visited-node, or 100,000 graph-edge budget prevented
  a complete search. Do not interpret this as absence of a dependency.

## Inspect an object or local flow

Use `get_service` to discover exact object/flow IDs, then pin the returned run:

```json
{"name":"get_object_trace","arguments":{"project":"example","run":"RUN_A","service":"gateway","object_id":"EXACT_ID"}}
```

```text
GET /api/v1/projects/example/graph/trace?run=RUN_A&service=gateway&object_id=EXACT_ID
```

The result includes matching objects, exact-ID local connections, and only
dependency-edge details tied to those IDs. Fuzzy names or adjacent services do
not qualify. `local_flow_available` means extracted local connections exist;
`partial` means the object exists without such connections. Neither proves a
continuous request path through another service. Incoming HTTP/RPC evidence
belongs to the caller and is not attached to the callee's object by coincidence.
Responses cap local connections and related edges at 200 each, sort before
truncation, and return full counts plus `truncated`. Query a narrower dependency
or flow ID to reduce matches.

Path and object queries allow omitting `run` to choose the latest completed
graph; every response returns the chosen `run_id`. Pin this ID for follow-up
questions so a scheduled refresh cannot silently mix evidence across versions.

## Permissions and errors

The new tools have the same viewer access as other read-only query tools. They
are available on both stdio and remote HTTP MCP. These tools cannot trigger
refresh or mutation. Shared-server roles remain global, not project-scoped.

HTTP returns 400 for invalid arguments or unreadable/corrupt graph content, 404
for unknown project/run/service/object/node, and 409 for a missing completed
graph artifact. Authentication failures use the existing 401/403 middleware.
MCP reports invalid input or query failures as errors, not empty successful
results.

## Verification

`go test ./...` includes semantic comparison, pinning, reversal/reordering,
pagination, missing/corrupt artifacts, depth/node/edge budgets, exact tracing,
stable truncation, viewer authorization, and HTTP/MCP response parity. The
pack acceptance fixture also compares actual graph builds before/after disabling
a knowledge pack. `make ui-test` covers routing and comparison-selection helpers.
