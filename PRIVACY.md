# Privacy Policy

This document describes the telemetry collected by AL LSP for Agents and how to disable it.

## What we collect

When telemetry is enabled, the wrapper emits **pseudonymous diagnostic events** to a single Azure Application Insights resource owned by the maintainer. Every event contains:

- Schema version, wrapper version, AL extension version
- OS / architecture
- Launcher (`vscode`, `claude-code`, or empty)
- A random session UUID (rotates every wrapper restart, never persisted)
- Active consent level (`errors` or `full`)

Per-event fields are enumerated in the design spec. All free-text user content is replaced by classifier enums or salted hashes.

## What we never collect

- Source code, file contents, or comments.
- Customer symbol names (only salted hashes that change per session).
- Paths containing your username (replaced with `<HOME>` / `<WS>`).
- Machine name, persistent identifiers.

## IP address handling

Application Insights ingestion observes the client IP for geolocation enrichment. The resource is configured (in `infra/appinsights.bicep`) so that only country-level enrichment is queryable. If Azure changes this behavior, the maintainer updates this document and bumps `schemaVersion`.

## How to disable

| Environment | Mechanism |
|-------------|-----------|
| Any | Set `AL_LSP_TELEMETRY=off` |
| Any | Pass `--telemetry off` to the wrapper |
| VS Code | Set `telemetry.telemetryLevel` to `off` |

## How to inspect what's sent

Set `AL_LSP_TELEMETRY_DUMP=<path>`. The wrapper writes every outgoing envelope as JSON Lines to that file *instead* of sending. Same code path — what you read is what would have shipped.

## Data retention

App Insights resource retention: 30 days. No extended retention. No export to a separate Log Analytics workspace beyond what is needed for queries.

## Where data lives

- Azure region: West Europe.
- Data controller: Torben Leth (sshadows@sshadows.dk).

## Legal basis (LIA summary)

GDPR Article 6(1)(f) legitimate interest:

- **Purpose:** identifying production failures of an open-source developer tool maintained by a single person, without which bug-fix latency would be measured in months.
- **Necessity:** the data minimization in the design (classifier enums, no free text, salted per-session hashes) is the lightest data set sufficient for the purpose.
- **Balancing:** users get a stable tool; data subjects keep code privacy via the scrub pipeline + dump-mode verifiability + opt-out controls. Residual re-identification risk is low.

## Subject rights

Events do not contain identifiers that allow the maintainer to locate a specific user's records, so direct access/erasure of past events is not feasible. You can stop processing at any time via the disable mechanisms above; this is the practical exercise of the right to object under Article 21.

## Changelog

- 2026-05-06 - Initial publication. schemaVersion=1.
