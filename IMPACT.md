# WoT Scoring — Impact Statement

## The Problem

Nostr has no built-in way to assess how trustworthy a pubkey is. Every client shows the same profile information whether someone has 50,000 followers or was created yesterday. Spam and impersonation are filtered per-client with custom heuristics that don't share data. NIP-85 defines a standard for trust attestations, but there are almost no implementations publishing these events today.

## What We Built

WoT Scoring is a complete NIP-85 Trusted Assertions provider — the only known implementation that publishes all four assertion kinds:

- **Kind 30382 — User Assertions.** PageRank trust scores computed over 51,000+ nodes and 620,000+ edges from the Nostr follow graph. Each event includes 12 metadata tags (rank, followers, posts, reactions, zaps).
- **Kind 30383 — Event Assertions.** Per-event engagement scores (comments, reposts, reactions, zaps) for notes by top-ranked pubkeys.
- **Kind 30384 — Addressable Event Assertions.** Engagement scoring for long-form articles (kind 30023) and live activities (kind 30311).
- **Kind 30385 — External Identifier Assertions (NIP-73).** Scores for hashtags and URLs shared by high-WoT pubkeys, enabling trust-weighted trending topics.

**Live service:** [wot.klabo.world](https://wot.klabo.world) — 17 API endpoints, auto re-crawls every 6 hours, publishes to 3 relays.

## Functional Readiness

The service is deployed and running in production. All 17 endpoints serve live data. 175 automated tests pass in CI. The binary is a single Go executable with one dependency (go-nostr). Docker, systemd, and bare-metal deployment are all supported. NIP-89 handler announcements are published on startup so clients can auto-discover the service.

## Depth & Innovation

Beyond standard PageRank scoring, we implemented:

- **Personalized trust scoring** (`/personalized`) — scores a target pubkey relative to any viewer's follow graph, blending global PageRank (50%) with social proximity (50%). This enables per-user trust assessments without clients running their own graph computations.
- **Composite scoring from multiple providers** — consumes kind 30382 events from external NIP-85 providers and blends them into a composite score (70% internal + 30% external average). This is true multi-provider NIP-85 interoperability.
- **Score auditing** (`/audit`) — full transparency into why a pubkey has its score, including PageRank breakdown, engagement metrics, top followers, and external assertion details.
- **Trust path finder** (`/graph`) — BFS shortest path between any two pubkeys through the follow graph, showing trust chains.
- **Follow recommendations** (`/recommend`) — friends-of-friends analysis for discovery.
- **Relay trust assessment** (`/relay`) — combines infrastructure trust data from trustedrelays.xyz with operator social reputation from PageRank.

## Interoperability

- **Publishes** all four NIP-85 kinds to public relays (relay.damus.io, nos.lol, relay.primal.net)
- **Consumes** kind 30382 assertions from external NIP-85 providers, with deduplication and freshness checks
- **NIP-89 handler** published on startup for automatic client discovery
- **Batch API** for clients that need to score many pubkeys at once
- **npub support** on all endpoints — accepts both hex and NIP-19 encoded keys
- Standard JSON responses with CORS headers for browser-based clients

Any Nostr client can query our assertions from relays using a standard REQ filter:

```json
["REQ", "wot", {"kinds": [30382], "authors": ["<our-pubkey>"], "#d": ["<target-pubkey>"]}]
```

## Decentralizing Ecosystem Impact

NIP-85 enables a marketplace of trust providers. Instead of one centralized reputation authority, multiple independent scoring services can publish attestations using different algorithms, seed sets, and trust models. Clients choose which providers to trust.

Our service demonstrates this by actively consuming assertions from other providers. When a second NIP-85 provider publishes kind 30382 events, our composite scoring automatically incorporates their data — no coordination required, just shared relay infrastructure.

The relay trust endpoint further decentralizes infrastructure trust by combining independent data sources (trustedrelays.xyz infrastructure metrics + our social graph reputation) into a single assessment.

## Documentation & Openness

- MIT licensed, public repository: [github.com/joelklabo/wot-scoring](https://github.com/joelklabo/wot-scoring)
- Comprehensive README with every endpoint documented and example responses
- CI: GitHub Actions running `go vet`, `go test -race`, and `go build` on every push
- 175 tests covering scoring, normalization, event parsing, relay trust, and API handlers
- This impact statement and technical architecture documented in the repository

## Business Model Sustainability

**Freemium API model.** The HTTP API is free for public queries at low volume. High-volume consumers (clients checking thousands of pubkeys daily) would use the batch endpoint under a paid tier. Revenue model:

1. **Free tier** — public API, rate-limited, suitable for individual clients and small apps
2. **Paid tier** — higher rate limits, SLA guarantees, priority crawl scheduling, custom seed sets for personalized scoring
3. **NIP-85 assertion subscriptions** — relay-based delivery requires no API; clients pay relay operators, not us. Our revenue comes from the HTTP API convenience layer.
4. **Consulting** — custom trust models, private deployments for organizations that want their own scoring instance

**Cost structure:** Near-zero. Single Go binary, no database, no external API costs. Hosting is a single VPS or Cloudflare Tunnel. The only variable cost is relay bandwidth, which scales linearly and is negligible at current volumes.

**Market:** Every Nostr client needs spam filtering and trust signals. As the protocol grows, the demand for shared trust infrastructure grows with it. NIP-85 is the standard; we're the reference implementation.

## Source Code

https://github.com/joelklabo/wot-scoring — MIT licensed.
