# WoT Scoring — Nostr Web of Trust

NIP-85 Trusted Assertions provider. Crawls the Nostr follow graph, computes PageRank trust scores, collects per-pubkey metadata, and publishes kind 30382 events to relays.

## What it does

1. Crawls kind 3 (contact list) events from seed pubkeys (depth 2)
2. Builds a directed graph of follow relationships
3. Computes PageRank over the graph
4. Crawls kind 1 (notes), kind 7 (reactions), and kind 9735 (zap receipts) for metadata
5. Serves scores and metadata via HTTP API
6. Publishes NIP-85 kind 30382 Trusted Assertion events to Nostr relays

## API

```
GET /                        — Service info and endpoint list
GET /score?pubkey=<hex>      — Trust score for a pubkey (0-100 normalized + raw PageRank)
GET /metadata?pubkey=<hex>   — Full NIP-85 metadata (followers, posts, reactions, zaps)
GET /top                     — Top 50 scored pubkeys
GET /export                  — All scores as JSON
GET /stats                   — Service stats and graph info
POST /publish                — Publish NIP-85 kind 30382 events to relays
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

## Built for

[WoT-a-thon](https://nosfabrica.com/wotathon/) hackathon — Web of Trust tools for Nostr.

## License

MIT
