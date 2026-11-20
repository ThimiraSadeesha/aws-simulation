package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Hub fans out real-time messages to websocket subscribers of each account.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{}
	log  *slog.Logger
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{subs: map[string]map[chan []byte]struct{}{}, log: log}
}

func (h *Hub) Publish(account string, msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[account] {
		select {
		case ch <- b:
		default: // slow consumer: drop rather than block the publisher
		}
	}
}

func (h *Hub) subscribe(account string) chan []byte {
	ch := make(chan []byte, 256)
	h.mu.Lock()
	if h.subs[account] == nil {
		h.subs[account] = map[chan []byte]struct{}{}
	}
	h.subs[account][ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(account string, ch chan []byte) {
	h.mu.Lock()
	delete(h.subs[account], ch)
	if len(h.subs[account]) == 0 {
		delete(h.subs, account)
	}
	h.mu.Unlock()
}

// Subscribers returns the number of active subscriptions (stats/tests).
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, m := range h.subs {
		n += len(m)
	}
	return n
}

func (s *Server) upgrader() *websocket.Upgrader {
	allowed := map[string]bool{}
	for _, o := range s.Cfg.AllowedOrigins {
		allowed[o] = true
	}
	return &websocket.Upgrader{
		ReadBufferSize: 4096, WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			o := r.Header.Get("Origin")
			if o == "" {
				return true // non-browser clients (CLI) send no Origin; they still need credentials
			}
			if allowed[o] {
				return true
			}
			// same-origin (through the frontend proxy the Host header is preserved or forwarded)
			host := r.Host
			if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
				host = fh
			}
			return o == "http://"+host || o == "https://"+host
		},
	}
}

// wsAuth upgrades the connection and, for cookie sessions, requires the first frame to carry
// the CSRF token (cross-site websocket hijacking protection).
func (s *Server) wsAuth(w http.ResponseWriter, r *http.Request) (*websocket.Conn, bool) {
	p := principalFrom(r)
	conn, err := s.upgrader().Upgrade(w, r, nil)
	if err != nil {
		return nil, false
	}
	conn.SetReadLimit(64 * 1024)
	if p.ViaSession {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		var first struct {
			CSRFToken string `json:"csrf_token"`
		}
		if err := conn.ReadJSON(&first); err != nil || !constantEq(first.CSRFToken, p.CSRFToken) {
			_ = conn.WriteJSON(map[string]any{"type": "error", "error": "invalid or missing csrf_token in first message"})
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "unauthenticated"))
			conn.Close()
			return nil, false
		}
		_ = conn.SetReadDeadline(time.Time{})
	}
	_ = conn.WriteJSON(map[string]any{"type": "ready", "principal": p.Display(), "account_id": p.AccountID})
	return conn, true
}

func (s *Server) handleEventsWS(c *Ctx) error {
	conn, ok := s.wsAuth(c.W, c.R)
	if !ok {
		return nil
	}
	defer conn.Close()
	ch := s.Hub.subscribe(c.P.AccountID)
	defer s.Hub.unsubscribe(c.P.AccountID, ch)
	done := make(chan struct{})
	go func() { // reader: detect close, answer pings
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-done:
			return nil
		case <-c.R.Context().Done():
			return nil
		case msg := <-ch:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return nil
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return nil
			}
		}
	}
}
