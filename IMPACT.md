# WoT Scoring — Impact Statement

## The Problem

Nostr has no built-in way to assess how trustworthy a pubkey is. Every client shows the same profile information whether someone has 50,000 followers or was created yesterday. Spam and impersonation are filtered per-client with custom heuristics that don't share data. NIP-85 defines a standard for trust attestations, but there are almost no implementations publishing these events today.

## What We Built

WoT Scoring is a complete NIP-85 Trusted Assertions provider — the only known implementation that publishes all five assertion kinds:

- **Kind 30382 — User Assertions.** PageRank trust scores computed over 51,000+ nodes and 620,000+ edges from the Nostr follow graph. Each event includes 12 metadata tags (rank, followers, posts, reactions, zaps).
- **Kind 30383 — Event Assertions.** Per-event engagement scores (comments, reposts, reactions, zaps) for notes by top-ranked pubkeys.
- **Kind 30384 — Addressable Event Assertions.** Engagement scoring for long-form articles (kind 30023) and live activities (kind 30311).
- **Kind 30385 — External Identifier Assertions (NIP-73).** Scores for hashtags and URLs shared by high-WoT pubkeys, enabling trust-weighted trending topics.
- **Kind 10040 — Provider Authorization.** Consumes and serves authorization events where users explicitly authorize trusted scoring providers.

**Live service:** [wot.klabo.world](https://wot.klabo.world) — 34 API endpoints, auto re-crawls every 6 hours, publishes to 5 relays. L402 Lightning paywall deployed to production. Machine-readable [OpenAPI 3.0 spec](https://wot.klabo.world/openapi.json) and interactive [Swagger UI explorer](https://wot.klabo.world/swagger) for automated integration and live API testing.

## Functional Readiness

The service is deployed and running in production. All 34 endpoints serve live data. 267 automated tests pass in CI (including L402 paywall, community detection, authorization, NIP-05 single/bulk/reverse verification, trust timeline, spam detection, batch spam, graph visualization, cross-provider assertion verification, OpenAPI spec validation, and Swagger UI tests). The binary is a single Go executable with one dependency (go-nostr). Docker, systemd, and bare-metal deployment are all supported. NIP-89 handler announcements are published on startup so clients can auto-discover the service.

Interactive UI features:
- **Score Lookup** — real-time trust score search with live debounced queries
- **Compare** — side-by-side trust comparison with relationship badges and shared follow analysis
- **Trust Path** — BFS shortest path visualization between any two pubkeys
- **Timeline** — trust evolution over time with monthly follower growth bars and velocity coloring
- **Spam Check** — multi-signal spam analysis with visual signal breakdown and classification
- **Trust Graph** — interactive force-directed SVG visualization of a pubkey's trust network with color-coded nodes (follows, followers, mutual)
- **Leaderboard** — top 10 pubkeys with live data from the scoring API

## Depth & Innovation

Beyond standard PageRank scoring, we implemented:

- **Personalized trust scoring** (`/personalized`) — scores a target pubkey relative to any viewer's follow graph, blending global PageRank (50%) with social proximity (50%). This enables per-user trust assessments without clients running their own graph computations.
- **Composite scoring from multiple providers** — consumes kind 30382 events from external NIP-85 providers and blends them into a composite score (70% internal + 30% external average). This is true multi-provider NIP-85 interoperability.
- **Time-decay scoring** (`/decay`) — exponential decay where newer follows weigh more, with configurable half-life. Reveals emerging trust vs. legacy reputation. `/decay/top` shows rank changes vs. static PageRank.
- **Score auditing** (`/audit`) — full transparency into why a pubkey has its score, including PageRank breakdown, engagement metrics, top followers, and external assertion details.
- **Trust path finder** (`/graph`) — BFS shortest path between any two pubkeys through the follow graph, showing trust chains up to 6 hops.
- **Follow recommendations** (`/recommend`) — friends-of-friends analysis weighted by mutual follow ratio (60%) and WoT score (40%).
- **Similar pubkey discovery** (`/similar`) — Jaccard similarity (70%) + WoT score (30%) for finding pubkeys with overlapping follow graphs.
- **Relay trust assessment** (`/relay`) — combines infrastructure trust data from trustedrelays.xyz with operator social reputation from PageRank (70/30 blend).
- **Trust comparison** (`/compare`) — side-by-side comparison showing direct relationship, shared follows, Jaccard similarity, and trust path.
- **Community detection** (`/communities`) — label propagation algorithm identifies trust communities within the follow graph, revealing organic clusters of related users.
- **Authorization tracking** (`/authorized`) — consumes kind 10040 events from relays, showing which users have explicitly authorized specific NIP-85 scoring providers.
- **NIP-05 identity verification** (`/nip05`) — resolves NIP-05 identifiers (user@domain) to pubkeys via standard `.well-known/nostr.json`, then returns the full WoT trust profile. Bridges the Nostr identity layer with NIP-85 trust assertions in a single API call.
- **Bulk NIP-05 verification** (`POST /nip05/batch`) — resolves up to 50 NIP-05 identifiers concurrently and returns trust profiles for each. Enables clients to verify and trust-score entire contact lists or directories in a single request.
- **Reverse NIP-05 lookup** (`/nip05/reverse`) — given a pubkey, fetches their kind 0 profile from relays, extracts the NIP-05 identifier, and bidirectionally verifies it resolves back to the same pubkey. Enables "who is this pubkey?" identity lookups — the inverse of standard NIP-05 resolution.
- **Spam detection** (`/spam`) — multi-signal spam classification combining 6 weighted indicators: WoT score (30%), follower/following ratio (15%), account age (15%), engagement received (15%), reports received (15%), and activity pattern (10%). Returns a 0.0-1.0 spam probability with classification ("likely_human", "suspicious", "likely_spam") and transparent signal breakdown explaining each factor. Enables clients to filter spam without running their own heuristics.
- **Batch spam filtering** (`POST /spam/batch`) — check up to 100 pubkeys for spam in one request, with summary counts (likely_human, suspicious, likely_spam, errors). Enables clients to filter entire contact lists or relay event feeds for spam without individual queries.
- **Trust graph visualization** (`/weboftrust`) — returns a D3.js-compatible force-directed graph (nodes + links) centered on a pubkey. Nodes are colored by relationship type (follow, follower, mutual) and sized by WoT score. Clients can render interactive trust network maps. The landing page includes a built-in SVG visualization with zoom and limit controls.
- **Mute list analysis** (`/blocked`) — NIP-51 kind 10000 mute list integration providing two modes: (1) who a pubkey has muted, and (2) who has muted a target pubkey. The reverse lookup produces a "community moderation signal" — when multiple high-WoT users have independently blocked the same pubkey, it's a strong negative trust indicator. Signals range from "no_data" through "weak_negative", "moderate_negative", to "strong_negative". Each entry includes WoT scores for context. This bridges NIP-51 (mute lists) with NIP-85 (trust assertions), adding negative trust signals that complement the positive signals from follows and engagement.
- **Cross-provider assertion verification** (`POST /verify`) — accepts any NIP-85 kind 30382 event as JSON, verifies its cryptographic signature and event ID, then cross-references claimed rank and follower count against our own graph data. Returns a verdict: "consistent" (claims match within tolerance), "divergent" (significant disagreement), "unverifiable" (no verifiable claims), or "invalid" (bad signature/structure). Each field check includes both the claimed and observed values. This is the first known NIP-85 cross-provider verification endpoint — enabling clients to assess whether multiple independent providers agree on a pubkey's trust profile.
- **Full NIP-85 kind 30382 tag compliance** — publishes ALL spec-defined tags: rank, followers, post/reply/reaction counts, zap stats, daily zap averages, common topics (hashtags), active hours (UTC), reports sent/received, and account age. No other known provider publishes all 17 tag types.

## Interoperability

- **Publishes** all five NIP-85 kinds to public relays (relay.damus.io, nos.lol, relay.primal.net) and NIP-85 dedicated relays (nip85.nostr1.com, nip85.brainstorm.world)
- **Consumes** kind 30382 assertions from external NIP-85 providers, with deduplication and freshness checks
- **Verifies** kind 30382 assertions from ANY provider via `/verify` — cryptographic signature validation + claim cross-referencing against our own graph
- **Consumes** kind 10000 mute lists (NIP-51) from relays, building a reverse index for community moderation analysis
- **NIP-89 handler** published on startup for automatic client discovery
- **Batch API** for clients that need to score many pubkeys at once (up to 100 per request)
- **NIP-05 identity resolution** — `/nip05` endpoint resolves NIP-05 identifiers to pubkeys and returns WoT trust profiles, bridging identity verification with trust scoring
- **Bulk NIP-05 verification** — `POST /nip05/batch` resolves up to 50 identifiers concurrently, enabling directory-scale identity-to-trust verification
- **Reverse NIP-05 lookup** — `/nip05/reverse` resolves pubkey→NIP-05 by fetching kind 0 profiles from relays, with bidirectional verification
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
- 252 tests covering scoring, normalization, event parsing, relay trust, L402 paywall, community detection, authorization, NIP-05 single/bulk/reverse verification, trust timeline, spam detection, batch spam, mute list analysis, graph visualization, topics, activity hours, reports, API handlers, API documentation, OpenAPI spec validation, and Swagger UI
- Interactive API documentation at `/docs` with endpoint cards, request/response examples, and live "Try it" buttons
- [Swagger UI API explorer](https://wot.klabo.world/swagger) at `/swagger` — interactive testing of all 33 endpoints directly in the browser
- Machine-readable [OpenAPI 3.0.3 spec](https://wot.klabo.world/openapi.json) at `/openapi.json` — enables automated client generation, Swagger UI integration, Postman import, and MCP agent discovery
- This impact statement and technical architecture documented in the repository

## Business Model Sustainability

**L402 Lightning Paywall — implemented and deployed.**

The API uses the L402 protocol (HTTP 402 Payment Required) with Lightning Network micropayments via LNbits. This is a working, Bitcoin-native revenue model — not a hypothetical future plan.

**Pricing:**

| Endpoint | Price | Free Tier |
|----------|-------|-----------|
| `/score` | 1 sat | 10/day per IP |
| `/decay` | 1 sat | 10/day per IP |
| `/nip05` | 1 sat | 10/day per IP |
| `/personalized` | 2 sats | 10/day per IP |
| `/similar` | 2 sats | 10/day per IP |
| `/recommend` | 2 sats | 10/day per IP |
| `/compare` | 2 sats | 10/day per IP |
| `/nip05/reverse` | 2 sats | 10/day per IP |
| `/spam` | 2 sats | 10/day per IP |
| `/blocked` | 2 sats | 10/day per IP |
| `/audit` | 5 sats | 10/day per IP |
| `/nip05/batch` | 5 sats | 10/day per IP |
| `/spam/batch` | 10 sats | 10/day per IP |
| `/weboftrust` | 3 sats | 10/day per IP |
| `/batch` | 10 sats | 10/day per IP |

**How it works:**
1. First 10 requests/day per IP are free (no payment needed)
2. After free tier: API returns HTTP 402 with a Lightning invoice
3. Client pays invoice, retries request with `X-Payment-Hash` header
4. Server verifies payment via LNbits, serves the response

Unpriced endpoints (`/top`, `/stats`, `/health`, `/export`, `/providers`, `/graph`, `/event`, `/external`, `/relay`, `/metadata`, `/communities`, `/authorized`, `/decay/top`, `/publish`) remain free and unlimited.

**Cost structure:** Near-zero. Single Go binary, no database, no external API costs. Hosting is a single VPS or Cloudflare Tunnel. The only variable cost is relay bandwidth, which scales linearly and is negligible at current volumes.

**Market:** Every Nostr client needs spam filtering and trust signals. As the protocol grows, the demand for shared trust infrastructure grows with it. NIP-85 is the standard; we're the reference implementation. The L402 paywall ensures the service sustains itself from the ecosystem it serves.

## Source Code

https://github.com/joelklabo/wot-scoring — MIT licensed.
