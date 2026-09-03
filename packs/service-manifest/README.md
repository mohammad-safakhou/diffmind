# Explicit architecture declarations

Opt-in pack for any language. Install with `diffmind pack install ./packs/service-manifest`,
then put `service-architecture.yaml` at each service's repository root.
Copy the gateway/catalog fixtures as a starting point.

Declarations are **configured relationships, not proof of executed calls**. They
supplement built-in analysis without inventing entrypoint-to-dependency traces.
HTTP supports literal URLs or service names; RPC supports literal service/host
names; queues support literal names (platform Kafka in this example).
Unresolved placeholders and HTTP URLs with credentials, query strings or
fragments are skipped. No environment variables or code are evaluated.

Edit the rules to match your own manifests, test the complete graph using
`diffmind pack test ./packs/service-manifest`, then contribute synthetic
fixtures. The graph fixture exercises aliases, regex resolution, external
targets, RPC, queue publishing and consuming.

This pack and its synthetic fixtures are Apache-2.0, under the repository LICENSE.
