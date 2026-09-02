# Security policy

Please report suspected vulnerabilities privately through GitHub's security
advisory feature. Do not open a public issue containing credentials, private
repository contents, or exploit details.

DiffMind analyzes local source trees and can invoke local developer tooling.
Review repository trust boundaries before analyzing untrusted code, keep the
web server bound to `127.0.0.1`, and use an API token when exposing it beyond
localhost.

For a company deployment, terminate TLS at a trusted reverse proxy. Use the
per-user trusted-proxy mode for normal access, keep the shared admin token as a
recovery credential, and make the application listener unreachable directly.
DiffMind's mutation audit log is stored at
`$DIFFMIND_HOME/audit/http.jsonl`. See the
[company deployment guide](docs/company-deployment.md) for the complete header,
role, and secret-handling contract.
