# Synthetic company acceptance fixture

Three independent repositories model a Go HTTP gateway calling a Python/Django
catalog and a Java/Spring billing service. A third HTTP destination is genuinely
external and must not be silently joined to an internal service.

`expected.json` is the reviewed, exact service/relationship expectation. The
acceptance test copies these sources into temporary Git repositories and runs
the real Diffmind CLI, ingestion pipeline, graph builder, and query interfaces.
No framework packages, company credentials, network services, or LLM are needed.
The source applications are analysis fixtures, not production application code.
The same fixture powers `scripts/prepare-demo.sh` for contributor onboarding.
The test also restores a real CLI backup and checks historical graphs and the
original job/ingestion attempt records.

Run `go test ./internal/workspace/ui -run TestCompanyAcceptance -v` from the
repository root. This fixture intentionally covers a small supported slice;
passing it is not a claim of correctness for every company or framework.
