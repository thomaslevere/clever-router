package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/clever-route/gateway/internal/cache"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow internal Next.js reverse proxy & browser origins
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type client struct {
	conn *websocket.Conn
	send chan []byte
}

type WSHub struct {
	cache   *cache.Cache
	clients map[*client]bool
	mu      sync.RWMutex
}

func newWSHub(c *cache.Cache) *WSHub {
	h := &WSHub{
		cache:   c,
		clients: make(map[*client]bool),
	}
	return h
}

func (h *WSHub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

func (h *WSHub) unregister(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

func (h *WSHub) broadcast(msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
		}
	}
}

// StartListening starts background Redis subscription to channel and broadcasts to WS clients.
func (h *WSHub) StartListening(ctx context.Context, channel string) {
	if h.cache == nil {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if err := h.cache.SubscribeEvent(ctx, channel, func(payload string) {
					h.broadcast([]byte(payload))
				}); err != nil {
					time.Sleep(1 * time.Second)
				}
			}
		}
	}()
}

func (a *API) wsLogs(c *gin.Context) {
	// Verify auth from query param or header
	token := c.Query("token")
	if token == "" {
		token = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	}
	if !a.isTokenValid(c.Request.Context(), token) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}

	cl := &client{
		conn: conn,
		send: make(chan []byte, 256),
	}

	a.logHub.register(cl)
	defer a.logHub.unregister(cl)

	// Writer goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case msg, ok := <-cl.send:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if !ok {
					_ = conn.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// Reader goroutine (listens for close/pong)
	conn.SetReadLimit(512)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (a *API) wsAudit(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		token = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	}
	if !a.isTokenValid(c.Request.Context(), token) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[ws] audit upgrade error: %v", err)
		return
	}

	cl := &client{
		conn: conn,
		send: make(chan []byte, 256),
	}

	a.auditHub.register(cl)
	defer a.auditHub.unregister(cl)

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case msg, ok := <-cl.send:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if !ok {
					_ = conn.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	conn.SetReadLimit(512)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (a *API) isTokenValid(ctx context.Context, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if token == a.cfg.AdminToken {
		return true
	}
	sess, err := a.cache.GetSession(ctx, token)
	return err == nil && sess != nil
}
