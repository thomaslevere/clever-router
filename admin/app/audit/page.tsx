"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api, API_BASE, getToken, UnauthorizedError } from "../lib/api";
import type { AuditEntry } from "../lib/types";

export default function AuditPage() {
  const [rows, setRows] = useState<AuditEntry[]>([]);
  const [search, setSearch] = useState("");
  const [err, setErr] = useState("");
  const [exporting, setExporting] = useState(false);
  const [wsLive, setWsLive] = useState(false);

  const wsRef = useRef<WebSocket | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await api.get<AuditEntry[]>("/audit");
      setRows(Array.isArray(res) ? res : []);
      setErr("");
    } catch (e: any) {
      if (e instanceof UnauthorizedError) {
        window.dispatchEvent(new Event("cr:auth-changed"));
        return;
      }
      setErr(e.message);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // WebSocket Live Audit Streaming
  useEffect(() => {
    const token = getToken();
    if (!token) return;

    let unmounted = false;
    let timer: any;

    function connect() {
      if (unmounted) return;
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      let host = window.location.host;
      let wsPath = "/admin/api/ws/audit";

      if (API_BASE.startsWith("http")) {
        const u = new URL(API_BASE);
        host = u.host;
        wsPath = `${u.pathname}/ws/audit`.replace(/\/+/g, "/");
      }

      const wsUrl = `${proto}//${host}${wsPath}?token=${encodeURIComponent(token)}`;

      try {
        const ws = new WebSocket(wsUrl);
        wsRef.current = ws;

        ws.onopen = () => {
          if (!unmounted) setWsLive(true);
        };

        ws.onmessage = (event) => {
          try {
            const entry: AuditEntry = JSON.parse(event.data);
            setRows((prev) => [entry, ...prev.filter((r) => r.id !== entry.id)]);
          } catch (e) {
            console.error("audit parse error", e);
          }
        };

        ws.onclose = () => {
          if (!unmounted) {
            setWsLive(false);
            timer = setTimeout(connect, 3000);
          }
        };
      } catch {
        if (!unmounted) {
          setWsLive(false);
          timer = setTimeout(connect, 3000);
        }
      }
    }

    connect();

    return () => {
      unmounted = true;
      clearTimeout(timer);
      if (wsRef.current) wsRef.current.close();
    };
  }, []);

  const handleExport = async () => {
    setExporting(true);
    try {
      const token = getToken();
      const res = await fetch(`${API_BASE}/audit/download`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) throw new Error("Export failed");
      const blob = await res.blob();
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `cleverroute-audit-${new Date().toISOString().replace(/[:.]/g, "-")}.txt`;
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
      document.body.removeChild(a);
    } catch {
      // Client fallback export
      const text = filteredRows
        .map(
          (r) =>
            `[${r.ts}] Actor=${r.actor} Action=${r.action} Target=${r.target_type}:${r.target_id || ""} Details=${JSON.stringify(r.after || {})}`
        )
        .join("\n");
      const blob = new Blob([text], { type: "text/plain;charset=utf-8" });
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `cleverroute-audit-${new Date().toISOString().replace(/[:.]/g, "-")}.txt`;
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
      document.body.removeChild(a);
    } finally {
      setExporting(false);
    }
  };

  const filteredRows = rows.filter((r) => {
    if (!search.trim()) return true;
    const q = search.toLowerCase();
    return (
      r.actor?.toLowerCase().includes(q) ||
      r.action?.toLowerCase().includes(q) ||
      r.target_type?.toLowerCase().includes(q) ||
      r.target_id?.toLowerCase().includes(q) ||
      JSON.stringify(r.after || {}).toLowerCase().includes(q)
    );
  });

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight">Audit Log</h1>
            {wsLive ? (
              <span className="badge badge-green">
                <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
                Real-time WebSocket Live
              </span>
            ) : (
              <span className="badge badge-gray">
                Auto-refreshing
              </span>
            )}
          </div>
          <p className="text-sm text-slate-500 dark:text-slate-400 mt-1">
            Every administrative mutation, auth event, and configuration change recorded permanently.
          </p>
        </div>

        <div className="flex items-center gap-3">
          <button
            onClick={load}
            className="btn-ghost text-xs px-3 py-2"
          >
            🔄 Refresh
          </button>
          <button
            id="export-audit-btn"
            onClick={handleExport}
            disabled={exporting}
            className="btn-primary text-xs font-semibold px-3.5 py-2 flex items-center gap-1.5 shadow-md hover:shadow-glow-brand"
          >
            {exporting ? (
              <>
                <div className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/30 border-t-white" />
                Exporting…
              </>
            ) : (
              <>
                <span>⬇️</span>
                Export Audit (.txt)
              </>
            )}
          </button>
        </div>
      </div>

      {/* Search Bar */}
      <div className="card p-3 flex items-center gap-3">
        <span className="text-sm text-slate-400">🔍</span>
        <input
          type="text"
          className="bg-transparent border-0 outline-none text-xs w-full text-slate-900 dark:text-slate-100 placeholder-slate-400"
          placeholder="Filter by actor, action (e.g. auth.login, router.create), target or details…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        {search && (
          <button
            onClick={() => setSearch("")}
            className="text-xs text-slate-400 hover:text-slate-600 dark:hover:text-slate-200"
          >
            Clear
          </button>
        )}
      </div>

      {/* Audit Table */}
      <div className="card overflow-hidden p-0 shadow-lg border border-black/10 dark:border-white/10">
        <table className="w-full text-xs">
          <thead className="border-b border-black/10 dark:border-white/10 bg-slate-100/70 dark:bg-white/[0.02] text-slate-500 dark:text-slate-400 uppercase tracking-wider font-semibold">
            <tr>
              <th className="px-4 py-3 text-left">Timestamp</th>
              <th className="px-4 py-3 text-left">Actor</th>
              <th className="px-4 py-3 text-left">Action</th>
              <th className="px-4 py-3 text-left">Target</th>
              <th className="px-4 py-3 text-left">Details</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-black/5 dark:divide-white/5">
            {filteredRows.map((r) => (
              <tr key={r.id} className="hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition">
                <td className="px-4 py-3 text-slate-500 dark:text-slate-400 font-mono text-[11px] whitespace-nowrap">
                  {new Date(r.ts).toLocaleString()}
                </td>
                <td className="px-4 py-3 font-medium text-slate-800 dark:text-slate-200">
                  <span className="inline-flex items-center gap-1.5">
                    <span>👤</span>
                    <span>{r.actor}</span>
                  </span>
                </td>
                <td className="px-4 py-3">
                  <span className="font-mono font-semibold px-2 py-0.5 rounded bg-black/5 dark:bg-white/10 text-brand">
                    {r.action}
                  </span>
                </td>
                <td className="px-4 py-3 text-slate-600 dark:text-slate-400">
                  <span className="font-medium text-slate-900 dark:text-slate-200">{r.target_type}</span>
                  {r.target_id && (
                    <span className="font-mono text-[11px] text-slate-500 ml-1">
                      :{r.target_id.length > 12 ? r.target_id.slice(0, 12) + "…" : r.target_id}
                    </span>
                  )}
                </td>
                <td className="px-4 py-3 text-slate-500 dark:text-slate-400 font-mono text-[11px] max-w-md truncate">
                  {r.after ? JSON.stringify(r.after) : "-"}
                </td>
              </tr>
            ))}
            {filteredRows.length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-12 text-center text-slate-400">
                  No audit entries found.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {err && (
        <div className="card border-red-500/30 text-xs text-red-500 bg-red-500/10 p-3">
          {err}
        </div>
      )}
    </div>
  );
}
