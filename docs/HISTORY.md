# Repository history

DiffMind was consolidated from four independent Git histories. The migration
keeps every reachable source commit, including original author and committer
timestamps, while normalizing paths, public terminology, and contributor email
addresses.

The retained source histories contain 257 unique commits:

| History | Reachable commits |
| --- | ---: |
| Original DiffMind repository | 8 |
| Extractor | 209 |
| Protocol | 5 |
| Workspace | 35 |

The default branch joins those histories with explicit merge commits. Historical
side branches are preserved under `archive/extractor/*`, `archive/protocol/*`,
and `archive/workspace/*` tags so a normal clone receives them. The pre-unified
DiffMind tip is retained as `archive/diffmind-original`.

Because the migration changed paths and removed private terminology from old
blobs and messages, commit hashes necessarily changed. Git authorship and both
author/committer dates were retained.
