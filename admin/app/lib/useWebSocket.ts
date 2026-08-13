"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import { API_BASE, getToken } from "./api";

export type WSStatus = "connected" | "connecting" | "disconnected";

export function useWebSocket<T = any>(
  relPath: string,
  onMessageCallback?: (data: T) => void
) {
  const [status, setStatus] = useState<WSStatus>("connecting");
  const [lastMessage, setLastMessage] = useState<T | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const callbackRef = useRef(onMessageCallback);

  useEffect(() => {
    callbackRef.current = onMessageCallback;
  }, [onMessageCallback]);

  const connect = useCallback(() => {
    const token = getToken();
    if (!token) {
      setStatus("disconnected");
      return;
    }

    setStatus("connecting");

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    let host = window.location.host;
    let fullWsPath = relPath.startsWith("/ws") ? `/admin/api${relPath}` : `/admin/api/ws${relPath}`;

    if (API_BASE.startsWith("http")) {
      try {
        const u = new URL(API_BASE);
        host = u.host;
        fullWsPath = `${u.pathname}${relPath.startsWith("/") ? relPath : "/" + relPath}`.replace(/\/+/g, "/");
      } catch {
        /* ignore */
      }
    }

    const wsUrl = `${proto}//${host}${fullWsPath}?token=${encodeURIComponent(token)}`;

    try {
      if (wsRef.current) {
        wsRef.current.close();
      }

      const ws = new WebSocket(wsUrl);
      wsRef.current = ws;

      ws.onopen = () => {
        setStatus("connected");
      };

      ws.onmessage = (event) => {
        try {
          const parsed = JSON.parse(event.data);
          setLastMessage(parsed);
          if (callbackRef.current) {
            callbackRef.current(parsed);
          }
        } catch {
          const raw = event.data as unknown as T;
          setLastMessage(raw);
          if (callbackRef.current) {
            callbackRef.current(raw);
          }
        }
      };

      ws.onerror = () => {
        setStatus("disconnected");
      };

      ws.onclose = (e) => {
        setStatus("disconnected");
      };
    } catch {
      setStatus("disconnected");
    }
  }, [relPath]);

  useEffect(() => {
    let unmounted = false;
    let timer: any;

    connect();

    // Reconnection & Keepalive
    const interval = setInterval(() => {
      if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
        try {
          wsRef.current.send(JSON.stringify({ type: "ping" }));
        } catch {
          /* ignore */
        }
      } else if (!unmounted && status === "disconnected") {
        connect();
      }
    }, 15000);

    return () => {
      unmounted = true;
      clearInterval(interval);
      clearTimeout(timer);
      if (wsRef.current) {
        wsRef.current.close();
      }
    };
  }, [connect, relPath, status]);

  const sendJson = useCallback((data: any) => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
      wsRef.current.send(typeof data === "string" ? data : JSON.stringify(data));
      return true;
    }
    return false;
  }, []);

  return {
    status,
    lastMessage,
    sendJson,
    reconnect: connect,
  };
}
