# WoT Scoring — Impact Statement

## The Problem

Nostr has no built-in way to assess how trustworthy a pubkey is. Every client shows the same profile information whether someone has 50,000 followers or was created yesterday. Spam and impersonation are filtered (or not) per-client with custom heuristics that don't share data.

NIP-85 defines a standard for trust attestations — three kinds of events that allow scoring services to publish machine-readable trust data. But there are almost no implementations publishing these events today.

## What This Tool Does

WoT Scoring implements all four NIP-85 assertion kinds:

**Kind 30382 — User Assertions.** Crawls the Nostr follow graph (kind 3 events) from public relays, builds a directed graph, computes PageRank, and publishes per-pubkey trust scores. Each event includes 12 tags: normalized rank (0-100), follower count, post count, reply count, reactions received, zap amounts sent/received, zap counts sent/received, and the earliest known event timestamp.

**Kind 30383 — Event Assertions.** Crawls engagement data (comments, reposts, reactions, zaps) for individual Nostr events by top-scored pubkeys. Publishes per-event engagement scores so clients can surface high-quality content without computing engagement locally.

**Kind 30384 — Addressable Event Assertions.** Same engagement scoring applied to long-form articles (kind 30023) and live activities (kind 30311). Enables clients to rank articles and streams by community engagement.

**Kind 30385 — External Identifier Assertions (NIP-73).** Crawls hashtags (from t-tags) and URLs (from r-tags) shared by high-WoT pubkeys and scores them by aggregate engagement. Publishes per-identifier scores with metrics like mention count, unique author count, reactions, reposts, comments, and zap amounts. This lets clients surface trending topics and high-quality external resources as ranked by the trust graph.

The result: any Nostr client can look up trust scores for pubkeys, events, articles, and external content by querying NIP-85 events from a known scoring service, without running the computation locally.

## Scale

A single crawl covers ~51,000 nodes and ~620,000 edges from 3 relays in under 10 seconds. Event engagement data is crawled for the top 500 pubkeys. The service auto re-crawls every 6 hours to keep data fresh.

The top-scored accounts match intuition: jack dorsey, jb55, pablo, and other well-known, well-connected Nostr users rank highest.

## Why It Matters

1. **Complete NIP-85 implementation.** This is the only project we're aware of that publishes all four NIP-85 assertion kinds (30382, 30383, 30384, 30385). NIP-85 was merged as PR #1534 on January 22, 2026 — this implementation is already compliant with the full merged spec, including NIP-73 external identifier support.

2. **Spam filtering gets better with shared trust data.** A client that checks NIP-85 attestations before rendering a note can silently deprioritize unknown/untrusted pubkeys without maintaining its own follow-graph crawler.

3. **Content quality signals.** Kind 30383 and 30384 events let clients sort and filter notes and articles by engagement, separate from author trust. A highly-engaged post from a low-ranked author can still surface.

4. **WoT is composable.** Once multiple scoring services publish NIP-85 events (each with different algorithms, seed sets, or trust models), clients can aggregate across them or choose which to trust. The protocol enables a marketplace of trust providers rather than a single centralized authority.

## Technical Details

- **Algorithm**: PageRank (20 iterations, 0.85 damping factor) over the kind-3 follow graph
- **Engagement scoring**: Weighted formula (reactions×1 + reposts×2 + comments×3 + zap_amount), log-scale normalized to 0-100
- **Language**: Go, single binary, only dependency is go-nostr
- **Relays**: relay.damus.io, nos.lol, relay.primal.net
- **CI**: GitHub Actions (go vet, go test -race, go build)
- **Deployment**: Docker support included

## Source Code

https://github.com/joelklabo/wot-scoring

MIT licensed.
