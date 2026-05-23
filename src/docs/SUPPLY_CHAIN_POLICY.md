# SUPPLY_CHAIN_POLICY

Date: 2026-03-18 UTC
Status: active

## Purpose
Define the explicit network and dependency policy for building `sharedwatch`.

## Allowed external Go module domains
The following domains are explicitly allowlisted for Go module resolution in this project:
- `proxy.golang.org`
- `sum.golang.org`

## Rationale
`sharedwatch` currently depends on a Go SQLite module fetched through the standard Go module supply chain. To keep the build path explicit and auditable, the standard Go proxy and checksum domains are recorded here as approved.

## Scope
This allowlist applies to:
- `go mod tidy`
- `go test`
- `go build`
- `install.sh`
- local/manual project build flows

## Notes
- This is an additive policy record.
- It does not authorize arbitrary outbound network access.
- If the project later vendors dependencies or moves to a different resolution path, this policy should be updated rather than silently relied upon.
