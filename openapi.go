package main

import (
	"fmt"
	"net/http"
)

func handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	fmt.Fprint(w, openAPISpec)
}

const openAPISpec = `{
  "openapi": "3.0.3",
  "info": {
    "title": "WoT Scoring API",
    "description": "NIP-85 Trusted Assertions provider for the Nostr Web of Trust. Computes PageRank trust scores over the Nostr follow graph, publishes all five NIP-85 assertion kinds, and provides 30+ endpoints for trust analysis, spam detection, identity verification, and graph visualization. L402 Lightning micropayments for sustained access.",
    "version": "1.0.0",
    "contact": {
      "name": "Max (SATMAX Agent)",
      "email": "max@klabo.world",
      "url": "https://wot.klabo.world"
    },
    "license": {
      "name": "MIT",
      "url": "https://github.com/joelklabo/wot-scoring/blob/master/LICENSE"
    }
  },
  "servers": [
    {
      "url": "https://wot.klabo.world",
      "description": "Production"
    }
  ],
  "tags": [
    {"name": "Scoring", "description": "Trust score lookups and batch scoring"},
    {"name": "Personalized", "description": "Viewer-relative trust scoring"},
    {"name": "Graph", "description": "Trust paths, recommendations, and similarity"},
    {"name": "Identity", "description": "NIP-05 identity verification with trust profiles"},
    {"name": "Temporal", "description": "Time-decay scoring and trust timelines"},
    {"name": "Moderation", "description": "Spam detection and batch filtering"},
    {"name": "Engagement", "description": "Event and external identifier scoring"},
    {"name": "Ranking", "description": "Leaderboards, statistics, and exports"},
    {"name": "Infrastructure", "description": "Health, providers, relay trust, communities, publishing"},
    {"name": "Visualization", "description": "D3.js-compatible graph data and trust comparison"},
    {"name": "Verification", "description": "Cross-provider NIP-85 assertion verification"},
    {"name": "Sybil Resistance", "description": "Sybil detection and resistance scoring for relay operators"},
    {"name": "Trust Paths", "description": "Multi-hop trust path analysis with scoring and diversity metrics"},
    {"name": "Reputation", "description": "Composite reputation scoring combining WoT, Sybil resistance, community, and anomaly analysis"},
    {"name": "Link Prediction", "description": "Graph-theoretic link prediction for follow relationship likelihood"},
    {"name": "Real-Time", "description": "WebSocket streaming for live score updates"},
    {"name": "Network Analysis", "description": "Graph topology health metrics and network-wide analysis"},
    {"name": "Cross-Provider", "description": "Compare WoT scores across multiple NIP-85 providers for consensus analysis"},
    {"name": "Trust Circles", "description": "Mutual-follow trust circle analysis with cohesion, density, and role metrics"},
    {"name": "Follow Quality", "description": "Analyze the quality and health of a pubkey's follow list"}
  ],
  "paths": {
    "/score": {
      "get": {
        "tags": ["Scoring"],
        "operationId": "getScore",
        "summary": "Get trust score for a pubkey",
        "description": "Returns normalized PageRank trust score (0-100), composite score from external NIP-85 providers, follower count, engagement metrics, topics, active hours, and reports. Accepts hex pubkeys or NIP-19 npub format.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub"}
        ],
        "responses": {
          "200": {"description": "Trust score response", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ScoreResponse"}}}},
          "400": {"description": "Invalid or missing pubkey"},
          "402": {"description": "L402 payment required (1 sat)"}
        }
      }
    },
    "/audit": {
      "get": {
        "tags": ["Scoring"],
        "operationId": "getAudit",
        "summary": "Audit why a pubkey has its score",
        "description": "Full transparency into score breakdown: PageRank component, engagement metrics, top followers with their scores, and external assertion details.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub"}
        ],
        "responses": {
          "200": {"description": "Score audit breakdown"},
          "400": {"description": "Invalid or missing pubkey"},
          "402": {"description": "L402 payment required (5 sats)"}
        }
      }
    },
    "/batch": {
      "post": {
        "tags": ["Scoring"],
        "operationId": "batchScore",
        "summary": "Score up to 100 pubkeys in one request",
        "description": "Batch scoring for clients that need to evaluate many pubkeys at once. Returns scores, follower counts, and composite scores.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["pubkeys"],
                "properties": {
                  "pubkeys": {"type": "array", "items": {"type": "string"}, "maxItems": 100, "description": "Array of hex pubkeys or npubs"}
                }
              }
            }
          }
        },
        "responses": {
          "200": {"description": "Batch score results"},
          "400": {"description": "Invalid request body"},
          "402": {"description": "L402 payment required (10 sats)"}
        }
      }
    },
    "/personalized": {
      "get": {
        "tags": ["Personalized"],
        "operationId": "getPersonalized",
        "summary": "Personalized trust score relative to a viewer",
        "description": "Scores a target pubkey from the perspective of a specific viewer. Blends global PageRank (50%) with social proximity signals (50%): direct follow, mutual follow, and trusted follower ratio.",
        "parameters": [
          {"name": "viewer", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Viewer hex pubkey or npub"},
          {"name": "target", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Target hex pubkey or npub"}
        ],
        "responses": {
          "200": {"description": "Personalized score with social proximity breakdown"},
          "400": {"description": "Missing or invalid parameters"},
          "402": {"description": "L402 payment required (2 sats)"}
        }
      }
    },
    "/similar": {
      "get": {
        "tags": ["Graph"],
        "operationId": "getSimilar",
        "summary": "Find pubkeys with similar follow graphs",
        "description": "Jaccard similarity (70%) + WoT score (30%) to discover pubkeys with overlapping follow sets.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub"},
          {"name": "limit", "in": "query", "required": false, "schema": {"type": "integer", "default": 20, "minimum": 1, "maximum": 50}, "description": "Max results"}
        ],
        "responses": {
          "200": {"description": "Similar pubkeys with Jaccard scores"},
          "400": {"description": "Invalid or missing pubkey"},
          "402": {"description": "L402 payment required (2 sats)"}
        }
      }
    },
    "/recommend": {
      "get": {
        "tags": ["Graph"],
        "operationId": "getRecommendations",
        "summary": "Follow recommendations via friends-of-friends",
        "description": "Recommends pubkeys that many of your follows also follow, weighted by mutual follow ratio (60%) and WoT score (40%).",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub"},
          {"name": "limit", "in": "query", "required": false, "schema": {"type": "integer", "default": 20, "minimum": 1, "maximum": 50}, "description": "Max results"}
        ],
        "responses": {
          "200": {"description": "Recommended pubkeys with mutual follower counts"},
          "400": {"description": "Invalid or missing pubkey"},
          "402": {"description": "L402 payment required (2 sats)"}
        }
      }
    },
    "/graph": {
      "get": {
        "tags": ["Graph"],
        "operationId": "getTrustPath",
        "summary": "Find shortest trust path between two pubkeys",
        "description": "BFS shortest path through the follow graph (up to 6 hops). Each node annotated with WoT score. Also supports single pubkey info mode.",
        "parameters": [
          {"name": "from", "in": "query", "required": false, "schema": {"type": "string"}, "description": "Source hex pubkey or npub (for path mode)"},
          {"name": "to", "in": "query", "required": false, "schema": {"type": "string"}, "description": "Destination hex pubkey or npub (for path mode)"},
          {"name": "pubkey", "in": "query", "required": false, "schema": {"type": "string"}, "description": "Single pubkey for info mode"}
        ],
        "responses": {
          "200": {"description": "Trust path with annotated nodes"},
          "400": {"description": "Invalid parameters"}
        }
      }
    },
    "/compare": {
      "get": {
        "tags": ["Visualization"],
        "operationId": "comparePubkeys",
        "summary": "Side-by-side trust comparison of two pubkeys",
        "description": "Compares two pubkeys: scores, ranks, percentiles, direct relationship, shared follows/followers with Jaccard similarity, and trust path.",
        "parameters": [
          {"name": "a", "in": "query", "required": true, "schema": {"type": "string"}, "description": "First hex pubkey or npub"},
          {"name": "b", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Second hex pubkey or npub"}
        ],
        "responses": {
          "200": {"description": "Detailed comparison with relationship and similarity data"},
          "400": {"description": "Missing or invalid parameters"},
          "402": {"description": "L402 payment required (2 sats)"}
        }
      }
    },
    "/weboftrust": {
      "get": {
        "tags": ["Visualization"],
        "operationId": "getWebOfTrust",
        "summary": "D3.js-compatible trust graph visualization",
        "description": "Returns a force-directed graph (nodes + links) centered on a pubkey. Nodes colored by relationship type (follow, follower, mutual) and sized by WoT score.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Center hex pubkey or npub"},
          {"name": "limit", "in": "query", "required": false, "schema": {"type": "integer", "default": 50, "minimum": 1, "maximum": 200}, "description": "Max nodes per direction"}
        ],
        "responses": {
          "200": {"description": "Graph with nodes and links arrays"},
          "400": {"description": "Invalid or missing pubkey"},
          "402": {"description": "L402 payment required (3 sats)"}
        }
      }
    },
    "/nip05": {
      "get": {
        "tags": ["Identity"],
        "operationId": "resolveNIP05",
        "summary": "Resolve NIP-05 identifier to trust profile",
        "description": "Resolves a NIP-05 identifier (user@domain.com) to its pubkey via .well-known/nostr.json, then returns the full WoT trust profile including score, trust level, engagement metrics, and topics.",
        "parameters": [
          {"name": "id", "in": "query", "required": true, "schema": {"type": "string"}, "description": "NIP-05 identifier (e.g. user@domain.com)"}
        ],
        "responses": {
          "200": {"description": "Trust profile with NIP-05 verification"},
          "400": {"description": "Invalid identifier or resolution failed"},
          "402": {"description": "L402 payment required (1 sat)"}
        }
      }
    },
    "/nip05/batch": {
      "post": {
        "tags": ["Identity"],
        "operationId": "batchNIP05",
        "summary": "Resolve up to 50 NIP-05 identifiers concurrently",
        "description": "Batch NIP-05 resolution with trust profiles. Enables clients to verify and trust-score entire contact lists or directories in a single request.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["identifiers"],
                "properties": {
                  "identifiers": {"type": "array", "items": {"type": "string"}, "maxItems": 50, "description": "Array of NIP-05 identifiers"}
                }
              }
            }
          }
        },
        "responses": {
          "200": {"description": "Batch resolution results"},
          "400": {"description": "Invalid request body"},
          "402": {"description": "L402 payment required (5 sats)"}
        }
      }
    },
    "/nip05/reverse": {
      "get": {
        "tags": ["Identity"],
        "operationId": "reverseNIP05",
        "summary": "Reverse NIP-05 lookup from pubkey",
        "description": "Given a pubkey, fetches their kind 0 profile from relays, extracts the NIP-05 identifier, and bidirectionally verifies it resolves back to the same pubkey.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub"}
        ],
        "responses": {
          "200": {"description": "Reverse NIP-05 lookup result with bidirectional verification"},
          "400": {"description": "Invalid or missing pubkey"},
          "402": {"description": "L402 payment required (2 sats)"}
        }
      }
    },
    "/decay": {
      "get": {
        "tags": ["Temporal"],
        "operationId": "getDecayScore",
        "summary": "Time-decay adjusted trust score",
        "description": "Exponential decay where newer follows weigh more. Configurable half-life reveals emerging vs legacy reputation. Shows delta between static and decayed scores.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub"},
          {"name": "half_life", "in": "query", "required": false, "schema": {"type": "number", "default": 365, "minimum": 1, "maximum": 3650}, "description": "Half-life in days"}
        ],
        "responses": {
          "200": {"description": "Decay-adjusted score with static comparison"},
          "400": {"description": "Invalid or missing pubkey"},
          "402": {"description": "L402 payment required (1 sat)"}
        }
      }
    },
    "/decay/top": {
      "get": {
        "tags": ["Temporal"],
        "operationId": "getDecayTop",
        "summary": "Top pubkeys by decay-adjusted score",
        "description": "Leaderboard showing rank changes when temporal freshness is factored in. Reveals who is gaining vs losing momentum.",
        "parameters": [
          {"name": "half_life", "in": "query", "required": false, "schema": {"type": "number", "default": 365, "minimum": 1, "maximum": 3650}, "description": "Half-life in days"},
          {"name": "limit", "in": "query", "required": false, "schema": {"type": "integer", "default": 50, "minimum": 1, "maximum": 200}, "description": "Max results"}
        ],
        "responses": {
          "200": {"description": "Ranked list with decay vs static rank changes"}
        }
      }
    },
    "/timeline": {
      "get": {
        "tags": ["Temporal"],
        "operationId": "getTimeline",
        "summary": "Trust evolution timeline for a pubkey",
        "description": "Monthly time-series of follower growth, estimated trust scores, and follow velocity. Reconstructed from follow event timestamps.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub"}
        ],
        "responses": {
          "200": {"description": "Timeline with monthly data points"},
          "400": {"description": "Invalid or missing pubkey"}
        }
      }
    },
    "/spam": {
      "get": {
        "tags": ["Moderation"],
        "operationId": "checkSpam",
        "summary": "Multi-signal spam classification",
        "description": "Classifies a pubkey as likely_human, suspicious, or likely_spam using 6 weighted signals: WoT score (30%), follower ratio (15%), account age (15%), engagement (15%), reports (15%), activity pattern (10%).",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub"}
        ],
        "responses": {
          "200": {"description": "Spam analysis with signal breakdown"},
          "400": {"description": "Invalid or missing pubkey"},
          "402": {"description": "L402 payment required (2 sats)"}
        }
      }
    },
    "/spam/batch": {
      "post": {
        "tags": ["Moderation"],
        "operationId": "batchSpam",
        "summary": "Check up to 100 pubkeys for spam",
        "description": "Batch spam filtering for contact lists or relay event feeds. Returns classification and probability for each pubkey plus summary counts.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["pubkeys"],
                "properties": {
                  "pubkeys": {"type": "array", "items": {"type": "string"}, "maxItems": 100, "description": "Array of hex pubkeys or npubs"}
                }
              }
            }
          }
        },
        "responses": {
          "200": {"description": "Batch spam results with summary counts"},
          "400": {"description": "Invalid request body"},
          "402": {"description": "L402 payment required (10 sats)"}
        }
      }
    },
    "/event": {
      "get": {
        "tags": ["Engagement"],
        "operationId": "getEventScore",
        "summary": "Engagement score for a Nostr event",
        "description": "Returns engagement metrics (comments, reposts, reactions, zaps) and a normalized rank for a specific event ID.",
        "parameters": [
          {"name": "id", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Event ID (hex)"}
        ],
        "responses": {
          "200": {"description": "Event engagement metrics"},
          "400": {"description": "Missing event ID"}
        }
      }
    },
    "/external": {
      "get": {
        "tags": ["Engagement"],
        "operationId": "getExternalScore",
        "summary": "Score for external identifiers (hashtags, URLs)",
        "description": "Trust-weighted engagement scoring for NIP-73 external identifiers. Without an ID parameter, returns top 50 trending identifiers.",
        "parameters": [
          {"name": "id", "in": "query", "required": false, "schema": {"type": "string"}, "description": "External identifier (hashtag or URL). Omit for top 50 list."}
        ],
        "responses": {
          "200": {"description": "External identifier engagement data"}
        }
      }
    },
    "/metadata": {
      "get": {
        "tags": ["Scoring"],
        "operationId": "getMetadata",
        "summary": "NIP-85 metadata for a pubkey",
        "description": "Returns all collected metadata: follower count, post/reply counts, reactions, zaps, topics, active hours, reports sent/received, and account age.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub"}
        ],
        "responses": {
          "200": {"description": "Full metadata profile"},
          "400": {"description": "Invalid or missing pubkey"}
        }
      }
    },
    "/top": {
      "get": {
        "tags": ["Ranking"],
        "operationId": "getTop",
        "summary": "Top 50 pubkeys by PageRank",
        "description": "Leaderboard of the highest-ranked pubkeys in the trust graph with normalized scores and follower counts.",
        "responses": {
          "200": {"description": "Array of top-ranked pubkeys"}
        }
      }
    },
    "/stats": {
      "get": {
        "tags": ["Ranking"],
        "operationId": "getStats",
        "summary": "Service statistics",
        "description": "Graph size, edge count, algorithm parameters, relay list, rate limits, and last build timestamp.",
        "responses": {
          "200": {"description": "Service statistics"}
        }
      }
    },
    "/export": {
      "get": {
        "tags": ["Ranking"],
        "operationId": "exportScores",
        "summary": "Export all scores",
        "description": "Full export of all pubkeys with their raw PageRank scores and normalized ranks. Useful for research and analysis.",
        "responses": {
          "200": {"description": "Array of all scored pubkeys"}
        }
      }
    },
    "/relay": {
      "get": {
        "tags": ["Infrastructure"],
        "operationId": "getRelayTrust",
        "summary": "Trust assessment for a Nostr relay",
        "description": "Combines infrastructure trust data from trustedrelays.xyz (reliability, quality, uptime) with operator social reputation from PageRank. 70/30 blend of infrastructure and social scores.",
        "parameters": [
          {"name": "url", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Relay WebSocket URL (e.g. wss://relay.damus.io)"}
        ],
        "responses": {
          "200": {"description": "Relay trust assessment with infrastructure and social scores"},
          "400": {"description": "Missing relay URL"}
        }
      }
    },
    "/communities": {
      "get": {
        "tags": ["Infrastructure"],
        "operationId": "getCommunities",
        "summary": "Trust communities via label propagation",
        "description": "Without a pubkey, returns top 20 communities. With a pubkey, returns the community that pubkey belongs to with top members.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": false, "schema": {"type": "string"}, "description": "Hex pubkey or npub (optional — omit for top communities)"}
        ],
        "responses": {
          "200": {"description": "Community data"},
          "404": {"description": "Pubkey not found in community graph"}
        }
      }
    },
    "/authorized": {
      "get": {
        "tags": ["Infrastructure"],
        "operationId": "getAuthorized",
        "summary": "NIP-85 authorization tracking",
        "description": "Shows which users have explicitly authorized a specific NIP-85 scoring provider via kind 10040 events. Without a pubkey, shows our own authorized users.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": false, "schema": {"type": "string"}, "description": "Provider pubkey (optional — defaults to this service)"}
        ],
        "responses": {
          "200": {"description": "Authorized users with scores"}
        }
      }
    },
    "/providers": {
      "get": {
        "tags": ["Infrastructure"],
        "operationId": "getProviders",
        "summary": "External NIP-85 providers",
        "description": "Lists external NIP-85 providers whose kind 30382 assertions are consumed for composite scoring.",
        "responses": {
          "200": {"description": "Provider list with assertion counts"}
        }
      }
    },
    "/publish": {
      "post": {
        "tags": ["Infrastructure"],
        "operationId": "publishAssertions",
        "summary": "Publish all NIP-85 assertions to relays",
        "description": "Triggers publication of all five NIP-85 assertion kinds (30382, 30383, 30384, 30385) plus NIP-89 handler announcement to configured relays.",
        "responses": {
          "200": {"description": "Publication counts per kind"},
          "405": {"description": "POST required"},
          "503": {"description": "Graph not built yet"}
        }
      }
    },
    "/health": {
      "get": {
        "tags": ["Infrastructure"],
        "operationId": "getHealth",
        "summary": "Health check",
        "description": "Returns service status (starting/ready), graph size, event counts, external provider stats, authorization counts, and uptime.",
        "responses": {
          "200": {"description": "Health status"}
        }
      }
    },
    "/blocked": {
      "get": {
        "tags": ["Trust Analysis"],
        "operationId": "getBlocked",
        "summary": "Mute list analysis (NIP-51 kind 10000)",
        "description": "Two modes: (1) pubkey mode returns who a pubkey has muted, (2) target mode returns who has muted a target pubkey with community moderation signal strength. Integrates NIP-51 mute lists with WoT trust scores.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": false, "schema": {"type": "string"}, "description": "Hex pubkey or npub — returns their mute list"},
          {"name": "target", "in": "query", "required": false, "schema": {"type": "string"}, "description": "Hex pubkey or npub — returns who has muted this target"}
        ],
        "responses": {
          "200": {"description": "Mute analysis with WoT scores and community signal"},
          "400": {"description": "Missing or invalid pubkey/target"},
          "402": {"description": "L402 payment required (2 sats)"}
        }
      }
    },
    "/verify": {
      "post": {
        "tags": ["Verification"],
        "operationId": "verifyAssertion",
        "summary": "Verify a NIP-85 assertion from any provider",
        "description": "Accepts a NIP-85 kind 30382 event (JSON) and cross-checks it against our own graph data. Verifies cryptographic signature, then compares claimed rank and follower count against our observations. Returns a verdict: consistent (claims match), divergent (claims don't match), unverifiable (no verifiable claims), or invalid (bad signature/structure). Enables multi-provider trust verification.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "description": "A Nostr event (kind 30382) with id, pubkey, sig, tags, etc.",
                "properties": {
                  "id": {"type": "string"},
                  "pubkey": {"type": "string"},
                  "created_at": {"type": "integer"},
                  "kind": {"type": "integer", "enum": [30382]},
                  "tags": {"type": "array", "items": {"type": "array", "items": {"type": "string"}}},
                  "content": {"type": "string"},
                  "sig": {"type": "string"}
                }
              }
            }
          }
        },
        "responses": {
          "200": {"description": "Verification result with per-field checks and overall verdict"},
          "400": {"description": "Invalid JSON or wrong event kind"},
          "402": {"description": "L402 payment required (2 sats)"},
          "405": {"description": "Method not allowed (POST required)"}
        }
      }
    },
    "/anomalies": {
      "get": {
        "tags": ["Trust Analysis"],
        "operationId": "getAnomalies",
        "summary": "Trust anomaly detection for a pubkey",
        "description": "Analyzes a pubkey's trust graph for anomalous patterns: follow-farming (high follow-back ratio), ghost/bot followers (zero-score followers), trust concentration (single-source dependency), score-follower divergence (many followers but low PageRank), and excessive following. Returns individual anomaly flags with severity levels and an overall risk assessment.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub to analyze"}
        ],
        "responses": {
          "200": {"description": "Anomaly analysis with risk level and individual flags"},
          "400": {"description": "Missing or invalid pubkey"},
          "402": {"description": "L402 payment required (3 sats)"}
        }
      }
    },
    "/sybil": {
      "get": {
        "tags": ["Sybil Resistance"],
        "operationId": "getSybilScore",
        "summary": "Sybil resistance score for a pubkey",
        "description": "Computes a Sybil resistance score (0-100) by analyzing five signals: follower quality (average WoT score of followers), mutual trust (organic bidirectional relationships), score-rank consistency (PageRank vs follower count alignment), follower diversity (neighborhood spread), and account substance (overall activity). Returns a classification (genuine, likely_genuine, suspicious, likely_sybil), confidence level, and full signal breakdown. Designed for relay operators to gate access or filter content.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub to analyze"}
        ],
        "responses": {
          "200": {"description": "Sybil resistance analysis with score, classification, and signal breakdown"},
          "400": {"description": "Missing or invalid pubkey"},
          "402": {"description": "L402 payment required (3 sats)"}
        }
      }
    },
    "/sybil/batch": {
      "post": {
        "tags": ["Sybil Resistance"],
        "operationId": "batchSybilScore",
        "summary": "Batch Sybil resistance scoring for up to 50 pubkeys",
        "description": "Scores multiple pubkeys for Sybil resistance in one request. Uses a simplified scoring model for performance. Results sorted by sybil_score ascending (most suspicious first). Useful for relay operators filtering event streams.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["pubkeys"],
                "properties": {
                  "pubkeys": {"type": "array", "items": {"type": "string"}, "maxItems": 50, "description": "Array of hex pubkeys or npubs"}
                }
              }
            }
          }
        },
        "responses": {
          "200": {"description": "Array of Sybil scores sorted by suspicion level"},
          "400": {"description": "Invalid JSON or missing pubkeys"},
          "402": {"description": "L402 payment required (10 sats)"},
          "405": {"description": "Method not allowed (POST required)"}
        }
      }
    },
    "/trust-path": {
      "get": {
        "tags": ["Trust Paths"],
        "operationId": "getMultiHopTrustPath",
        "summary": "Multi-hop trust path analysis between two pubkeys",
        "description": "Finds and scores multiple trust paths between two pubkeys through the follow graph. Computes trust attenuation per hop (product of normalized WoT scores with mutual-follow bonus), identifies weakest links, and combines independent paths for an overall trust assessment. Useful for determining how two accounts are connected through mutual trust relationships.",
        "parameters": [
          {"name": "from", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Source hex pubkey or npub"},
          {"name": "to", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Target hex pubkey or npub"},
          {"name": "max_paths", "in": "query", "required": false, "schema": {"type": "integer", "default": 3, "minimum": 1, "maximum": 5}, "description": "Maximum number of distinct paths to find (1-5, default 3)"}
        ],
        "responses": {
          "200": {"description": "Trust path analysis with scored paths, diversity metrics, and classification"},
          "400": {"description": "Missing or invalid pubkeys"},
          "402": {"description": "L402 payment required (5 sats)"}
        }
      }
    },
    "/reputation": {
      "get": {
        "tags": ["Reputation"],
        "operationId": "getReputation",
        "summary": "Comprehensive reputation profile for a pubkey",
        "description": "Computes a composite reputation score (0-100, grade A-F) by combining five dimensions: WoT standing (PageRank percentile), Sybil resistance (follower quality and mutual trust), community integration (cluster membership and quality), anomaly cleanliness (absence of trust manipulation flags), and network diversity (follower spread across graph regions). Returns a detailed breakdown with per-component scores, grades, and a human-readable summary.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub to analyze"}
        ],
        "responses": {
          "200": {"description": "Reputation profile with composite score, grade, component breakdown, and summary"},
          "400": {"description": "Missing or invalid pubkey"},
          "402": {"description": "L402 payment required (5 sats)"}
        }
      }
    },
    "/predict": {
      "get": {
        "tags": ["Link Prediction"],
        "operationId": "predictLink",
        "summary": "Predict whether a follow relationship will form between two pubkeys",
        "description": "Uses five graph-theoretic link prediction signals (Common Neighbors, Adamic-Adar Index, Preferential Attachment, Jaccard Coefficient, WoT Score Proximity) to estimate the likelihood of a follow relationship forming. Returns a prediction score (0-1), confidence, classification, per-signal breakdown, and top mutual connections.",
        "parameters": [
          {"name": "source", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Source hex pubkey or npub"},
          {"name": "target", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Target hex pubkey or npub"}
        ],
        "responses": {
          "200": {"description": "Link prediction with signal breakdown and mutual connections"},
          "400": {"description": "Missing, invalid, or identical pubkeys"},
          "402": {"description": "L402 payment required (3 sats)"}
        }
      }
    },
    "/influence": {
      "get": {
        "tags": ["Influence Analysis"],
        "operationId": "simulateInfluence",
        "summary": "Simulate how a follow/unfollow would ripple through PageRank scores",
        "description": "Performs differential PageRank analysis: computes current scores vs. hypothetical scores after a simulated graph change. Shows which pubkeys would be most affected and by how much. Useful for understanding the cascading impact of a single follow/unfollow on the trust network.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "The pubkey being followed/unfollowed (hex or npub)"},
          {"name": "other", "in": "query", "required": true, "schema": {"type": "string"}, "description": "The pubkey performing the follow/unfollow action (hex or npub)"},
          {"name": "action", "in": "query", "required": false, "schema": {"type": "string", "enum": ["follow", "unfollow"], "default": "follow"}, "description": "The simulated action (default: follow)"}
        ],
        "responses": {
          "200": {"description": "Influence propagation analysis with affected pubkeys and score deltas"},
          "400": {"description": "Missing, invalid, or identical pubkeys, or invalid action"},
          "402": {"description": "L402 payment required (5 sats)"}
        }
      }
    },
    "/influence/batch": {
      "post": {
        "tags": ["Influence Analysis"],
        "operationId": "batchInfluenceAnalysis",
        "summary": "Batch static influence analysis for multiple pubkeys",
        "description": "Analyzes up to 50 pubkeys in a single request, returning each one's trust score, percentile rank, follower metrics, mutual connections, 2-hop reach estimate, and network role classification (hub, authority, connector, consumer, observer, participant, isolated). Results sorted by trust score descending. No simulation — uses pre-computed PageRank for fast O(1) per-pubkey lookups.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["pubkeys"],
                "properties": {
                  "pubkeys": {
                    "type": "array",
                    "items": {"type": "string"},
                    "maxItems": 50,
                    "description": "Array of hex pubkeys or npub identifiers"
                  }
                }
              }
            }
          }
        },
        "responses": {
          "200": {"description": "Batch influence results with per-pubkey metrics and role classifications"},
          "400": {"description": "Invalid JSON, empty pubkeys array, or exceeds 50 limit"},
          "402": {"description": "L402 payment required (10 sats)"},
          "405": {"description": "Method not allowed (POST required)"}
        }
      }
    },
    "/network-health": {
      "get": {
        "tags": ["Network Analysis"],
        "operationId": "getNetworkHealth",
        "summary": "Comprehensive network topology health analysis",
        "description": "Computes graph-theoretic health metrics: degree distribution, connectivity, reciprocity, Gini coefficient of score centralization, power-law exponent, and top hubs. Returns an overall health score (0-100) and classification.",
        "responses": {
          "200": {"description": "Network health metrics including connectivity, degree stats, score distribution, top hubs, and health classification"},
          "402": {"description": "L402 payment required (5 sats)"},
          "503": {"description": "Graph not built yet"}
        }
      }
    },
    "/compare-providers": {
      "get": {
        "tags": ["Cross-Provider"],
        "operationId": "compareProviders",
        "summary": "Compare WoT scores across NIP-85 providers",
        "description": "Returns trust scores for a pubkey from our engine and all known external NIP-85 providers. Includes consensus metrics (mean, median, standard deviation, agreement level). Demonstrates NIP-85 interoperability — different providers independently scoring the same pubkey.",
        "parameters": [
          {
            "name": "pubkey",
            "in": "query",
            "required": true,
            "description": "Hex pubkey or npub",
            "schema": {"type": "string"}
          }
        ],
        "responses": {
          "200": {"description": "Cross-provider score comparison with consensus metrics"},
          "400": {"description": "Missing or invalid pubkey"},
          "402": {"description": "L402 payment required (5 sats)"}
        }
      }
    },
    "/ws/scores": {
      "get": {
        "tags": ["Real-Time"],
        "operationId": "wsScores",
        "summary": "Real-time score streaming via WebSocket",
        "description": "WebSocket endpoint for live score updates. Connect, subscribe to pubkeys, receive current scores immediately then updates after each graph recomputation (~6h). Protocol: send {type:subscribe,pubkeys:[...]} to watch up to 100 pubkeys. Without WebSocket upgrade, returns endpoint documentation as JSON.",
        "responses": {
          "101": {"description": "WebSocket upgrade successful"},
          "200": {"description": "Endpoint documentation (non-WebSocket request)"}
        }
      }
    },
    "/trust-circle": {
      "get": {
        "tags": ["Trust Circles"],
        "operationId": "getTrustCircle",
        "summary": "Analyze a pubkey's mutual-follow trust circle",
        "description": "Returns the trust circle (mutual follows) for a pubkey with per-member scoring, shared follow counts, mutual strength metrics, and aggregate circle analytics including cohesion, density, and role distribution. The inner circle highlights the top 10 most-trusted mutual connections.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub to analyze"}
        ],
        "responses": {
          "200": {"description": "Trust circle analysis with members, inner circle, and metrics"},
          "400": {"description": "Missing or invalid pubkey"},
          "402": {"description": "L402 payment required (5 sats)"}
        }
      }
    },
    "/trust-circle/compare": {
      "get": {
        "tags": ["Trust Circles"],
        "operationId": "compareTrustCircles",
        "summary": "Compare two pubkeys' trust circles",
        "description": "Compares the trust circles (mutual follows) of two pubkeys. Returns overlapping members (in both circles), unique members (in only one), and a compatibility score (0-100) based on circle overlap ratio, shared follow ratio, and average WoT score of overlapping members. Useful for Nostr clients to show 'how compatible are these two users?' or 'who do we both trust?'",
        "parameters": [
          {"name": "pubkey1", "in": "query", "required": true, "schema": {"type": "string"}, "description": "First hex pubkey or npub"},
          {"name": "pubkey2", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Second hex pubkey or npub"}
        ],
        "responses": {
          "200": {"description": "Circle comparison with overlap, unique members, and compatibility score"},
          "400": {"description": "Missing, invalid, or identical pubkeys"},
          "402": {"description": "L402 payment required (5 sats)"}
        }
      }
    },
    "/follow-quality": {
      "get": {
        "tags": ["Follow Quality"],
        "operationId": "getFollowQuality",
        "summary": "Analyze the quality of a pubkey's follow list",
        "description": "Evaluates the quality of who a pubkey follows, scoring each follow by trust score and categorizing into tiers (strong/moderate/weak/unknown). Returns an overall quality score (0-100), breakdown metrics (avg trust, reciprocity, diversity, signal ratio), category counts, and suggestions for low-quality follows to reconsider. Useful for Nostr clients to help users curate healthier follow lists.",
        "parameters": [
          {"name": "pubkey", "in": "query", "required": true, "schema": {"type": "string"}, "description": "Hex pubkey or npub to analyze"},
          {"name": "suggestions", "in": "query", "required": false, "schema": {"type": "integer", "default": 10, "minimum": 0, "maximum": 50}, "description": "Max number of low-quality follow suggestions to return"}
        ],
        "responses": {
          "200": {"description": "Follow quality analysis with breakdown, categories, and suggestions"},
          "400": {"description": "Missing or invalid pubkey"},
          "402": {"description": "L402 payment required (5 sats)"}
        }
      }
    },
    "/docs": {
      "get": {
        "tags": ["Infrastructure"],
        "operationId": "getDocs",
        "summary": "Interactive API documentation",
        "description": "HTML page with endpoint cards, request/response examples, and live Try-it buttons.",
        "responses": {
          "200": {"description": "HTML documentation page", "content": {"text/html": {}}}
        }
      }
    },
    "/swagger": {
      "get": {
        "tags": ["Infrastructure"],
        "operationId": "getSwagger",
        "summary": "Swagger UI API explorer",
        "description": "Interactive API explorer powered by Swagger UI. Try any endpoint directly in the browser.",
        "responses": {
          "200": {"description": "HTML Swagger UI page", "content": {"text/html": {}}}
        }
      }
    },
    "/openapi.json": {
      "get": {
        "tags": ["Infrastructure"],
        "operationId": "getOpenAPI",
        "summary": "OpenAPI 3.0 specification",
        "description": "Machine-readable API specification in OpenAPI 3.0.3 format.",
        "responses": {
          "200": {"description": "OpenAPI JSON spec", "content": {"application/json": {}}}
        }
      }
    }
  },
  "components": {
    "schemas": {
      "ScoreResponse": {
        "type": "object",
        "properties": {
          "pubkey": {"type": "string", "description": "Hex pubkey"},
          "score": {"type": "integer", "description": "Normalized score (0-100)"},
          "raw_score": {"type": "number", "description": "Raw PageRank value"},
          "found": {"type": "boolean", "description": "Whether pubkey exists in graph"},
          "graph_size": {"type": "integer", "description": "Total nodes in graph"},
          "followers": {"type": "integer"},
          "post_count": {"type": "integer"},
          "reply_count": {"type": "integer"},
          "reactions": {"type": "integer"},
          "zap_amount": {"type": "integer"},
          "zap_count": {"type": "integer"},
          "composite_score": {"type": "number", "description": "Blended score from multiple NIP-85 providers"},
          "topics": {"type": "array", "items": {"type": "string"}, "description": "Top hashtag topics"},
          "active_hours_start": {"type": "integer", "description": "Most active hour (UTC)"},
          "active_hours_end": {"type": "integer", "description": "End of active window (UTC)"},
          "reports_received": {"type": "integer"},
          "reports_sent": {"type": "integer"}
        }
      },
      "Error": {
        "type": "object",
        "properties": {
          "error": {"type": "string", "description": "Error message"}
        }
      }
    },
    "securitySchemes": {
      "L402": {
        "type": "apiKey",
        "in": "header",
        "name": "X-Payment-Hash",
        "description": "L402 Lightning payment. First 10 requests/day per IP are free. After that, the API returns HTTP 402 with a Lightning invoice. Pay the invoice and retry with the payment hash in this header."
      }
    }
  }
}`
