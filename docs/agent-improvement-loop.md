# Agent-assisted improvement loop

DiffMind treats an agent's architecture assertion as a proposal, never as a
fact. The deterministic analyzer, saved source evidence, tests, and a reviewer
decide what becomes active.

## During a coding task

1. Query the smallest relevant service, endpoint, contract, and dependency
   page before editing code. Check the saved analysis revision and unresolved
   facts.
2. Record a gap only when expected and observed behavior are concrete. Keep
   proprietary source out of the record; use repository/run/object IDs and
   bounded `path:line` pointers.
3. Classify the fix as a repository override, private company pack, reusable
   detector, or core pipeline bug. Do not hide a pipeline defect with an alias.
4. Reproduce it with a synthetic positive and negative fixture. For reusable
   work, run the pack tests and full graph assertion against a pinned baseline.
5. Preview the exact graph/contract changes. Advance the record through
   `observed -> reproduced -> proposed -> tested -> accepted -> active` using
   the latest collection revision. Rejection, deferral, superseding, and
   rollback retain their reason and history.
6. Activate only within the user's authority. Rebuild the affected graph,
   inspect evidence, run the application tests, then resume the original task.

Use `GET /api/v1/projects/{pid}/improvement-gaps` or
`list_improvement_gaps` first. Creation and transitions use optimistic
revisions, preventing two agents from silently overwriting review state. The
state transition to `active` records acceptance; it deliberately does not edit
or install a pack by itself. Use the existing validated pack APIs/commands for
that mutation and keep rollback material.

Default budget: one local proposal or five minutes of improvement work per
ordinary coding task. Expanding detector support, publishing fixtures, opening
an issue/PR, running recurring work, or uploading source requires explicit
authorization. Private configuration remains private by default.
