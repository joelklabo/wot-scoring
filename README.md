# WoT Scoring — Nostr Web of Trust

PageRank-based trust scoring over the Nostr follow graph. Crawls kind 3 (contact list) events from public relays, builds a directed graph, and computes trust scores.

## What it does

1. Starts from seed pubkeys and crawls follows (depth 2)
2. Builds a directed graph of follow relationships
3. Computes PageRank over the graph
4. Serves scores via HTTP API
5. Publishes NIP-85 kind 30382 trust attestation events to Nostr relays

## API

```
GET /score?pubkey=<hex>   — Trust score for a pubkey (0-100 normalized + raw PageRank)
GET /top                  — Top 50 scored pubkeys
GET /stats                — Graph stats (nodes, edges, algorithm params)
POST /publish             — Publish NIP-85 kind 30382 events for top-scored pubkeys
```

## Run

```bash
go build -o wot-scoring .
./wot-scoring
# Listens on :8090 (override with PORT env var)
# NIP-85 publishing requires NOSTR_NSEC env var
```

## Numbers

Typical crawl: ~51,000 nodes, ~620,000 edges in 8-10 seconds from 4 seed pubkeys.

## NIP-85

The `/publish` endpoint signs and publishes kind 30382 events to relays. Each event contains:
- `d` tag: subject pubkey
- `rank` tag: normalized score (0-100)
- `pagerank_raw` tag: raw PageRank value
- `algorithm`, `graph_nodes`, `graph_edges` metadata tags

## Built for

[WoT-a-thon](https://nosfabrica.com/wot-a-thon) hackathon — Web of Trust tools for Nostr.

## License

MIT
