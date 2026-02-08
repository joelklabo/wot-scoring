# WoT Scoring — Nostr Web of Trust

[![CI](https://github.com/joelklabo/wot-scoring/actions/workflows/ci.yml/badge.svg)](https://github.com/joelklabo/wot-scoring/actions/workflows/ci.yml)

NIP-85 Trusted Assertions provider. Crawls the Nostr follow graph, computes PageRank trust scores, collects per-pubkey and per-event engagement metadata, and publishes kind 30382/30383/30384 events to relays. Auto re-crawls every 6 hours.

## What it does

1. Crawls kind 3 (contact list) events from seed pubkeys (depth 2)
2. Builds a directed graph of follow relationships
3. Computes PageRank over the graph
4. Crawls kind 1 (notes), kind 7 (reactions), and kind 9735 (zap receipts) for user metadata
5. Crawls event engagement (comments, reposts, reactions, zaps) for kind 30383/30384
6. Crawls addressable events (kind 30023 long-form, kind 30311 live activities) for kind 30384
7. Serves scores and metadata via HTTP API
8. Publishes all three NIP-85 assertion kinds to Nostr relays
9. Re-crawls automatically every 6 hours

## API

```
GET /                        — Service info and endpoint list
GET /score?pubkey=<hex>      — Trust score for a pubkey (kind 30382)
GET /metadata?pubkey=<hex>   — Full NIP-85 metadata (followers, posts, reactions, zaps)
GET /event?id=<hex>          — Event engagement score (kind 30383)
GET /top                     — Top 50 scored pubkeys
GET /export                  — All scores as JSON
GET /stats                   — Service stats and graph info
POST /publish                — Publish NIP-85 kind 30382/30383/30384 events to relays
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

## Built for

[WoT-a-thon](https://nosfabrica.com/wotathon/) hackathon — Web of Trust tools for Nostr.

## License

MIT
