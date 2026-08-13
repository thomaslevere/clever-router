package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/clever-route/gateway/internal/logger"
	"github.com/clever-route/gateway/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type terminalReq struct {
	Cmd string `json:"cmd"`
	Cwd string `json:"cwd,omitempty"`
}

type terminalResp struct {
	Type     string `json:"type"` // "stdout", "stderr", "exit", "error", "info"
	Data     string `json:"data,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Cmd      string `json:"cmd,omitempty"`
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

	var writeMu sync.Mutex
	sendMsg := func(resp terminalResp) {
		writeMu.Lock()
		defer writeMu.Unlock()
		b, _ := json.Marshal(resp)
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = conn.WriteMessage(websocket.TextMessage, b)
	}

	// Send initial welcome message
	sendMsg(terminalResp{
		Type: "info",
		Data: "\x1b[1;34m================================================================================\x1b[0m\r\n" +
			"\x1b[1;32m CleverRoute Control Plane — Interactive Web Terminal v1.0\x1b[0m\r\n" +
			"\x1b[1;34m================================================================================\x1b[0m\r\n" +
			"Type any shell command (e.g. \x1b[36mdocker ps\x1b[0m, \x1b[36mdf -h\x1b[0m, \x1b[36mfree -m\x1b[0m, \x1b[36mps aux\x1b[0m) and press Enter.\r\n\r\n",
	})

	// Set keepalive
	conn.SetReadLimit(64 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	// Heartbeat ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			writeMu.Lock()
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))

		var req terminalReq
		if err := json.Unmarshal(msg, &req); err != nil {
			// Treat raw string as command
			req.Cmd = string(msg)
		}

		cmdStr := strings.TrimSpace(req.Cmd)
		if cmdStr == "" {
			continue
		}

		// Audit execution
		actor, _ := c.Get("actor")
		actorStr := "admin"
		if s, ok := actor.(string); ok && s != "" {
			actorStr = s
		}

		logger.Info("terminal", "system", fmt.Sprintf("%s executed terminal command: %s", actorStr, cmdStr), store.Map{"cmd": cmdStr})

		// Execute command in sub-shell
		execCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

		var cmd *exec.Cmd
		if req.Cwd != "" {
			cmd = exec.CommandContext(execCtx, "/bin/sh", "-c", cmdStr)
			cmd.Dir = req.Cwd
		} else {
			cmd = exec.CommandContext(execCtx, "/bin/sh", "-c", cmdStr)
		}

		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		err = cmd.Run()
		cancel()

		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}

		if stdoutBuf.Len() > 0 {
			sendMsg(terminalResp{
				Type: "stdout",
				Cmd:  cmdStr,
				Data: stdoutBuf.String(),
			})
		}
		if stderrBuf.Len() > 0 {
			sendMsg(terminalResp{
				Type: "stderr",
				Cmd:  cmdStr,
				Data: stderrBuf.String(),
			})
		}

		sendMsg(terminalResp{
			Type:     "exit",
			Cmd:      cmdStr,
			ExitCode: exitCode,
		})
	}
}
