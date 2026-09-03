# Spring Cloud OpenFeign configured URLs

Opt in with `diffmind pack install ./packs/spring-openfeign-config`.
Then rebuild the project graph.

Reads `spring.cloud.openfeign.client.config.<client>.url` from the base
`src/main/resources/application.yaml`, `application.yml`, and
`application.properties` files. Fixtures cover all three formats.

These are **configured** relationships. They do not prove that a client is
instantiated, called, or active in production. Annotation URLs can override
configuration; see the [official OpenFeign URL rules](https://docs.spring.io/spring-cloud-openfeign/reference/spring-cloud-openfeign.html).

Limitations: no active-profile selection/merging, external Config Server,
environment expansion, annotation/config precedence, YAML merge aliases,
multi-line/escaped properties, load-balancer discovery, or dynamic URLs.
Base files in both formats are treated as separate declarations, not resolved
with Spring precedence. Profile-specific filenames are intentionally excluded.
URLs with credentials, query strings or fragments are rejected. This pack
does not replace the existing AST Feign detector.

All fixtures are synthetic. Pack and fixtures are Apache-2.0 under the root LICENSE.
