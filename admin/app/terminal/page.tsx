"use client";

import { useEffect, useRef, useState } from "react";
import { API_BASE, getToken } from "../lib/api";

interface OutputLine {
  id: number;
  cmd?: string;
  type: "stdout" | "stderr" | "info" | "error" | "input";
  text: string;
}

const quickPills = [
  { label: "🐳 docker ps", cmd: "docker ps" },
  { label: "💾 df -h", cmd: "df -h" },
  { label: "🧠 free -m", cmd: "free -m" },
  { label: "⚙️ ps aux", cmd: "ps aux" },
  { label: "⏱️ uptime", cmd: "uptime" },
  { label: "📁 ls -la", cmd: "ls -la" },
];

export default function TerminalPage() {
  const [lines, setLines] = useState<OutputLine[]>([]);
  const [input, setInput] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [historyIdx, setHistoryIdx] = useState<number>(-1);
  const [wsStatus, setWsStatus] = useState<"connected" | "connecting" | "disconnected">("connecting");

  const terminalEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    const token = getToken();
    if (!token) return;

    let unmounted = false;
    let timer: any;

    function connect() {
      if (unmounted) return;
      setWsStatus("connecting");

      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      let host = window.location.host;
      let wsPath = "/admin/api/ws/terminal";

      if (API_BASE.startsWith("http")) {
        const u = new URL(API_BASE);
        host = u.host;
        wsPath = `${u.pathname}/ws/terminal`.replace(/\/+/g, "/");
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
            const data = JSON.parse(event.data);
            if (data.type === "info" || data.type === "stdout" || data.type === "stderr") {
              setLines((prev) => [
                ...prev,
                {
                  id: Date.now() + Math.random(),
                  cmd: data.cmd,
                  type: data.type,
                  text: data.data || "",
                },
              ]);
            } else if (data.type === "exit") {
              const codeStr = data.exit_code === 0 ? "0" : `${data.exit_code}`;
              setLines((prev) => [
                ...prev,
                {
                  id: Date.now() + Math.random(),
                  type: data.exit_code === 0 ? "info" : "error",
                  text: `[Process exited with code ${codeStr}]`,
                },
              ]);
            }
          } catch {
            setLines((prev) => [
              ...prev,
              {
                id: Date.now() + Math.random(),
                type: "stdout",
                text: event.data,
              },
            ]);
          }
        };

        ws.onclose = () => {
          if (!unmounted) {
            setWsStatus("disconnected");
            timer = setTimeout(connect, 3000);
          }
        };

        ws.onerror = () => {
          if (!unmounted) setWsStatus("disconnected");
        };
      } catch {
        if (!unmounted) {
          setWsStatus("disconnected");
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

  useEffect(() => {
    terminalEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [lines]);

  const runCmd = (commandToSend: string) => {
    const trimmed = commandToSend.trim();
    if (!trimmed) return;

    // Record in input display
    setLines((prev) => [
      ...prev,
      {
        id: Date.now() + Math.random(),
        type: "input",
        text: trimmed,
      },
    ]);

    // Record in command history
    setHistory((prev) => [...prev.filter((c) => c !== trimmed), trimmed]);
    setHistoryIdx(-1);

    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify({ cmd: trimmed }));
    } else {
      setLines((prev) => [
        ...prev,
        {
          id: Date.now(),
          type: "error",
          text: "WebSocket connection not open. Command not sent.",
        },
      ]);
    }

    setInput("");
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      runCmd(input);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      if (history.length > 0) {
        const nextIdx = historyIdx < history.length - 1 ? historyIdx + 1 : historyIdx;
        setHistoryIdx(nextIdx);
        setInput(history[history.length - 1 - nextIdx] || "");
      }
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      if (historyIdx > 0) {
        const nextIdx = historyIdx - 1;
        setHistoryIdx(nextIdx);
        setInput(history[history.length - 1 - nextIdx] || "");
      } else if (historyIdx === 0) {
        setHistoryIdx(-1);
        setInput("");
      }
    }
  };

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight">Interactive Server Terminal</h1>
            {wsStatus === "connected" ? (
              <span className="badge badge-green">
                <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
                Live Terminal WS Connected
              </span>
            ) : wsStatus === "connecting" ? (
              <span className="badge badge-amber">
                <span className="h-2 w-2 rounded-full bg-amber-500 animate-ping" />
                Connecting…
              </span>
            ) : (
              <span className="badge badge-red">
                <span className="h-2 w-2 rounded-full bg-red-500" />
                Terminal Disconnected
              </span>
            )}
          </div>
          <p className="text-sm text-slate-500 dark:text-slate-400 mt-1">
            Execute real-time shell commands directly on the CleverRoute control plane host.
          </p>
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={() => setLines([])}
            className="btn-ghost text-xs px-3 py-2"
          >
            🧹 Clear Screen
          </button>
        </div>
      </div>

      {/* Quick Action Suggestion Pills */}
      <div className="card p-3.5 flex flex-wrap items-center gap-2 border border-black/10 dark:border-white/10">
        <span className="text-xs font-semibold text-slate-500 dark:text-slate-400 mr-1">
          Quick Commands:
        </span>
        {quickPills.map((pill) => (
          <button
            key={pill.cmd}
            onClick={() => runCmd(pill.cmd)}
            className="px-2.5 py-1 text-xs font-mono rounded-lg bg-black/5 dark:bg-white/10 text-slate-800 dark:text-slate-200 hover:bg-brand hover:text-white dark:hover:bg-brand dark:hover:text-white transition shadow-sm"
          >
            {pill.label}
          </button>
        ))}
      </div>

      {/* Full-Featured Web Terminal Console */}
      <div className="rounded-xl border border-black/20 dark:border-white/10 bg-[#090d16] text-slate-100 font-mono text-xs shadow-2xl overflow-hidden flex flex-col h-[600px]">
        {/* Terminal Titlebar */}
        <div className="flex items-center justify-between px-4 py-2.5 bg-[#0e1320] border-b border-white/10 text-slate-400 text-[11px] shrink-0">
          <div className="flex items-center gap-2">
            <span className="inline-block h-3 w-3 rounded-full bg-red-500/80" />
            <span className="inline-block h-3 w-3 rounded-full bg-amber-500/80" />
            <span className="inline-block h-3 w-3 rounded-full bg-emerald-500/80" />
            <span className="ml-2 font-bold text-slate-200">admin@cleverroute:~#</span>
          </div>
          <div className="flex items-center gap-3">
            <span>Shell: /bin/sh</span>
            <span>Timeout: 60s</span>
          </div>
        </div>

        {/* Console Content Window */}
        <div
          className="p-4 flex-1 overflow-y-auto space-y-1.5 select-text"
          onClick={() => inputRef.current?.focus()}
        >
          {lines.map((l) => {
            if (l.type === "input") {
              return (
                <div key={l.id} className="flex items-center gap-2 text-emerald-400 font-semibold pt-1">
                  <span>admin@cleverroute:~$</span>
                  <span className="text-white">{l.text}</span>
                </div>
              );
            }
            if (l.type === "stderr" || l.type === "error") {
              return (
                <pre key={l.id} className="text-red-400 whitespace-pre-wrap break-all">
                  {l.text}
                </pre>
              );
            }
            if (l.type === "info") {
              return (
                <pre key={l.id} className="text-brand-300 font-semibold whitespace-pre-wrap break-all">
                  {l.text}
                </pre>
              );
            }
            return (
              <pre key={l.id} className="text-slate-200 whitespace-pre-wrap break-all leading-relaxed">
                {l.text}
              </pre>
            );
          })}
          <div ref={terminalEndRef} />
        </div>

        {/* Command Input Prompt Bar */}
        <div className="p-3 bg-[#0e1320] border-t border-white/10 flex items-center gap-2 shrink-0">
          <span className="text-emerald-400 font-bold text-xs select-none">admin@cleverroute:~$</span>
          <input
            ref={inputRef}
            id="terminal-input"
            type="text"
            className="bg-transparent border-0 outline-none flex-1 font-mono text-xs text-white placeholder-slate-500"
            placeholder="Type command and press Enter (Use ↑/↓ for history)…"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            autoFocus
          />
          <button
            id="send-cmd-btn"
            onClick={() => runCmd(input)}
            className="btn-primary text-xs py-1 px-3"
          >
            Run
          </button>
        </div>
      </div>
    </div>
  );
}
