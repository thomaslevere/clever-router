"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { API_BASE, api, getToken } from "./api";
import type { Router } from "./types";

export interface RouterRealtimeState {
  runtimeState: string;
  healthStatus: string;
  desiredState: string;
  targetAddr: string;
  nativePanelUrl: string;
  modelsCount: number;
  providersCount: number;
  logs: string[];
  wsStatus: "connected" | "connecting" | "disconnected";
  busyAction: "" | "start" | "restart" | "stop" | "discover" | "wipe";
  actionError: string;
}

export function useRouterRealtime(routerId: string, initialRouter: Router | null, onRefresh?: () => void) {
  const [state, setState] = useState<RouterRealtimeState>({
    runtimeState: initialRouter?.runtime_state || "stopped",
    healthStatus: initialRouter?.health_status || "unknown",
    desiredState: initialRouter?.desired_state || "stopped",
    targetAddr: initialRouter?.target_addr || "",
    nativePanelUrl: initialRouter?.native_panel_url || "",
    modelsCount: initialRouter?.models_count || 0,
    providersCount: initialRouter?.providers_count || 0,
    logs: [],
    wsStatus: "connecting",
    busyAction: "",
    actionError: "",
  });

  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const onRefreshRef = useRef(onRefresh);

  useEffect(() => {
    onRefreshRef.current = onRefresh;
  }, [onRefresh]);

  // Sync initialRouter if it arrives or updates externally
  useEffect(() => {
    if (initialRouter) {
      setState((prev) => ({
        ...prev,
        runtimeState: prev.busyAction ? prev.runtimeState : initialRouter.runtime_state,
        healthStatus: initialRouter.health_status,
        desiredState: initialRouter.desired_state,
        targetAddr: initialRouter.target_addr || prev.targetAddr,
        nativePanelUrl: initialRouter.native_panel_url || prev.nativePanelUrl,
        modelsCount: initialRouter.models_count,
        providersCount: initialRouter.providers_count,
      }));
    }
  }, [initialRouter]);

  const connect = useCallback(() => {
    if (!routerId) return;

    const token = getToken();
    if (!token) {
      setState((prev) => ({ ...prev, wsStatus: "disconnected" }));
      return;
    }

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    let host = window.location.host;
    let fullWsPath = `/admin/api/routers/${routerId}/ws`;

    if (API_BASE.startsWith("http")) {
      try {
        const u = new URL(API_BASE);
        host = u.host;
        fullWsPath = `${u.pathname}/routers/${routerId}/ws`.replace(/\/+/g, "/");
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
        setState((prev) => ({ ...prev, wsStatus: "connected", actionError: "" }));
      };

      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data);
          if (msg.type === "state_changed") {
            setState((prev) => {
              const newRuntime = msg.runtime_state || msg.status || prev.runtimeState;
              const newHealth = msg.health_status || prev.healthStatus;
              
              // Clear busy action once transition completes
              let newBusy = prev.busyAction;
              if (prev.busyAction === "start" && (newRuntime === "running" || newRuntime === "unhealthy")) {
                newBusy = "";
              } else if (prev.busyAction === "stop" && newRuntime === "stopped") {
                newBusy = "";
              } else if (prev.busyAction === "restart" && newRuntime === "running") {
                newBusy = "";
              }

              return {
                ...prev,
                runtimeState: newRuntime,
                healthStatus: newHealth,
                desiredState: msg.desired_state || prev.desiredState,
                targetAddr: msg.target_addr || prev.targetAddr,
                nativePanelUrl: msg.native_panel_url || prev.nativePanelUrl,
                modelsCount: msg.models_count !== undefined ? msg.models_count : prev.modelsCount,
                providersCount: msg.providers_count !== undefined ? msg.providers_count : prev.providersCount,
                busyAction: newBusy,
              };
            });
            if (onRefreshRef.current) onRefreshRef.current();
          } else if (msg.type === "log_chunk") {
            if (msg.log_message) {
              setState((prev) => ({
                ...prev,
                logs: [...prev.logs.slice(-499), msg.log_message],
              }));
            }
          } else if (msg.type === "models_updated") {
            setState((prev) => ({
              ...prev,
              modelsCount: msg.models_count !== undefined ? msg.models_count : prev.modelsCount,
              providersCount: msg.providers_count !== undefined ? msg.providers_count : prev.providersCount,
              busyAction: prev.busyAction === "discover" ? "" : prev.busyAction,
            }));
            if (onRefreshRef.current) onRefreshRef.current();
          }
        } catch {
          /* ignore non-json messages */
        }
      };

      ws.onerror = () => {
        setState((prev) => ({ ...prev, wsStatus: "disconnected" }));
      };

      ws.onclose = () => {
        setState((prev) => ({ ...prev, wsStatus: "disconnected" }));
        if (reconnectTimeoutRef.current) clearTimeout(reconnectTimeoutRef.current);
        reconnectTimeoutRef.current = setTimeout(connect, 3000);
      };
    } catch {
      setState((prev) => ({ ...prev, wsStatus: "disconnected" }));
    }
  }, [routerId]);

  useEffect(() => {
    connect();
    return () => {
      if (wsRef.current) wsRef.current.close();
      if (reconnectTimeoutRef.current) clearTimeout(reconnectTimeoutRef.current);
    };
  }, [connect]);

  // Fallback status polling every 8s
  useEffect(() => {
    const t = setInterval(async () => {
      try {
        const data = await api.get<{
          id: string;
          runtime_state: string;
          health_status: string;
          desired_state: string;
          target_addr?: string;
          native_panel_url?: string;
          models_count: number;
          providers_count: number;
        }>(`/routers/${routerId}/status`);

        if (data) {
          setState((prev) => ({
            ...prev,
            runtimeState: prev.busyAction ? prev.runtimeState : data.runtime_state,
            healthStatus: data.health_status,
            desiredState: data.desired_state,
            targetAddr: data.target_addr || prev.targetAddr,
            nativePanelUrl: data.native_panel_url || prev.nativePanelUrl,
            modelsCount: data.models_count,
            providersCount: data.providers_count,
          }));
        }
      } catch {
        /* ignore polling errors */
      }
    }, 8000);
    return () => clearInterval(t);
  }, [routerId]);

  // Interactive Action Handlers with Debounce Lock
  const handleStart = async () => {
    if (state.busyAction || state.runtimeState === "starting" || state.runtimeState === "running") {
      return;
    }
    setState((prev) => ({
      ...prev,
      busyAction: "start",
      runtimeState: "starting",
      actionError: "",
    }));
    try {
      await api.post(`/routers/${routerId}/start`);
    } catch (e: any) {
      setState((prev) => ({
        ...prev,
        busyAction: "",
        runtimeState: "stopped",
        actionError: e.message || "Start failed",
      }));
    }
  };

  const handleRestart = async () => {
    if (state.busyAction || state.runtimeState === "starting" || state.runtimeState === "stopping") {
      return;
    }
    setState((prev) => ({
      ...prev,
      busyAction: "restart",
      runtimeState: "starting",
      actionError: "",
    }));
    try {
      await api.post(`/routers/${routerId}/restart`);
    } catch (e: any) {
      setState((prev) => ({
        ...prev,
        busyAction: "",
        actionError: e.message || "Restart failed",
      }));
    }
  };

  const handleStop = async () => {
    if (state.busyAction || state.runtimeState === "stopping" || state.runtimeState === "stopped") {
      return;
    }
    setState((prev) => ({
      ...prev,
      busyAction: "stop",
      runtimeState: "stopping",
      actionError: "",
    }));
    try {
      await api.post(`/routers/${routerId}/stop`);
    } catch (e: any) {
      setState((prev) => ({
        ...prev,
        busyAction: "",
        actionError: e.message || "Stop failed",
      }));
    }
  };

  const handleDiscover = async () => {
    if (state.busyAction === "discover") {
      return;
    }
    setState((prev) => ({
      ...prev,
      busyAction: "discover",
      actionError: "",
    }));
    try {
      await api.post(`/routers/${routerId}/discover`);
    } catch (e: any) {
      setState((prev) => ({
        ...prev,
        busyAction: "",
        actionError: e.message || "Discovery failed",
      }));
    }
  };

  const clearLogs = () => {
    setState((prev) => ({ ...prev, logs: [] }));
  };

  return {
    ...state,
    handleStart,
    handleRestart,
    handleStop,
    handleDiscover,
    clearLogs,
  };
}
