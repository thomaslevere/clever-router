package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/clever-route/gateway/internal/logger"
	"github.com/clever-route/gateway/internal/store"
	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type resizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (a *API) wsTerminal(c *gin.Context) {
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
		log.Printf("[ws-terminal] upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Audit terminal session start
	actor, _ := c.Get("actor")
	actorStr := "admin"
	if s, ok := actor.(string); ok && s != "" {
		actorStr = s
	}
	logger.Info("terminal", "system", fmt.Sprintf("%s started interactive PTY server terminal session", actorStr), store.Map{})

	// Spawn real PTY process (prefer bash on Ubuntu, fallback to sh)
	shell := "bash"
	if _, err := exec.LookPath("bash"); err != nil {
		if _, err2 := exec.LookPath("/bin/bash"); err2 == nil {
			shell = "/bin/bash"
		} else {
			shell = "/bin/sh"
		}
	}

	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptyFile, err := pty.Start(cmd)
	if err != nil {
		log.Printf("[ws-terminal] pty.Start error: %v", err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("\r\n\x1b[31mFailed to start PTY shell: %v\x1b[0m\r\n", err)))
		return
	}
	defer func() {
		_ = ptyFile.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	// Set initial window size
	_ = pty.Setsize(ptyFile, &pty.Winsize{Rows: 24, Cols: 80})

	var writeMu sync.Mutex
	writeWS := func(msgType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return conn.WriteMessage(msgType, data)
	}

	// Goroutine 1: Read raw PTY output and stream to WebSocket client
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := ptyFile.Read(buf)
			if n > 0 {
				if err := writeWS(websocket.TextMessage, buf[:n]); err != nil {
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[ws-terminal] pty read error: %v", err)
				}
				return
			}
		}
	}()

	// Keepalive ticker
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			if err := writeWS(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}()

	// Goroutine 2: Read WebSocket messages and write directly to PTY stdin or resize PTY
	conn.SetReadLimit(64 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))

		if msgType == websocket.TextMessage || msgType == websocket.BinaryMessage {
			// Check if message is JSON resize payload
			var rmsg resizeMsg
			if err := json.Unmarshal(msg, &rmsg); err == nil && rmsg.Type == "resize" && rmsg.Cols > 0 && rmsg.Rows > 0 {
				_ = pty.Setsize(ptyFile, &pty.Winsize{Rows: rmsg.Rows, Cols: rmsg.Cols})
				continue
			}

			// Write input bytes directly to PTY stdin
			_, _ = ptyFile.Write(msg)
		}
	}
}
