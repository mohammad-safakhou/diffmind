# README media assets

These assets are created from generated DiffMind workspaces and aggregate
results from pinned public repositories, never a company workspace.

| File | Role |
| --- | --- |
| `diffmind-demo.gif` | Fourteen-second README story with cross-faded, tightly framed scenes |
| `public-proof.svg` | Editable comparison artwork for all four public proof levels |
| `public-proof.png` | Rendered README version of the comparison artwork |
| `project-list.jpg` | Synthetic project card |
| `demo-shop-graph.jpg` | Six-service architecture graph |
| `graph-comparison.jpg` | Baseline and changed saved graphs |
| `operations-history.jpg` | Durable ingestion and operation history |
| `enterprise-overview.png` | 15-service team scope from the generated 150-service company |

Dashboard stills used by the animation are captured at 1920×1080. `make
demo-media` rebuilds the 1200×675, 12-fps GIF and requires `ffmpeg`. Capture from
a fresh public workspace, keep the browser free of personal tabs or overlays,
and reject any frame containing a local username, private path, real
organization name, credential or token.
