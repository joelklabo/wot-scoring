package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// WSMessage is the envelope for all WebSocket messages.
type WSMessage struct {
	Type    string          `json:"type"`
	Pubkeys []string        `json:"pubkeys,omitempty"`
	Scores  []WSScoreEntry  `json:"scores,omitempty"`
	Error   string          `json:"error,omitempty"`
	Stats   *WSStats        `json:"stats,omitempty"`
}

// WSScoreEntry is a score update for a single pubkey.
type WSScoreEntry struct {
	Pubkey     string  `json:"pubkey"`
	Score      int     `json:"score"`
	RawScore   float64 `json:"raw_score"`
	Percentile float64 `json:"percentile"`
	Rank       int     `json:"rank"`
}

// WSStats accompanies score updates with graph-level context.
type WSStats struct {
	Nodes     int       `json:"nodes"`
	Edges     int       `json:"edges"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WSClient represents a connected WebSocket client.
type WSClient struct {
	conn    *websocket.Conn
	pubkeys map[string]bool // subscribed pubkeys
	mu      sync.Mutex
	cancel  context.CancelFunc
}

// WSHub manages all active WebSocket clients.
type WSHub struct {
	mu      sync.Mutex
	clients map[*WSClient]bool
	graph   *Graph
}

// NewWSHub creates a new WebSocket hub.
func NewWSHub(g *Graph) *WSHub {
	return &WSHub{
		clients: make(map[*WSClient]bool),
		graph:   g,
	}
}

// Register adds a client to the hub.
func (h *WSHub) Register(c *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
}

// Unregister removes a client from the hub.
func (h *WSHub) Unregister(c *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// ClientCount returns the number of connected clients.
func (h *WSHub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// BroadcastScoreUpdate pushes updated scores to all clients for their subscribed pubkeys.
// Call this after each PageRank recompute.
func (h *WSHub) BroadcastScoreUpdate() {
	h.mu.Lock()
	clients := make([]*WSClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	if len(clients) == 0 {
		return
	}

	stats := h.graph.Stats()
	wsStats := &WSStats{
		Nodes:     stats.Nodes,
		Edges:     stats.Edges,
		UpdatedAt: time.Now(),
	}

	for _, client := range clients {
		client.mu.Lock()
		pubkeys := make([]string, 0, len(client.pubkeys))
		for pk := range client.pubkeys {
			pubkeys = append(pubkeys, pk)
		}
		client.mu.Unlock()

		if len(pubkeys) == 0 {
			continue
		}

		scores := h.lookupScores(pubkeys)
		msg := WSMessage{
			Type:   "update",
			Scores: scores,
			Stats:  wsStats,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := wsjson.Write(ctx, client.conn, msg)
		cancel()
		if err != nil {
			log.Printf("ws: failed to send update to client: %v", err)
			client.cancel()
		}
	}

	log.Printf("ws: broadcast score update to %d clients", len(clients))
}

// lookupScores fetches current scores for the given pubkeys.
func (h *WSHub) lookupScores(pubkeys []string) []WSScoreEntry {
	stats := h.graph.Stats()
	entries := make([]WSScoreEntry, 0, len(pubkeys))
	for _, pk := range pubkeys {
		raw, ok := h.graph.GetScore(pk)
		if !ok {
			entries = append(entries, WSScoreEntry{Pubkey: pk, Score: 0})
			continue
		}
		entries = append(entries, WSScoreEntry{
			Pubkey:     pk,
			Score:      normalizeScore(raw, stats.Nodes),
			RawScore:   raw,
			Percentile: h.graph.Percentile(pk),
			Rank:       h.graph.Rank(pk),
		})
	}
	return entries
}

// handleWebSocket is the HTTP handler for the /ws/scores endpoint.
func (h *WSHub) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		log.Printf("ws: accept error: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := &WSClient{
		conn:    c,
		pubkeys: make(map[string]bool),
		cancel:  cancel,
	}

	h.Register(client)
	defer func() {
		h.Unregister(client)
		c.CloseNow()
	}()

	// Send welcome message
	gstats := h.graph.Stats()
	welcome := WSMessage{
		Type: "connected",
		Stats: &WSStats{
			Nodes:     gstats.Nodes,
			Edges:     gstats.Edges,
			UpdatedAt: time.Now(),
		},
	}
	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	_ = wsjson.Write(wctx, c, welcome)
	wcancel()

	// Read loop: process subscribe/unsubscribe messages
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var msg WSMessage
		err := wsjson.Read(ctx, c, &msg)
		if err != nil {
			return
		}

		switch msg.Type {
		case "subscribe":
			resolved := make([]string, 0, len(msg.Pubkeys))
			for _, pk := range msg.Pubkeys {
				hex, err := resolvePubkey(pk)
				if err != nil {
					continue
				}
				resolved = append(resolved, hex)
			}
			if len(resolved) == 0 {
				errMsg := WSMessage{Type: "error", Error: "no valid pubkeys provided"}
				wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
				_ = wsjson.Write(wctx, c, errMsg)
				wcancel()
				continue
			}
			// Cap subscriptions at 100 pubkeys
			client.mu.Lock()
			for _, pk := range resolved {
				if len(client.pubkeys) >= 100 {
					break
				}
				client.pubkeys[pk] = true
			}
			client.mu.Unlock()

			// Send current scores immediately
			scores := h.lookupScores(resolved)
			stats := h.graph.Stats()
			resp := WSMessage{
				Type:   "scores",
				Scores: scores,
				Stats: &WSStats{
					Nodes:     stats.Nodes,
					Edges:     stats.Edges,
					UpdatedAt: time.Now(),
				},
			}
			wctx, wcancel = context.WithTimeout(ctx, 5*time.Second)
			_ = wsjson.Write(wctx, c, resp)
			wcancel()

		case "unsubscribe":
			client.mu.Lock()
			for _, pk := range msg.Pubkeys {
				hex, _ := resolvePubkey(pk)
				delete(client.pubkeys, hex)
			}
			client.mu.Unlock()

			ack := WSMessage{Type: "unsubscribed"}
			wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
			_ = wsjson.Write(wctx, c, ack)
			wcancel()

		default:
			errMsg := WSMessage{Type: "error", Error: "unknown message type: " + msg.Type}
			wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
			_ = wsjson.Write(wctx, c, errMsg)
			wcancel()
		}
	}
}

// handleWebSocketInfo returns information about the WebSocket endpoint (for non-WebSocket requests).
func handleWebSocketInfo(hub *WSHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If this is a WebSocket upgrade, handle it
		if r.Header.Get("Upgrade") == "websocket" {
			hub.handleWebSocket(w, r)
			return
		}

		// Otherwise return endpoint documentation
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"endpoint":         "/ws/scores",
			"protocol":         "websocket",
			"connected_clients": hub.ClientCount(),
			"description":      "Real-time WoT score streaming. Subscribe to pubkey score updates pushed after each graph recomputation.",
			"messages": map[string]interface{}{
				"subscribe": map[string]interface{}{
					"description": "Subscribe to score updates for pubkeys (max 100)",
					"example":     `{"type":"subscribe","pubkeys":["<hex_or_npub>","<hex_or_npub>"]}`,
				},
				"unsubscribe": map[string]interface{}{
					"description": "Unsubscribe from pubkey score updates",
					"example":     `{"type":"unsubscribe","pubkeys":["<hex_or_npub>"]}`,
				},
			},
			"responses": map[string]interface{}{
				"connected": "Sent on connection with current graph stats",
				"scores":    "Sent immediately after subscribe with current scores",
				"update":    "Pushed after each graph recomputation (~every 6 hours) with updated scores",
				"error":     "Sent when a message cannot be processed",
			},
		})
	}
}
