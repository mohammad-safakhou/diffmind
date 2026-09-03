# Tested patterns and limitations

Diffmind is deterministic static analysis, not a runtime dependency inventory.
These are reproducible baselines, not a claim that every use of a framework is
supported. A registered detector is not equivalent to complete framework support.

| Surface | Tested baseline | Verification | Important limits |
| --- | --- | --- | --- |
| Go `net/http` | Literal package-level Get/Head/Post/PostForm calls; import aliases and shadowing negatives | `internal/extractor/detectors/languages/golang/httpclient/nethttp/nethttp_test.go`, company acceptance | Dynamic URLs, custom client wrappers and request construction are outside this detector |
| Python Django | A simple service route used in a cross-repository HTTP relationship | `testdata/company/catalog`, company acceptance | Not a guarantee for arbitrary routing, middleware or dynamic URL construction |
| Java Spring MVC | A simple service endpoint used in a cross-repository HTTP relationship | `testdata/company/billing`, company acceptance | Not a guarantee for arbitrary annotation composition or runtime configuration |
| Helm identity (built in) | Name, ingress aliases, owned queues, port metadata | `packs/helm-values/testdata`, pack tests | Conventional `deploy/values.yaml`; no Helm rendering or chart evaluation |
| Explicit service manifest (opt in) | HTTP/RPC targets, aliases, regex service resolution, queue publishers/consumers, external HTTP | `packs/service-manifest`, pack graph acceptance over HTTP/MCP | Declarations are not proof of a call or entrypoint reachability; no resource creation or infrastructure execution |
| Spring Cloud OpenFeign configuration (opt in) | Literal client URLs in base application YAML/YML and single-line properties; comments/placeholders/credentials negative cases | `packs/spring-openfeign-config` | No profile selection, environment/config-server expansion, annotation precedence, load-balancing discovery or escaped/continued properties |

The real-binary company acceptance test imports synthetic Go/Python/Java Git
repositories and checks exact service relationships and source evidence through
HTTP and MCP. Pack graph acceptance separately verifies installation/integrity,
the graph job, query transport, disabling packs and immutable historical graphs.
Neither substitutes for validating your company's known architecture.

## Teaching a missing convention

1. Reduce it to a **synthetic** repository/configuration example.
2. If it is a declarative convention, start with `diffmind pack init ./my-pack`.
   Adapt a field path or narrow regex; add exact detection and graph assertions.
3. Include a near-match negative fixture so a rule cannot pass by overmatching.
4. Run `diffmind pack lint ./my-pack`, `diffmind pack test ./my-pack` and
   `diffmind pack explain ./my-pack --repo ./test-repo`.
5. Install the pack, rebuild the project graph, and compare it against known
   relationships. Contribute the pack, fixtures and documented limits together.

For semantics such as shadowing, call reachability or framework annotations,
extend the AST detector and its tests instead of relying on a broad code regex.
Packs cannot run code, execute a shell, call an LLM, or load an arbitrary plugin.
See [pack authoring](knowledge-packs.md) and [contributing](../CONTRIBUTING.md).

The OpenFeign pack follows the configured URL key described in the
[official Spring Cloud OpenFeign documentation](https://docs.spring.io/spring-cloud-openfeign/reference/spring-cloud-openfeign.html).
Those rules also explain why configuration alone does not establish the effective
runtime target: an annotation URL can take precedence.
