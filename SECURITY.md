# Security policy

Please report suspected vulnerabilities privately through GitHub's security
advisory feature. Do not open a public issue containing credentials, private
repository contents, or exploit details.

DiffMind analyzes local source trees and can invoke local developer tooling.
Review repository trust boundaries before analyzing untrusted code, keep the
web server bound to `127.0.0.1`, and use an API token when exposing it beyond
localhost.
