"use client";

import { useEffect, useRef, useState } from "react";
import { api, API_BASE, getToken } from "../lib/api";

interface LogEntry {
  id?: number | string;
  ts: string;
  level: "INFO" | "WARN" | "ERROR" | "DEBUG" | string;
  source: string;
  router_slug?: string;
  message: string;
  metadata?: Record<string, any>;
}

export default function LogsPage() {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [wsStatus, setWsStatus] = useState<"connected" | "connecting" | "disconnected">("connecting");
  const [levelFilter, setLevelFilter] = useState<string>("ALL");
  const [sourceFilter, setSourceFilter] = useState<string>("ALL");
  const [search, setSearch] = useState<string>("");
  const [isPaused, setIsPaused] = useState<boolean>(false);
  const [downloading, setDownloading] = useState<boolean>(false);

  const logsEndRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);

  // Initial load via REST
  useEffect(() => {
    api.get<LogEntry[]>("/logs?limit=100")
      .then((data) => {
        if (Array.isArray(data)) {
          // Sort ascending for console stream
          const sorted = [...data].reverse();
          setLogs(sorted);
        }
      })
      .catch((err) => console.error("failed to load initial logs", err));
  }, []);

  // WebSocket Live Streaming
  useEffect(() => {
    const token = getToken();
    if (!token) return;

    let unmounted = false;
    let reconnectTimer: any;

    function connect() {
      if (unmounted) return;
      setWsStatus("connecting");

      // Construct WebSocket URL based on current window location
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      let host = window.location.host;
      let wsPath = "/admin/api/ws/logs";

      // If API_BASE is absolute (e.g. dev mode http://localhost:8080/admin/api)
      if (API_BASE.startsWith("http")) {
        const u = new URL(API_BASE);
        const wsProto = u.protocol === "https:" ? "wss:" : "ws:";
        host = u.host;
        wsPath = `${u.pathname}/ws/logs`.replace(/\/+/g, "/");
      }

      const wsUrl = `${proto}//${host}${wsPath}?token=${encodeURIComponent(token)}`;

      try {
        const ws = new WebSocket(wsUrl);
        wsRef.current = ws;

        ws.onopen = () => {
          if (!unmounted) setWsStatus("connected");
        };

        ws.onmessage = (event) => {
          try {
            const entry: LogEntry = JSON.parse(event.data);
            if (!isPaused) {
              setLogs((prev) => {
                const next = [...prev, entry];
                if (next.length > 500) return next.slice(next.length - 500);
                return next;
              });
            }
          } catch {
            // Raw text log line fallback
            const line = event.data;
            if (!isPaused) {
              setLogs((prev) => [
                ...prev,
                {
                  ts: new Date().toISOString(),
                  level: "INFO",
                  source: "system",
                  message: line,
                },
              ]);
            }
          }
        };

        ws.onerror = () => {
          if (!unmounted) setWsStatus("disconnected");
        };

        ws.onclose = () => {
          if (!unmounted) {
            setWsStatus("disconnected");
            reconnectTimer = setTimeout(connect, 3000);
          }
        };
      } catch (err) {
        if (!unmounted) {
          setWsStatus("disconnected");
          reconnectTimer = setTimeout(connect, 3000);
        }
      }
    }

    connect();

    return () => {
      unmounted = true;
      clearTimeout(reconnectTimer);
      if (wsRef.current) {
        wsRef.current.close();
      }
    };
  }, [isPaused]);

  // Auto-scroll to bottom unless paused
  useEffect(() => {
    if (!isPaused) {
      logsEndRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [logs, isPaused]);

  const filteredLogs = logs.filter((l) => {
    if (levelFilter !== "ALL" && l.level !== levelFilter) return false;
    if (sourceFilter !== "ALL" && l.source !== sourceFilter) return false;
    if (search.trim()) {
      const q = search.toLowerCase();
      const matchMsg = l.message?.toLowerCase().includes(q);
      const matchSrc = l.source?.toLowerCase().includes(q);
      const matchSlug = l.router_slug?.toLowerCase().includes(q);
      const matchMeta = JSON.stringify(l.metadata || {}).toLowerCase().includes(q);
      if (!matchMsg && !matchSrc && !matchSlug && !matchMeta) return false;
    }
    return true;
  });

  const handleDownloadLogs = async () => {
    setDownloading(true);
    try {
      // Fetch fresh export from server API
      const token = getToken();
      const res = await fetch(
        `${API_BASE}/logs/download?level=${levelFilter === "ALL" ? "" : levelFilter}&source=${sourceFilter === "ALL" ? "" : sourceFilter}`,
        {
          headers: {
            Authorization: `Bearer ${token}`,
          },
        }
      );
      if (!res.ok) throw new Error("Failed to download logs");
      const blob = await res.blob();
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `cleverroute-logs-${new Date().toISOString().replace(/[:.]/g, "-")}.txt`;
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
      document.body.removeChild(a);
    } catch (err) {
      // Fallback to client-side buffer export
      const text = filteredLogs
        .map(
          (l) =>
            `[${l.ts}] [${l.level.padEnd(5)}] [${l.router_slug ? `${l.source}:${l.router_slug}` : l.source}] ${l.message} ${JSON.stringify(l.metadata || {})}`
        )
        .join("\n");
      const blob = new Blob([text], { type: "text/plain;charset=utf-8" });
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `cleverroute-logs-${new Date().toISOString().replace(/[:.]/g, "-")}.txt`;
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
      document.body.removeChild(a);
    } finally {
      setDownloading(false);
    }
  };

  const getLevelBadgeClass = (level: string) => {
    switch (level) {
      case "ERROR":
        return "badge-red";
      case "WARN":
        return "badge-amber";
      case "DEBUG":
        return "badge-gray";
      case "INFO":
      default:
        return "badge-blue";
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight">Live System Logs</h1>
            {wsStatus === "connected" ? (
              <span className="badge badge-green">
                <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
                Live WebSocket Connected
              </span>
            ) : wsStatus === "connecting" ? (
              <span className="badge badge-amber">
                <span className="h-2 w-2 rounded-full bg-amber-500 animate-ping" />
                Connecting…
              </span>
            ) : (
              <span className="badge badge-red">
                <span className="h-2 w-2 rounded-full bg-red-500" />
                Disconnected (Polling)
              </span>
            )}
          </div>
          <p className="text-sm text-slate-500 dark:text-slate-400 mt-1">
            Real-time streaming console for AI proxy events, router runtimes, authentication, and supervisor lifecycle.
          </p>
        </div>

        {/* Action Controls */}
        <div className="flex items-center gap-2.5">
          <button
            onClick={() => setIsPaused(!isPaused)}
            className={`btn text-xs font-semibold px-3 py-2 ${
              isPaused
                ? "bg-emerald-600 text-white hover:bg-emerald-700 shadow-sm"
                : "btn-secondary"
            }`}
          >
            {isPaused ? "▶️ Resume Stream" : "⏸️ Pause Stream"}
          </button>

          <button
            onClick={() => setLogs([])}
            className="btn-ghost text-xs px-3 py-2"
            title="Clear buffer"
          >
            🧹 Clear
          </button>

          <button
            id="download-logs-btn"
            onClick={handleDownloadLogs}
            disabled={downloading}
            className="btn-primary text-xs font-semibold px-3.5 py-2 flex items-center gap-1.5 shadow-md hover:shadow-glow-brand"
          >
            {downloading ? (
              <>
                <div className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/30 border-t-white" />
                Exporting…
              </>
            ) : (
              <>
                <span>⬇️</span>
                Download Live Logs (.txt)
              </>
            )}
          </button>
        </div>
      </div>

      {/* Filters Bar */}
      <div className="card grid grid-cols-1 gap-3 sm:grid-cols-4 p-4">
        <div>
          <label className="block text-[11px] font-semibold text-slate-500 dark:text-slate-400 uppercase tracking-wider mb-1">
            Search Messages
          </label>
          <input
            className="input text-xs"
            type="text"
            placeholder="Filter keywords, model, slug…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>

        <div>
          <label className="block text-[11px] font-semibold text-slate-500 dark:text-slate-400 uppercase tracking-wider mb-1">
            Log Level
          </label>
          <select
            className="input text-xs cursor-pointer"
            value={levelFilter}
            onChange={(e) => setLevelFilter(e.target.value)}
          >
            <option value="ALL">All Levels</option>
            <option value="INFO">INFO</option>
            <option value="WARN">WARN</option>
            <option value="ERROR">ERROR</option>
            <option value="DEBUG">DEBUG</option>
          </select>
        </div>

        <div>
          <label className="block text-[11px] font-semibold text-slate-500 dark:text-slate-400 uppercase tracking-wider mb-1">
            Source Component
          </label>
          <select
            className="input text-xs cursor-pointer"
            value={sourceFilter}
            onChange={(e) => setSourceFilter(e.target.value)}
          >
            <option value="ALL">All Sources</option>
            <option value="proxy">Proxy (AI Requests)</option>
            <option value="router">Router / Aggregator</option>
            <option value="auth">Auth & Sessions</option>
            <option value="system">System & Supervisor</option>
            <option value="audit">Audit Actions</option>
          </select>
        </div>

        <div className="flex items-end justify-between sm:justify-end gap-2 text-xs text-slate-500 dark:text-slate-400 pb-1">
          <span>Showing <strong className="text-slate-900 dark:text-slate-200">{filteredLogs.length}</strong> entries</span>
        </div>
      </div>

      {/* Terminal Live Stream View */}
      <div className="rounded-xl border border-black/15 dark:border-white/10 bg-[#090d16] text-slate-200 font-mono text-xs shadow-2xl overflow-hidden">
        {/* Terminal Titlebar */}
        <div className="flex items-center justify-between px-4 py-2.5 bg-[#0e1320] border-b border-white/5 text-[11px] text-slate-400">
          <div className="flex items-center gap-2">
            <span className="inline-block h-3 w-3 rounded-full bg-red-500/80" />
            <span className="inline-block h-3 w-3 rounded-full bg-amber-500/80" />
            <span className="inline-block h-3 w-3 rounded-full bg-emerald-500/80" />
            <span className="ml-2 font-semibold text-slate-300">cleverroute.log</span>
          </div>
          <div className="flex items-center gap-3 text-[11px]">
            <span>Buffer: {logs.length}/500</span>
            {isPaused && (
              <span className="text-amber-400 font-semibold uppercase animate-pulse">
                [Paused]
              </span>
            )}
          </div>
        </div>

        {/* Console Body */}
        <div className="p-4 h-[550px] overflow-y-auto space-y-1.5 select-text">
          {filteredLogs.length === 0 ? (
            <div className="flex h-full items-center justify-center text-slate-500 italic">
              No logs matching current filters. Trigger actions or wait for live stream…
            </div>
          ) : (
            filteredLogs.map((l, idx) => (
              <div
                key={idx}
                className="flex items-start gap-2.5 hover:bg-white/[0.04] p-1 rounded transition group leading-relaxed"
              >
                <span className="text-slate-500 shrink-0 text-[11px] select-none">
                  {new Date(l.ts).toLocaleTimeString()}
                </span>
                <span className={`badge shrink-0 text-[10px] py-0 px-1.5 uppercase font-bold ${getLevelBadgeClass(l.level)}`}>
                  {l.level}
                </span>
                <span className="text-brand-300 shrink-0 font-semibold text-[11px]">
                  [{l.router_slug ? `${l.source}:${l.router_slug}` : l.source}]
                </span>
                <span className="text-slate-200 break-all flex-1">{l.message}</span>
                {l.metadata && Object.keys(l.metadata).length > 0 && (
                  <span className="text-slate-500 text-[10px] shrink-0 font-mono group-hover:text-slate-400">
                    {JSON.stringify(l.metadata)}
                  </span>
                )}
              </div>
            ))
          )}
          <div ref={logsEndRef} />
        </div>
      </div>
    </div>
  );
}
