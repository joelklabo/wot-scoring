# WoT Scoring — Impact Statement

## The Problem

Nostr has no built-in way to assess how trustworthy a pubkey is. Every client shows the same profile information whether someone has 50,000 followers or was created yesterday. Spam and impersonation are filtered (or not) per-client with custom heuristics that don't share data.

NIP-85 defines a standard for trust attestations — kind 30382 events that allow scoring services to publish machine-readable trust data. But there are almost no implementations publishing these events today.

## What This Tool Does

WoT Scoring crawls the Nostr follow graph (kind 3 events) from public relays, builds a directed graph of follow relationships, and computes PageRank scores over the network. It then publishes NIP-85 kind 30382 events containing normalized trust scores (0-100) for the top-scored pubkeys.

The result: any Nostr client can look up a pubkey's trust score by querying for kind 30382 events from a known scoring service, without running the computation locally.

## Scale

A single run crawls ~51,000 nodes and ~620,000 edges from 3 relays in under 10 seconds. The top-scored accounts match intuition: jack dorsey, jb55, pablo, and other well-known, well-connected Nostr users rank highest.

## Why It Matters

1. **NIP-85 needs working implementations.** Standards without running code don't get adopted. This is a working reference implementation.

2. **Spam filtering gets better with shared trust data.** A client that checks NIP-85 attestations before rendering a note can silently deprioritize unknown/untrusted pubkeys without maintaining its own follow-graph crawler.

3. **WoT is composable.** Once multiple scoring services publish kind 30382 events (each with different algorithms, seed sets, or trust models), clients can aggregate across them or choose which to trust. The protocol enables a marketplace of trust providers rather than a single centralized authority.

## Source Code

https://github.com/joelklabo/wot-scoring

MIT licensed. Single-file Go binary with no dependencies beyond go-nostr.
