# WoT Scoring — Nostr Web of Trust

**Live:** [wot.klabo.world](https://wot.klabo.world)

[![CI](https://github.com/joelklabo/wot-scoring/actions/workflows/ci.yml/badge.svg)](https://github.com/joelklabo/wot-scoring/actions/workflows/ci.yml)

NIP-85 Trusted Assertions provider. Crawls the Nostr follow graph, computes PageRank trust scores, collects per-pubkey, per-event, and per-identifier engagement metadata, publishes kind 30382/30383/30384/30385 events to relays, **consumes kind 10040 provider authorizations**, **consumes kind 10000 mute lists (NIP-51) for community moderation signals**, enriches relay data from trustedrelays.xyz with social reputation, **detects trust communities** via label propagation, and **consumes assertions from external NIP-85 providers** for composite trust scoring. Auto re-crawls every 6 hours.

## What it does

1. Crawls kind 3 (contact list) events from seed pubkeys (depth 2)
2. Builds a directed graph of follow relationships
3. Computes PageRank over the graph
4. Crawls kind 1 (notes), kind 7 (reactions), and kind 9735 (zap receipts) for user metadata
5. Crawls event engagement (comments, reposts, reactions, zaps) for kind 30383/30384
6. Crawls addressable events (kind 30023 long-form, kind 30311 live activities) for kind 30384
7. Crawls external identifiers — hashtags (t-tags) and URLs (r-tags) — for kind 30385
8. Serves scores and metadata via HTTP API
9. Combines relay infrastructure trust (trustedrelays.xyz) with operator social reputation (PageRank)
10. Publishes all four NIP-85 assertion kinds to Nostr relays
11. **Consumes kind 30382 assertions from external NIP-85 providers**
12. Computes composite trust scores blending internal PageRank with external assertions
13. **Consumes kind 10040 provider authorization events** — tracks which users trust which providers
14. **Consumes kind 10000 mute lists (NIP-51)** — builds reverse index for community moderation signals
15. **Detects trust communities** via label propagation over the follow graph
16. Re-crawls automatically every 6 hours

## API

Live at https://wot.klabo.world — try any endpoint below.

```
GET /                        — Service info and endpoint list
GET /health                  — Health check (status, graph size, uptime)
GET /score?pubkey=<hex>      — Trust score for a pubkey (kind 30382) + composite from external providers
GET /audit?pubkey=<hex>      — Score audit: full breakdown of why a pubkey has its score
GET /personalized?viewer=<hex>&target=<hex> — Personalized trust score relative to viewer's follow graph
POST /batch                  — Score up to 100 pubkeys in one request (JSON: {"pubkeys":[...]})
GET /similar?pubkey=<hex>    — Find similar pubkeys by follow-graph overlap
GET /recommend?pubkey=<hex>  — Follow recommendations (friends-of-friends)
GET /metadata?pubkey=<hex>   — Full NIP-85 metadata (followers, posts, reactions, zaps)
GET /event?id=<hex>          — Event engagement score (kind 30383)
GET /external?id=<ident>     — External identifier score (kind 30385, NIP-73)
GET /external                — Top 50 external identifiers (hashtags, URLs)
GET /relay?url=<wss://...>   — Relay trust + operator WoT (via trustedrelays.xyz)
GET /decay?pubkey=<hex>      — Time-decayed trust score (newer follows weigh more)
GET /decay/top               — Top pubkeys by decay-adjusted score with rank changes
GET /authorized              — Kind 10040 authorized users (who declared trust in this provider)
GET /authorized?pubkey=<hex> — Authorizations for a specific provider
GET /communities             — Top trust communities (label propagation clusters)
GET /communities?pubkey=<hex>— Community membership and top peers for a pubkey
GET /nip05?id=user@domain    — NIP-05 verification + WoT trust profile (resolves identity to pubkey)
POST /nip05/batch            — Bulk NIP-05 verification (up to 50 identifiers, concurrent)
GET /nip05/reverse?pubkey=<hex|npub> — Reverse NIP-05 lookup (pubkey → NIP-05 identity, bidirectional verification)
GET /timeline?pubkey=<hex|npub> — Trust timeline: monthly follower growth and estimated score evolution over time
GET /spam?pubkey=<hex|npub>  — Spam detection: multi-signal analysis with classification and signal breakdown
POST /spam/batch             — Bulk spam check up to 100 pubkeys with summary counts (JSON: {"pubkeys":[...]})
GET /weboftrust?pubkey=<hex|npub> — D3.js-compatible trust graph: nodes + links for force-directed visualization
GET /blocked?pubkey=<hex|npub> — Who has this pubkey muted (their NIP-51 mute list)
GET /blocked?target=<hex|npub> — Who has muted this target (reverse lookup + community signal)
GET /providers               — External NIP-85 assertion providers and assertion counts
GET /top                     — Top 50 scored pubkeys
GET /export                  — All scores as JSON
GET /stats                   — Service stats and graph info
POST /publish                — Publish NIP-85 kind 30382/30383/30384/30385 + NIP-89 handler to relays
```

## Interactive UI

The landing page at [wot.klabo.world](https://wot.klabo.world) includes three interactive tools:

- **Score Lookup** — Enter any npub or hex pubkey to see trust score, followers, posts, reactions, zaps
- **Trust Leaderboard** — Top 10 scored pubkeys with live rank, score, and follower counts
- **Trust Communities** — Visualize detected trust clusters with member counts and top-ranked members
- **Compare** — Side-by-side comparison of two pubkeys with relationship badges (mutual follow, shared follows, trusted followers) and bar charts
- **Trust Path** — BFS shortest-path visualization showing each hop with WoT scores between any two pubkeys
- **Timeline** — Trust evolution visualization showing monthly follower growth bars with velocity coloring
- **Spam Check** — Multi-signal spam probability analysis with per-signal breakdown bars
- **Trust Graph** — Interactive force-directed SVG visualization of a pubkey's trust network (follows, followers, mutual connections)

## Run

```bash
go build -o wot-scoring .
./wot-scoring
# Listens on :8090 (override with PORT env var)
# NIP-85 publishing requires NOSTR_NSEC env var
```

Docker:

```bash
docker build -t wot-scoring .
docker run -p 8090:8090 -e NOSTR_NSEC=nsec1... wot-scoring
```

Systemd (persistent service):

```bash
sudo cp wot-scoring /usr/local/bin/
sudo cp wot-scoring.service /etc/systemd/system/
sudo mkdir -p /etc/wot-scoring
echo "NOSTR_NSEC=nsec1..." | sudo tee /etc/wot-scoring/env
sudo useradd -r -s /usr/sbin/nologin wot
sudo systemctl enable --now wot-scoring
```

## Test

```bash
go test -v ./...
```

## Numbers

Typical crawl: ~51,000 nodes, ~620,000 edges in 8-10 seconds from 4 seed pubkeys.

## NIP-85 Tags Published

Each kind 30382 event includes these standard NIP-85 tags:

| Tag | Description |
|-----|-------------|
| `d` | Subject pubkey |
| `p` | Subject pubkey (relay hint) |
| `rank` | Normalized trust score (0-100) |
| `followers` | Follower count from follow graph |
| `post_cnt` | Kind 1 notes (not replies) |
| `reply_cnt` | Kind 1 notes that are replies |
| `reactions_cnt` | Kind 7 reactions received |
| `zap_amt_recd` | Sats received via zap receipts |
| `zap_cnt_recd` | Number of zaps received |
| `zap_amt_sent` | Sats sent via zap receipts |
| `zap_cnt_sent` | Number of zaps sent |
| `first_created_at` | Earliest known event timestamp |
| `zap_avg_amt_day_recd` | Average daily sats received |
| `zap_avg_amt_day_sent` | Average daily sats sent |
| `t` | Common topics/hashtags (up to 5) |
| `active_hours_start` | Peak activity window start (UTC hour, 0-23) |
| `active_hours_end` | Peak activity window end (UTC hour, 0-23) |
| `reports_cnt_recd` | Kind 1984 reports received |
| `reports_cnt_sent` | Kind 1984 reports sent |

## Kind 30383 Tags (Event Assertions)

Each kind 30383 event scores an individual Nostr event:

| Tag | Description |
|-----|-------------|
| `d` | Event ID |
| `e` | Event ID (relay hint) |
| `p` | Author pubkey |
| `rank` | Engagement score (0-100) |
| `comments` | Reply count |
| `reposts` | Kind 6 repost count |
| `reactions` | Kind 7 reaction count |
| `zap_count` | Number of zaps received |
| `zap_amount` | Sats received via zaps |

## Kind 30384 Tags (Addressable Event Assertions)

Each kind 30384 event scores an addressable event (articles, live activities):

| Tag | Description |
|-----|-------------|
| `d` | Event address (kind:pubkey:d-tag) |
| `a` | Event address (relay hint) |
| `p` | Author pubkey |
| `rank` | Engagement score (0-100) |
| `comments` | Reply count |
| `reposts` | Repost count |
| `reactions` | Reaction count |
| `zap_count` | Number of zaps received |
| `zap_amount` | Sats received via zaps |

## Kind 30385 Tags (External Identifier Assertions)

Each kind 30385 event scores an external identifier (NIP-73 format — hashtags, URLs):

| Tag | Description |
|-----|-------------|
| `d` | NIP-73 identifier (e.g. `#bitcoin`, `https://example.com`) |
| `rank` | Engagement score (0-100) |
| `mentions` | Number of events referencing this identifier |
| `unique_authors` | Number of distinct authors who mentioned it |
| `reactions` | Aggregate reaction count |
| `reposts` | Aggregate repost count |
| `comments` | Aggregate reply count |
| `zap_count` | Number of zaps on mentioning events |
| `zap_amount` | Sats zapped on mentioning events |

## Relay Trust Assessment

The `/relay` endpoint combines infrastructure data from [trustedrelays.xyz](https://trustedrelays.xyz) with operator social reputation from our PageRank graph:

```
combined_score = infrastructure_trust * 0.70 + operator_wot_score * 0.30
```

Example response for `GET /relay?url=wss://relay.damus.io`:

```json
{
  "url": "wss://relay.damus.io",
  "name": "damus.io",
  "operator_pubkey": "32e1827...",
  "relay_trust": {
    "overall": 90,
    "reliability": 84,
    "quality": 94,
    "accessibility": 94,
    "operator_trust": 71,
    "uptime_percent": 100,
    "confidence": "high",
    "observations": 5149
  },
  "operator_wot": {
    "pubkey": "32e1827...",
    "wot_score": 18,
    "followers": 1358,
    "in_graph": true
  },
  "combined_score": 68,
  "source": "wot.klabo.world + trustedrelays.xyz"
}
```

Responses are cached for 30 minutes to respect trustedrelays.xyz rate limits (60 req/min). Unknown relays gracefully return a 0 score.

## Interoperability — External Assertion Consumption

The service **consumes NIP-85 kind 30382 events from other providers** on the relay network and blends them into a composite trust score.

- Subscribes to kind 30382 events from the last 7 days on all configured relays
- Filters out self-published assertions (only external providers are included)
- Stores per-provider, per-subject assertions with deduplication (newest wins)
- When querying `/score`, if external assertions exist for a pubkey, the response includes a `composite_score` and `external_assertions` breakdown

**Composite scoring formula:**

```
composite = (internal_score × 0.70) + (external_avg × 0.30)
```

This means clients can ask our service for a trust score that blends multiple independent WoT engines — true NIP-85 interoperability.

The `/providers` endpoint lists all discovered external NIP-85 assertion providers and their assertion counts.

## Personalized Trust Scoring

The `/personalized` endpoint scores a target pubkey relative to a viewer's follow graph — the same query Vertex claims NIP-85 can't serve. Our server handles the computation:

```
GET /personalized?viewer=<hex>&target=<hex>
```

Response:

```json
{
  "personalized_score": 55,
  "global_score": 21,
  "viewer_follows_target": true,
  "target_follows_viewer": true,
  "mutual_follow": true,
  "trusted_followers": 748,
  "shared_follows": 293,
  "trusted_follower_sample": ["32e1827...", "fa984bd..."]
}
```

**Formula:** 50% global PageRank + 50% social proximity (direct follow: +40, mutual: +10, trusted follower ratio: up to +50).

## Similar Pubkey Discovery

Find pubkeys with the most overlapping follow graphs — useful for recommendations and discovery:

```
GET /similar?pubkey=<hex|npub>&limit=20
```

Response:

```json
{
  "pubkey": "32e1827...",
  "similar": [
    {
      "pubkey": "82341f...",
      "similarity": 0.218,
      "shared_follows": 293,
      "wot_score": 21
    }
  ],
  "total_found": 20,
  "graph_size": 51446
}
```

**Algorithm:** Jaccard similarity of follow sets (|intersection| / |union|), weighted 70% similarity + 30% WoT PageRank score. Minimum 3 follows required. Max 50 results.

## Follow Recommendations

Get personalized follow recommendations — "who should this pubkey follow?" based on friends-of-friends analysis:

```
GET /recommend?pubkey=<hex|npub>&limit=20
```

Response:

```json
{
  "pubkey": "32e1827...",
  "recommendations": [
    {
      "pubkey": "82341f...",
      "mutual_follows": 45,
      "mutual_ratio": 0.293,
      "wot_score": 67
    }
  ],
  "total_found": 20,
  "follows_count": 154,
  "graph_size": 51446
}
```

**Algorithm:** For each account you follow, find who *they* follow. Count how many of your follows also follow each candidate. Exclude accounts you already follow. Rank by 60% mutual ratio + 40% WoT score. Minimum 2 mutual connections required. Max 50 results.

## Graph Explorer

Two modes for exploring trust connections in the follow graph:

### Trust Path Finder

Find the shortest connection between any two pubkeys through the follow graph:

```
GET /graph?from=<hex|npub>&to=<hex|npub>
```

Response:

```json
{
  "from": "32e1827...",
  "to": "fa984bd...",
  "found": true,
  "path": [
    {"pubkey": "32e1827...", "wot_score": 92},
    {"pubkey": "82341f...", "wot_score": 78},
    {"pubkey": "fa984bd...", "wot_score": 65}
  ],
  "hops": 2,
  "graph_size": 51446
}
```

BFS over follow edges, max depth 6. Each node in the path includes its WoT score.

### Neighborhood Graph

Get the local follow network around a pubkey — who they follow, who follows them, and mutual connections:

```
GET /graph?pubkey=<hex|npub>&depth=1&limit=50
```

Response:

```json
{
  "pubkey": "32e1827...",
  "wot_score": 92,
  "follows_count": 942,
  "followers_count": 12847,
  "mutual_count": 15,
  "neighbors": [
    {"pubkey": "82341f...", "wot_score": 78, "relation": "mutual"},
    {"pubkey": "fa984bd...", "wot_score": 65, "relation": "follows"},
    {"pubkey": "abc123...", "wot_score": 45, "relation": "follower"}
  ],
  "depth": 1,
  "graph_size": 51446
}
```

Relations: `mutual` (both follow each other), `follows` (you follow them), `follower` (they follow you), `extended` (depth=2, friends-of-friends). Depth 1 or 2, max 200 results, sorted by WoT score.

## Score Audit

Explains exactly why a pubkey has its score, breaking down all contributing factors:

```
GET /audit?pubkey=<hex|npub>
```

Response:

```json
{
  "pubkey": "32e1827...",
  "found": true,
  "final_score": 92,
  "pagerank": {
    "raw_score": 0.000245,
    "normalized_score": 92,
    "follower_count": 12847,
    "following_count": 942,
    "percentile": 0.9987,
    "rank": 7,
    "algorithm": "PageRank",
    "damping": 0.85,
    "iterations": 20,
    "normalization": "log10(raw/avg + 1) * 25, capped at 100"
  },
  "engagement": {
    "posts": 234,
    "replies": 156,
    "reactions_received": 892,
    "reactions_sent": 445,
    "zaps_received_sats": 15240,
    "zaps_received_count": 42,
    "zaps_sent_sats": 5000,
    "zaps_sent_count": 18,
    "first_event": "2022-12-03T10:30:00Z"
  },
  "top_followers": [
    {"pubkey": "82341f...", "score": 95},
    {"pubkey": "fa984bd...", "score": 78}
  ],
  "graph_context": {
    "total_nodes": 51446,
    "total_edges": 620467,
    "last_rebuild": "2026-02-09T12:30:00Z"
  }
}
```

When external NIP-85 assertions exist, the response includes a `composite` object showing the 70/30 internal/external weighting and per-provider breakdown instead of `final_score`.

## Trust Comparison

Compare two pubkeys side-by-side to understand their relationship in the Web of Trust:

```
GET /compare?a=<pubkey|npub>&b=<pubkey|npub>
```

Returns:
- **Direct relationship**: mutual, a_follows_b, b_follows_a, or none
- **Profile stats**: WoT score, rank, percentile, follows/followers count for each
- **Shared follows**: top 20 people both pubkeys follow (ranked by WoT score)
- **Shared followers**: top 20 people who follow both pubkeys (ranked by WoT score)
- **Follow similarity**: Jaccard index (0.0-1.0) of their follow sets
- **Trust path**: shortest path between the two pubkeys via BFS

## Batch Scoring

Score up to 100 pubkeys in a single request:

```
POST /batch
Content-Type: application/json
{"pubkeys": ["hex1", "hex2", "npub1..."]}
```

Returns score, composite score, and follower count per pubkey. Supports both hex and npub formats.

## NIP-89 Handler Announcement

On publish, the service also emits a kind 31990 event (NIP-89 Recommended Application Handler) announcing support for kinds 30382, 30383, 30384, and 30385. This lets Nostr clients auto-discover the service as a NIP-85 assertion provider.

## Client Integration

Any Nostr client can consume NIP-85 trust scores directly from relays — no API dependency required. Here's how to query user trust assertions using nostr-tools:

```javascript
import { SimplePool } from 'nostr-tools/pool'

const pool = new SimplePool()
const WOT_PROVIDER = '28207d114dec1046c40ad9d8f5b2d86e0e470e4c0fc35739c17679faa8df4534'
const relays = ['wss://relay.damus.io', 'wss://nos.lol', 'wss://relay.primal.net']

// Query trust score for a pubkey
async function getTrustScore(targetPubkey) {
  const events = await pool.querySync(relays, {
    kinds: [30382],
    authors: [WOT_PROVIDER],
    '#d': [targetPubkey],
  })
  if (events.length === 0) return null
  const event = events[0]
  const rank = event.tags.find(t => t[0] === 'rank')?.[1]
  const followers = event.tags.find(t => t[0] === 'followers')?.[1]
  return { score: parseInt(rank), followers: parseInt(followers) }
}

// Filter a feed by trust score
async function filterByTrust(pubkeys, minScore = 10) {
  const events = await pool.querySync(relays, {
    kinds: [30382],
    authors: [WOT_PROVIDER],
    '#d': pubkeys,
  })
  const scores = new Map()
  for (const ev of events) {
    const pk = ev.tags.find(t => t[0] === 'd')?.[1]
    const rank = parseInt(ev.tags.find(t => t[0] === 'rank')?.[1] || '0')
    if (pk) scores.set(pk, rank)
  }
  return pubkeys.filter(pk => (scores.get(pk) || 0) >= minScore)
}
```

Or use the HTTP API directly:

```bash
# Single score lookup
curl https://wot.klabo.world/score?pubkey=npub1sg6plzptd64u62a878hep2kev3zah5demn5au0ge0nf6ynlvk9qs2gpzg7

# Batch scoring (up to 100 pubkeys)
curl -X POST https://wot.klabo.world/batch \
  -H 'Content-Type: application/json' \
  -d '{"pubkeys":["82341f882b6eabcd...", "fa984bd7dbb282f0..."]}'

# Personalized trust (is this person trustworthy FROM MY perspective?)
curl "https://wot.klabo.world/personalized?viewer=MY_PUBKEY&target=THEIR_PUBKEY"
```

## Trust Decay Scoring

Time-decayed PageRank that weighs recent follows more heavily than old ones. A follow from last week contributes more to trust than one from two years ago.

```
GET /decay?pubkey=<hex|npub>&half_life=365
```

Response:

```json
{
  "pubkey": "32e1827...",
  "decay_score": 88,
  "static_score": 92,
  "delta": -4,
  "half_life_days": 365,
  "found": true,
  "follower_count": 12847,
  "followers_with_time_data": 11234,
  "oldest_follow": "2022-11-15T08:30:00Z",
  "newest_follow": "2026-02-08T14:22:00Z"
}
```

**Algorithm:** Exponential decay on PageRank edge weights. Each follow's contribution is scaled by `e^(-λ × age_days)` where `λ = ln(2) / half_life_days`. Default half-life: 365 days (a 1-year-old follow has 50% weight). Configurable via `half_life` parameter.

The `/decay/top` endpoint shows how rankings shift when freshness is factored in — who gains rank (recently followed) vs who loses rank (legacy follows fading).

## NIP-05 Identity Verification

Look up a NIP-05 identifier and get its WoT trust profile in one request — bridges Nostr identity verification with Web of Trust scoring:

```
GET /nip05?id=user@domain.com
```

Response:

```json
{
  "nip05": "user@domain.com",
  "pubkey": "32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245",
  "verified": true,
  "trust_level": "highly_trusted",
  "score": 92,
  "raw_score": 0.000245,
  "found": true,
  "graph_size": 51446,
  "followers": 12847,
  "post_count": 234,
  "reply_count": 156,
  "reactions": 892,
  "nip05_relays": ["wss://relay.damus.io"],
  "topics": ["bitcoin", "nostr", "lightning"]
}
```

**Trust levels:** `highly_trusted` (80+), `trusted` (50+), `moderate` (20+), `low` (>0), `untrusted` (0), `unknown` (not in graph).

This endpoint resolves the NIP-05 identifier via the standard `/.well-known/nostr.json` protocol, then returns the full WoT trust profile for the resolved pubkey. Useful for verifying identities before transacting, following, or trusting someone.

## Bulk NIP-05 Verification

Verify and trust-score up to 50 NIP-05 identifiers in a single request. Resolves all identifiers concurrently for fast directory-scale lookups.

```
POST /nip05/batch
Content-Type: application/json
{"identifiers": ["jb55@jb55.com", "max@klabo.world", "alice@example.com"]}
```

Response:

```json
{
  "count": 3,
  "graph_size": 51446,
  "results": [
    {
      "nip05": "jb55@jb55.com",
      "pubkey": "32e1827...",
      "verified": true,
      "trust_level": "highly_trusted",
      "score": 92,
      "found": true,
      "followers": 12847,
      "nip05_relays": ["wss://relay.damus.io"]
    },
    {
      "nip05": "max@klabo.world",
      "pubkey": "f2da534b...",
      "verified": true,
      "trust_level": "trusted",
      "score": 55,
      "found": true,
      "followers": 42
    },
    {
      "nip05": "alice@example.com",
      "error": "NIP-05 endpoint returned status 404",
      "verified": false
    }
  ]
}
```

Failed resolutions return per-item errors without failing the entire batch. Useful for clients that need to verify contact lists, organization directories, or NIP-05-heavy platforms.

## Reverse NIP-05 Lookup

Given a pubkey, find its NIP-05 identity — the inverse of standard NIP-05 resolution. Fetches the pubkey's kind 0 profile from relays, extracts the NIP-05 field, and bidirectionally verifies it resolves back to the same pubkey.

```
GET /nip05/reverse?pubkey=<hex|npub>
```

Response:

```json
{
  "pubkey": "32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245",
  "nip05": "_@jb55.com",
  "display_name": "jb55",
  "verified": true,
  "score": 92,
  "found": true,
  "graph_size": 51446,
  "followers": 12847,
  "nip05_relays": ["wss://relay.damus.io"]
}
```

If the pubkey has no kind 0 profile or no NIP-05 field set, the response still includes trust score data with `verified: false` and an error message. Useful for answering "who is this pubkey?" when you only have a hex key or npub.

## L402 Lightning Paywall

The API supports the [L402 protocol](https://docs.lightning.engineering/the-lightning-network/l402) for pay-per-query access via Lightning Network micropayments.

**Free tier:** 10 requests/day per IP on priced endpoints. No payment needed.

**Priced endpoints:**

| Endpoint | Price |
|----------|-------|
| `/score`, `/decay`, `/nip05` | 1 sat |
| `/personalized`, `/similar`, `/recommend`, `/compare`, `/nip05/reverse`, `/timeline`, `/spam`, `/blocked` | 2 sats |
| `/weboftrust` | 3 sats |
| `/audit`, `/nip05/batch` | 5 sats |
| `/batch`, `/spam/batch` | 10 sats |

All other endpoints (`/top`, `/stats`, `/health`, `/export`, `/providers`, `/graph`, `/event`, `/external`, `/relay`, `/metadata`, `/docs`, `/swagger`, `/openapi.json`) are free and unlimited.

**Interactive API documentation:** [https://wot.klabo.world/docs](https://wot.klabo.world/docs)

**Swagger UI API explorer:** [https://wot.klabo.world/swagger](https://wot.klabo.world/swagger) — interactive testing of all endpoints in the browser

**OpenAPI Spec:** [https://wot.klabo.world/openapi.json](https://wot.klabo.world/openapi.json) — machine-readable OpenAPI 3.0.3 specification for automated client generation and tool integration

**Usage flow:**

```bash
# Free tier (first 10 requests/day)
curl https://wot.klabo.world/score?pubkey=<hex>

# After free tier: returns 402 with invoice
# Response: {"status":"payment_required","invoice":"lnbc...","payment_hash":"abc123","amount_sats":1}

# Pay the invoice, then retry with payment hash
curl -H "X-Payment-Hash: abc123" https://wot.klabo.world/score?pubkey=<hex>
```

**Configuration:** Set `LNBITS_URL` and `LNBITS_KEY` environment variables to enable. Without these, the paywall is disabled and all endpoints are free.

## Built for

[WoT-a-thon](https://nosfabrica.com/wotathon/) hackathon — Web of Trust tools for Nostr.

## License

MIT
