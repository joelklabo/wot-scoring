# WoT Scoring — Nostr Web of Trust

**Live:** [wot.klabo.world](https://wot.klabo.world)

[![CI](https://github.com/joelklabo/wot-scoring/actions/workflows/ci.yml/badge.svg)](https://github.com/joelklabo/wot-scoring/actions/workflows/ci.yml)

NIP-85 Trusted Assertions provider. Crawls the Nostr follow graph, computes PageRank trust scores, collects per-pubkey, per-event, and per-identifier engagement metadata, publishes kind 30382/30383/30384/30385 events to relays, enriches relay data from trustedrelays.xyz with social reputation, and **consumes assertions from external NIP-85 providers** for composite trust scoring. Auto re-crawls every 6 hours.

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
13. Re-crawls automatically every 6 hours

## API

Live at https://wot.klabo.world — try any endpoint below.

```
GET /                        — Service info and endpoint list
GET /health                  — Health check (status, graph size, uptime)
GET /score?pubkey=<hex>      — Trust score for a pubkey (kind 30382) + composite from external providers
GET /personalized?viewer=<hex>&target=<hex> — Personalized trust score relative to viewer's follow graph
POST /batch                  — Score up to 100 pubkeys in one request (JSON: {"pubkeys":[...]})
GET /similar?pubkey=<hex>    — Find similar pubkeys by follow-graph overlap
GET /recommend?pubkey=<hex>  — Follow recommendations (friends-of-friends)
GET /metadata?pubkey=<hex>   — Full NIP-85 metadata (followers, posts, reactions, zaps)
GET /event?id=<hex>          — Event engagement score (kind 30383)
GET /external?id=<ident>     — External identifier score (kind 30385, NIP-73)
GET /external                — Top 50 external identifiers (hashtags, URLs)
GET /relay?url=<wss://...>   — Relay trust + operator WoT (via trustedrelays.xyz)
GET /providers               — External NIP-85 assertion providers and assertion counts
GET /top                     — Top 50 scored pubkeys
GET /export                  — All scores as JSON
GET /stats                   — Service stats and graph info
POST /publish                — Publish NIP-85 kind 30382/30383/30384/30385 + NIP-89 handler to relays
```

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

## Built for

[WoT-a-thon](https://nosfabrica.com/wotathon/) hackathon — Web of Trust tools for Nostr.

## License

MIT
