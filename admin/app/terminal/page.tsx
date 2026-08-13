"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import { API_BASE, getToken } from "../lib/api";

const quickPills = [
  { label: "🐳 docker ps", cmd: "docker ps\r" },
  { label: "💾 df -h", cmd: "df -h\r" },
  { label: "🧠 free -m", cmd: "free -m\r" },
  { label: "⚙️ ps aux", cmd: "ps aux\r" },
  { label: "⏱️ uptime", cmd: "uptime\r" },
  { label: "📁 ls -la", cmd: "ls -la\r" },
  { label: "🌱 env", cmd: "env\r" },
  { label: "🧹 clear", cmd: "clear\r" },
];

export default function TerminalPage() {
  const terminalRef = useRef<HTMLDivElement>(null);
  const xtermRef = useRef<any>(null);
  const fitAddonRef = useRef<any>(null);
  const wsRef = useRef<WebSocket | null>(null);

  const [status, setStatus] = useState<"connected" | "connecting" | "disconnected">("connecting");

  const sendRawData = useCallback((data: string | object) => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
      wsRef.current.send(typeof data === "string" ? data : JSON.stringify(data));
    }
  }, []);

  useEffect(() => {
    let unmounted = false;
    let term: any;
    let fitAddon: any;
    let resizeTimer: any;

    async function initTerminal() {
      if (unmounted || !terminalRef.current) return;

      // Dynamically import xterm and fit addon (client side only)
      const { Terminal } = await import("@xterm/xterm");
      const { FitAddon } = await import("@xterm/addon-fit");

      if (unmounted || !terminalRef.current) return;

      term = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: 'Ubuntu Mono, DejaVu Sans Mono, Menlo, Consolas, monospace',
        theme: {
          background: "#1e0018", // Authentic Ubuntu Terminal background
          foreground: "#ffffff",
          cursor: "#4ade80",
          selectionBackground: "rgba(119, 138, 88, 0.4)",
          black: "#2e3436",
          red: "#cc0000",
          green: "#4e9a06",
          yellow: "#c4a000",
          blue: "#3465a4",
          magenta: "#75507b",
          cyan: "#06989a",
          white: "#d3d7cf",
          brightBlack: "#555753",
          brightRed: "#ef2929",
          brightGreen: "#8ae234",
          brightYellow: "#fce94f",
          brightBlue: "#729fcf",
          brightMagenta: "#ad7fa8",
          brightCyan: "#34e2e2",
          brightWhite: "#eeeeec",
        },
      });

      fitAddon = new FitAddon();
      term.loadAddon(fitAddon);

      // Clear existing content and open xterm inside container
      terminalRef.current.innerHTML = "";
      term.open(terminalRef.current);
      fitAddon.fit();

      xtermRef.current = term;
      fitAddonRef.current = fitAddon;

      // Handle user keypresses -> send directly to PTY stdin over WebSocket
      term.onData((data: string) => {
        if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
          wsRef.current.send(data);
        }
      });

      // Handle terminal resize -> send resize dimensions to PTY
      term.onResize(({ cols, rows }: { cols: number; rows: number }) => {
        if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
          wsRef.current.send(JSON.stringify({ type: "resize", cols, rows }));
        }
      });

      // Connect WebSocket
      connectWS();
    }

    function connectWS() {
      const token = getToken();
      if (!token) {
        setStatus("disconnected");
        return;
      }

      setStatus("connecting");

      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      let host = window.location.host;
      let wsPath = "/admin/api/ws/terminal";

      if (API_BASE.startsWith("http")) {
        try {
          const u = new URL(API_BASE);
          host = u.host;
          wsPath = `${u.pathname}/ws/terminal`.replace(/\/+/g, "/");
        } catch {
          /* ignore */
        }
      }

      const wsUrl = `${proto}//${host}${wsPath}?token=${encodeURIComponent(token)}`;

      try {
        if (wsRef.current) {
          wsRef.current.close();
        }

        const ws = new WebSocket(wsUrl);
        wsRef.current = ws;

        ws.onopen = () => {
          if (unmounted) return;
          setStatus("connected");
          if (term && fitAddon) {
            fitAddon.fit();
            ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
          }
        };

        ws.onmessage = (event) => {
          if (term) {
            term.write(event.data);
          }
        };

        ws.onerror = () => {
          if (!unmounted) setStatus("disconnected");
        };

        ws.onclose = () => {
          if (!unmounted) {
            setStatus("disconnected");
            resizeTimer = setTimeout(connectWS, 3000);
          }
        };
      } catch {
        if (!unmounted) {
          setStatus("disconnected");
          resizeTimer = setTimeout(connectWS, 3000);
        }
      }
    }

    initTerminal();

    const handleWindowResize = () => {
      if (fitAddonRef.current && xtermRef.current) {
        fitAddonRef.current.fit();
      }
    };

    window.addEventListener("resize", handleWindowResize);

    return () => {
      unmounted = true;
      clearTimeout(resizeTimer);
      window.removeEventListener("resize", handleWindowResize);
      if (wsRef.current) wsRef.current.close();
      if (term) term.dispose();
    };
  }, []);

  const handleQuickPill = (cmd: string) => {
    if (xtermRef.current) {
      xtermRef.current.focus();
    }
    sendRawData(cmd);
  };

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight">Real Server Interactive Terminal (xterm.js)</h1>
            {status === "connected" ? (
              <span className="badge badge-green">
                <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
                🟢 Connected (Native PTY WebSocket)
              </span>
            ) : status === "connecting" ? (
              <span className="badge badge-amber">
                <span className="h-2 w-2 rounded-full bg-amber-500 animate-ping" />
                Connecting PTY Shell…
              </span>
            ) : (
              <span className="badge badge-red">
                <span className="h-2 w-2 rounded-full bg-red-500" />
                🔴 Disconnected (Auto-Reconnecting)
              </span>
            )}
          </div>
          <p className="text-sm text-slate-500 dark:text-slate-400 mt-1">
            Real interactive server PTY terminal (xterm.js) running directly on the application host.
          </p>
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={() => handleQuickPill("clear\r")}
            className="btn-ghost text-xs px-3 py-2"
          >
            🧹 Clear Terminal
          </button>
        </div>
      </div>

      {/* Quick Action Suggestion Pills */}
      <div className="card p-3.5 flex flex-wrap items-center gap-2 border border-black/10 dark:border-white/10">
        <span className="text-xs font-semibold text-slate-500 dark:text-slate-400 mr-1">
          Quick Execution:
        </span>
        {quickPills.map((pill) => (
          <button
            key={pill.label}
            onClick={() => handleQuickPill(pill.cmd)}
            className="px-2.5 py-1 text-xs font-mono rounded-lg bg-black/5 dark:bg-white/10 text-slate-800 dark:text-slate-200 hover:bg-brand hover:text-white dark:hover:bg-brand dark:hover:text-white transition shadow-sm"
          >
            {pill.label}
          </button>
        ))}
      </div>

      {/* Ubuntu Aubergine Terminal Window Frame with xterm.js Canvas */}
      <div className="rounded-xl border border-purple-950/40 bg-[#1e0018] shadow-2xl overflow-hidden flex flex-col h-[650px]">
        {/* Ubuntu Window Title Bar (#300a24) */}
        <div className="flex items-center justify-between px-4 py-2.5 bg-[#300a24] border-b border-purple-900/30 text-slate-300 text-[11px] select-none shrink-0">
          <div className="flex items-center gap-2">
            <span className="inline-block h-3 w-3 rounded-full bg-[#ff5f56] hover:opacity-80 transition cursor-pointer" />
            <span className="inline-block h-3 w-3 rounded-full bg-[#ffbd2e] hover:opacity-80 transition cursor-pointer" />
            <span className="inline-block h-3 w-3 rounded-full bg-[#27c93f] hover:opacity-80 transition cursor-pointer" />
            <span className="ml-3 font-semibold text-white tracking-wide">
              admin@cleverroute: ~ (xterm.js / PTY)
            </span>
          </div>

          <div className="flex items-center gap-4 text-purple-200 text-[11px]">
            <span>Engine: xterm.js v5</span>
            <span className="text-emerald-400 font-bold">● PTY Active</span>
          </div>
        </div>

        {/* xterm.js Terminal Mount Element */}
        <div
          ref={terminalRef}
          className="p-3 flex-1 overflow-hidden bg-[#1e0018]"
          onClick={() => xtermRef.current?.focus()}
        />
      </div>
    </div>
  );
}
