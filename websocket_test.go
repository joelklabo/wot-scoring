package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestWSHubRegisterUnregister(t *testing.T) {
	g := NewGraph()
	hub := NewWSHub(g)

	client := &WSClient{
		pubkeys: make(map[string]bool),
		cancel:  func() {},
	}

	hub.Register(client)
	if hub.ClientCount() != 1 {
		t.Fatalf("expected 1 client, got %d", hub.ClientCount())
	}

	hub.Unregister(client)
	if hub.ClientCount() != 0 {
		t.Fatalf("expected 0 clients, got %d", hub.ClientCount())
	}
}

func TestWSHubLookupScores(t *testing.T) {
	g := NewGraph()
	g.AddFollow("alice", "bob")
	g.AddFollow("alice", "carol")
	g.AddFollow("bob", "carol")
	g.ComputePageRank(20, 0.85)

	hub := NewWSHub(g)
	scores := hub.lookupScores([]string{"carol", "unknown"})

	if len(scores) != 2 {
		t.Fatalf("expected 2 score entries, got %d", len(scores))
	}

	// carol should have a score (she has 2 followers)
	if scores[0].Pubkey != "carol" {
		t.Errorf("expected pubkey carol, got %s", scores[0].Pubkey)
	}
	if scores[0].RawScore == 0 {
		t.Error("expected carol to have a non-zero raw score")
	}
	if scores[0].Score == 0 {
		t.Error("expected carol to have a non-zero normalized score")
	}

	// unknown should have zero score
	if scores[1].Pubkey != "unknown" {
		t.Errorf("expected pubkey unknown, got %s", scores[1].Pubkey)
	}
	if scores[1].Score != 0 {
		t.Errorf("expected unknown score 0, got %d", scores[1].Score)
	}
}

func TestWSHubBroadcastNoClients(t *testing.T) {
	g := NewGraph()
	hub := NewWSHub(g)

	// Should not panic with 0 clients
	hub.BroadcastScoreUpdate()
}

func setupTestWSServer(t *testing.T) (*WSHub, *httptest.Server) {
	t.Helper()
	g := NewGraph()
	g.AddFollow("alice", "bob")
	g.AddFollow("bob", "carol")
	g.AddFollow("carol", "alice")
	g.ComputePageRank(20, 0.85)

	hub := NewWSHub(g)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/scores", handleWebSocketInfo(hub))
	server := httptest.NewServer(mux)
	return hub, server
}

func TestWSConnectAndSubscribe(t *testing.T) {
	hub, server := setupTestWSServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Connect
	wsURL := "ws" + server.URL[4:] + "/ws/scores"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer c.CloseNow()

	// Read welcome message
	var welcome WSMessage
	err = wsjson.Read(ctx, c, &welcome)
	if err != nil {
		t.Fatalf("read welcome error: %v", err)
	}
	if welcome.Type != "connected" {
		t.Errorf("expected type 'connected', got '%s'", welcome.Type)
	}
	if welcome.Stats == nil {
		t.Fatal("expected stats in welcome message")
	}
	if welcome.Stats.Nodes != 3 {
		t.Errorf("expected 3 nodes, got %d", welcome.Stats.Nodes)
	}

	// Subscribe to a pubkey
	sub := WSMessage{Type: "subscribe", Pubkeys: []string{"bob"}}
	err = wsjson.Write(ctx, c, sub)
	if err != nil {
		t.Fatalf("write subscribe error: %v", err)
	}

	// Read scores response
	var scores WSMessage
	err = wsjson.Read(ctx, c, &scores)
	if err != nil {
		t.Fatalf("read scores error: %v", err)
	}
	if scores.Type != "scores" {
		t.Errorf("expected type 'scores', got '%s'", scores.Type)
	}
	if len(scores.Scores) != 1 {
		t.Fatalf("expected 1 score entry, got %d", len(scores.Scores))
	}
	if scores.Scores[0].Pubkey != "bob" {
		t.Errorf("expected pubkey 'bob', got '%s'", scores.Scores[0].Pubkey)
	}
	if scores.Scores[0].RawScore == 0 {
		t.Error("expected non-zero raw score for bob")
	}

	// Verify client is registered
	if hub.ClientCount() != 1 {
		t.Errorf("expected 1 connected client, got %d", hub.ClientCount())
	}

	c.Close(websocket.StatusNormalClosure, "done")
}

func TestWSSubscribeMultiplePubkeys(t *testing.T) {
	_, server := setupTestWSServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + server.URL[4:] + "/ws/scores"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer c.CloseNow()

	// Read welcome
	var welcome WSMessage
	_ = wsjson.Read(ctx, c, &welcome)

	// Subscribe to multiple pubkeys
	sub := WSMessage{Type: "subscribe", Pubkeys: []string{"alice", "bob", "carol"}}
	err = wsjson.Write(ctx, c, sub)
	if err != nil {
		t.Fatalf("write error: %v", err)
	}

	var scores WSMessage
	err = wsjson.Read(ctx, c, &scores)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(scores.Scores) != 3 {
		t.Fatalf("expected 3 score entries, got %d", len(scores.Scores))
	}

	c.Close(websocket.StatusNormalClosure, "done")
}

func TestWSUnsubscribe(t *testing.T) {
	_, server := setupTestWSServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + server.URL[4:] + "/ws/scores"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer c.CloseNow()

	// Read welcome
	var welcome WSMessage
	_ = wsjson.Read(ctx, c, &welcome)

	// Subscribe
	sub := WSMessage{Type: "subscribe", Pubkeys: []string{"alice", "bob"}}
	_ = wsjson.Write(ctx, c, sub)
	var scores WSMessage
	_ = wsjson.Read(ctx, c, &scores)

	// Unsubscribe
	unsub := WSMessage{Type: "unsubscribe", Pubkeys: []string{"alice"}}
	err = wsjson.Write(ctx, c, unsub)
	if err != nil {
		t.Fatalf("write unsubscribe error: %v", err)
	}

	var ack WSMessage
	err = wsjson.Read(ctx, c, &ack)
	if err != nil {
		t.Fatalf("read ack error: %v", err)
	}
	if ack.Type != "unsubscribed" {
		t.Errorf("expected type 'unsubscribed', got '%s'", ack.Type)
	}

	c.Close(websocket.StatusNormalClosure, "done")
}

func TestWSInvalidMessageType(t *testing.T) {
	_, server := setupTestWSServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + server.URL[4:] + "/ws/scores"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer c.CloseNow()

	// Read welcome
	var welcome WSMessage
	_ = wsjson.Read(ctx, c, &welcome)

	// Send invalid type
	bad := WSMessage{Type: "invalid"}
	_ = wsjson.Write(ctx, c, bad)

	var errMsg WSMessage
	err = wsjson.Read(ctx, c, &errMsg)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if errMsg.Type != "error" {
		t.Errorf("expected type 'error', got '%s'", errMsg.Type)
	}
	if errMsg.Error == "" {
		t.Error("expected non-empty error message")
	}

	c.Close(websocket.StatusNormalClosure, "done")
}

func TestWSSubscribeEmpty(t *testing.T) {
	_, server := setupTestWSServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + server.URL[4:] + "/ws/scores"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer c.CloseNow()

	// Read welcome
	var welcome WSMessage
	_ = wsjson.Read(ctx, c, &welcome)

	// Subscribe with no pubkeys
	sub := WSMessage{Type: "subscribe", Pubkeys: []string{}}
	_ = wsjson.Write(ctx, c, sub)

	var errMsg WSMessage
	err = wsjson.Read(ctx, c, &errMsg)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if errMsg.Type != "error" {
		t.Errorf("expected type 'error', got '%s'", errMsg.Type)
	}

	c.Close(websocket.StatusNormalClosure, "done")
}

func TestWSBroadcastToSubscribedClients(t *testing.T) {
	hub, server := setupTestWSServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + server.URL[4:] + "/ws/scores"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer c.CloseNow()

	// Read welcome
	var welcome WSMessage
	_ = wsjson.Read(ctx, c, &welcome)

	// Subscribe
	sub := WSMessage{Type: "subscribe", Pubkeys: []string{"alice"}}
	_ = wsjson.Write(ctx, c, sub)
	var scores WSMessage
	_ = wsjson.Read(ctx, c, &scores)

	// Trigger broadcast
	hub.BroadcastScoreUpdate()

	// Read the update
	var update WSMessage
	err = wsjson.Read(ctx, c, &update)
	if err != nil {
		t.Fatalf("read update error: %v", err)
	}
	if update.Type != "update" {
		t.Errorf("expected type 'update', got '%s'", update.Type)
	}
	if len(update.Scores) != 1 {
		t.Fatalf("expected 1 score in update, got %d", len(update.Scores))
	}
	if update.Scores[0].Pubkey != "alice" {
		t.Errorf("expected pubkey 'alice', got '%s'", update.Scores[0].Pubkey)
	}
	if update.Stats == nil {
		t.Fatal("expected stats in update")
	}

	c.Close(websocket.StatusNormalClosure, "done")
}

func TestWSInfoEndpoint(t *testing.T) {
	g := NewGraph()
	hub := NewWSHub(g)
	handler := handleWebSocketInfo(hub)

	req := httptest.NewRequest("GET", "/ws/scores", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}

	// Verify it contains endpoint info
	body := w.Body.String()
	if body == "" {
		t.Fatal("expected non-empty body")
	}
}

func TestWSHubConcurrentAccess(t *testing.T) {
	g := NewGraph()
	hub := NewWSHub(g)

	// Concurrent register/unregister
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &WSClient{pubkeys: make(map[string]bool), cancel: func() {}}
			hub.Register(c)
			hub.ClientCount()
			hub.Unregister(c)
		}()
	}
	wg.Wait()

	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients after concurrent ops, got %d", hub.ClientCount())
	}
}
