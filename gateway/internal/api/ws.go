package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/clever-route/gateway/internal/cache"
	"github.com/docker/docker/pkg/stdcopy"
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

type RouterEvent struct {
	Type           string         `json:"type"` // "state_changed", "log_chunk", "models_updated", "error"
	RouterID       string         `json:"router_id"`
	Status         string         `json:"status,omitempty"`
	DesiredState   string         `json:"desired_state,omitempty"`
	RuntimeState   string         `json:"runtime_state,omitempty"`
	HealthStatus   string         `json:"health_status,omitempty"`
	TargetAddr     string         `json:"target_addr,omitempty"`
	NativePanelURL string         `json:"native_panel_url,omitempty"`
	LogMessage     string         `json:"log_message,omitempty"`
	ModelsCount    int            `json:"models_count,omitempty"`
	ProvidersCount int            `json:"providers_count,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	Timestamp      int64          `json:"timestamp"`
}

type RouterWSHub struct {
	cache   *cache.Cache
	clients map[string]map[*client]bool
	mu      sync.RWMutex
}

func newRouterWSHub(c *cache.Cache) *RouterWSHub {
	return &RouterWSHub{
		cache:   c,
		clients: make(map[string]map[*client]bool),
	}
}

func (h *RouterWSHub) register(routerID string, c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[routerID]; !ok {
		h.clients[routerID] = make(map[*client]bool)
	}
	h.clients[routerID][c] = true
}

func (h *RouterWSHub) unregister(routerID string, c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if conns, ok := h.clients[routerID]; ok {
		delete(conns, c)
		close(c.send)
		if len(conns) == 0 {
			delete(h.clients, routerID)
		}
	}
}

func (h *RouterWSHub) Broadcast(routerID string, event RouterEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	event.RouterID = routerID
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	if conns, ok := h.clients[routerID]; ok {
		for c := range conns {
			select {
			case c.send <- data:
			default:
			}
		}
	}
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

func (a *API) wsRouter(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		token = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	}
	if !a.isTokenValid(c.Request.Context(), token) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	routerID := c.Param("id")
	r, err := a.findRouter(c, routerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "router not found"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[ws] router upgrade error for %s: %v", routerID, err)
		return
	}

	cl := &client{
		conn: conn,
		send: make(chan []byte, 512),
	}

	a.routerHub.register(r.ID, cl)
	defer a.routerHub.unregister(r.ID, cl)

	// Send immediate initial state snapshot
	initialEvent := RouterEvent{
		Type:           "state_changed",
		RouterID:       r.ID,
		Status:         r.RuntimeState,
		DesiredState:   r.DesiredState,
		RuntimeState:   r.RuntimeState,
		HealthStatus:   r.HealthStatus,
		TargetAddr:     r.TargetAddr,
		NativePanelURL: r.NativePanelURL,
		ModelsCount:    r.ModelsCount,
		ProvidersCount: r.ProvidersCount,
		Timestamp:      time.Now().UnixMilli(),
	}
	if initialData, err := json.Marshal(initialEvent); err == nil {
		_ = conn.WriteMessage(websocket.TextMessage, initialData)
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// Stream container logs to this client if container is running
	if a.manager != nil && r.ContainerID != "" {
		go func() {
			rc, err := a.manager.Logs(ctx, r, true)
			if err != nil {
				return
			}
			defer rc.Close()

			pr, pw := io.Pipe()
			go func() {
				defer pw.Close()
				_, _ = stdcopy.StdCopy(pw, pw, rc)
			}()

			scanner := bufio.NewScanner(pr)
			for scanner.Scan() {
				select {
				case <-ctx.Done():
					return
				default:
					line := scanner.Text()
					logEvent := RouterEvent{
						Type:       "log_chunk",
						RouterID:   r.ID,
						LogMessage: line,
						Timestamp:  time.Now().UnixMilli(),
					}
					if data, err := json.Marshal(logEvent); err == nil {
						select {
						case cl.send <- data:
						default:
						}
					}
				}
			}
		}()
	}

	// Writer goroutine
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-cl.send:
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if !ok {
					_ = conn.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// Reader goroutine (listens for close/pong)
	conn.SetReadLimit(2048)
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
