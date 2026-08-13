"use client";

import { useEffect, useRef, useState } from "react";
import { useWebSocket } from "../lib/useWebSocket";

interface HistoryEntry {
  id: string;
  promptPath?: string;
  cmd: string;
  output?: string;
  exitCode?: number;
}

const quickPills = [
  { label: "🐳 docker ps", cmd: "docker ps" },
  { label: "💾 df -h", cmd: "df -h" },
  { label: "🧠 free -m", cmd: "free -m" },
  { label: "⚙️ ps aux", cmd: "ps aux" },
  { label: "⏱️ uptime", cmd: "uptime" },
  { label: "📁 ls -la", cmd: "ls -la" },
  { label: "🌱 env", cmd: "env" },
];

// Helper to parse simple ANSI colors to React Spans
function parseAnsi(text: string) {
  if (!text) return null;
  const parts = text.split(/(\x1b\[[0-9;]*m)/g);
  let currentColor = "text-slate-200";

  return parts.map((part, i) => {
    if (part.startsWith("\x1b[")) {
      if (part.includes("31m")) currentColor = "text-red-400 font-semibold";
      else if (part.includes("32m")) currentColor = "text-emerald-400 font-semibold";
      else if (part.includes("33m")) currentColor = "text-amber-400 font-semibold";
      else if (part.includes("34m")) currentColor = "text-sky-400 font-semibold";
      else if (part.includes("35m")) currentColor = "text-purple-400 font-semibold";
      else if (part.includes("36m")) currentColor = "text-teal-400 font-semibold";
      else if (part.includes("1m")) currentColor += " font-bold";
      else if (part.includes("0m")) currentColor = "text-slate-200";
      return null;
    }
    return (
      <span key={i} className={currentColor}>
        {part}
      </span>
    );
  });
}

export default function TerminalPage() {
  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const [currentInput, setCurrentInput] = useState("");
  const [cursorPos, setCursorPos] = useState(0);
  const [cmdHistory, setCmdHistory] = useState<string[]>([]);
  const [historyIdx, setHistoryIdx] = useState<number>(-1);
  const [isFocused, setIsFocused] = useState(true);
  const [currentPath, setCurrentPath] = useState("~/cleverroute");

  const terminalEndRef = useRef<HTMLDivElement>(null);
  const hiddenInputRef = useRef<HTMLInputElement>(null);

  const { status, sendJson } = useWebSocket("/ws/terminal", (data) => {
    if (data.type === "info" || data.type === "stdout" || data.type === "stderr") {
      setHistory((prev) => {
        if (prev.length === 0) return prev;
        const lastIdx = prev.length - 1;
        const last = prev[lastIdx];
        const newOutput = (last.output || "") + (data.data || "");
        const updated = [...prev];
        updated[lastIdx] = { ...last, output: newOutput };
        return updated;
      });
    } else if (data.type === "exit") {
      setHistory((prev) => {
        if (prev.length === 0) return prev;
        const lastIdx = prev.length - 1;
        const last = prev[lastIdx];
        const updated = [...prev];
        updated[lastIdx] = { ...last, exitCode: data.exit_code };
        return updated;
      });
    }
  });

  useEffect(() => {
    terminalEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [history, currentInput]);

  const runCommand = (cmdToRun: string) => {
    const trimmed = cmdToRun.trim();
    if (!trimmed) {
      // Empty prompt enter
      setHistory((prev) => [
        ...prev,
        {
          id: Math.random().toString(),
          promptPath: currentPath,
          cmd: "",
          output: "",
        },
      ]);
      setCurrentInput("");
      setCursorPos(0);
      return;
    }

    if (trimmed === "clear") {
      setHistory([]);
      setCurrentInput("");
      setCursorPos(0);
      return;
    }

    // Add entry to screen history
    setHistory((prev) => [
      ...prev,
      {
        id: Math.random().toString(),
        promptPath: currentPath,
        cmd: trimmed,
        output: "",
      },
    ]);

    // Save in command history
    setCmdHistory((prev) => [...prev.filter((c) => c !== trimmed), trimmed]);
    setHistoryIdx(-1);

    // Send via WebSocket
    sendJson({ cmd: trimmed });

    setCurrentInput("");
    setCursorPos(0);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      runCommand(currentInput);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      if (cmdHistory.length > 0) {
        const nextIdx = historyIdx < cmdHistory.length - 1 ? historyIdx + 1 : historyIdx;
        setHistoryIdx(nextIdx);
        const val = cmdHistory[cmdHistory.length - 1 - nextIdx] || "";
        setCurrentInput(val);
        setCursorPos(val.length);
      }
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      if (historyIdx > 0) {
        const nextIdx = historyIdx - 1;
        setHistoryIdx(nextIdx);
        const val = cmdHistory[cmdHistory.length - 1 - nextIdx] || "";
        setCurrentInput(val);
        setCursorPos(val.length);
      } else if (historyIdx === 0) {
        setHistoryIdx(-1);
        setCurrentInput("");
        setCursorPos(0);
      }
    } else if (e.key === "l" && e.ctrlKey) {
      e.preventDefault();
      setHistory([]);
    } else if (e.key === "c" && e.ctrlKey) {
      e.preventDefault();
      runCommand("");
    }
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = e.target.value;
    setCurrentInput(val);
    setCursorPos(e.target.selectionStart || val.length);
  };

  // Focus controller
  const focusTerminal = () => {
    hiddenInputRef.current?.focus();
    setIsFocused(true);
  };

  // Render Powerline Prompt Segment
  const renderPowerlinePrompt = (path: string = currentPath) => (
    <span className="inline-flex items-center text-xs font-mono select-none my-0.5">
      {/* Segment 1: User / Host */}
      <span className="bg-[#485437] text-white px-2.5 py-0.5 font-bold flex items-center gap-1.5 rounded-l-sm">
        <span>⚡</span>
        <span>admin@cleverroute</span>
      </span>
      {/* Chevron 1 */}
      <svg className="h-5 w-3 text-[#485437] fill-current bg-[#0284c7] -ml-0.5" viewBox="0 0 10 20">
        <polygon points="0,0 10,10 0,20" />
      </svg>

      {/* Segment 2: Directory Path */}
      <span className="bg-[#0284c7] text-white px-2.5 py-0.5 font-semibold flex items-center gap-1">
        <span>📁</span>
        <span>{path}</span>
      </span>
      {/* Chevron 2 */}
      <svg className="h-5 w-3 text-[#0284c7] fill-current bg-[#16a34a] -ml-0.5" viewBox="0 0 10 20">
        <polygon points="0,0 10,10 0,20" />
      </svg>

      {/* Segment 3: Git Branch */}
      <span className="bg-[#16a34a] text-white px-2.5 py-0.5 font-bold flex items-center gap-1">
        <span></span>
        <span>main</span>
      </span>
      {/* Chevron 3 */}
      <svg className="h-5 w-3 text-[#16a34a] fill-current -ml-0.5" viewBox="0 0 10 20">
        <polygon points="0,0 10,10 0,20" />
      </svg>

      <span className="ml-2 text-emerald-400 font-bold">❯</span>
    </span>
  );

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight">Ubuntu Zsh Terminal</h1>
            {status === "connected" ? (
              <span className="badge badge-green">
                <span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />
                🟢 Connected (Native WebSocket)
              </span>
            ) : status === "connecting" ? (
              <span className="badge badge-amber">
                <span className="h-2 w-2 rounded-full bg-amber-500 animate-ping" />
                Connecting WebSocket…
              </span>
            ) : (
              <span className="badge badge-red">
                <span className="h-2 w-2 rounded-full bg-red-500" />
                🔴 Disconnected (Auto-Reconnecting)
              </span>
            )}
          </div>
          <p className="text-sm text-slate-500 dark:text-slate-400 mt-1">
            Oh My Zsh Powerline server shell with direct inline typing and real-time execution.
          </p>
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={() => setHistory([])}
            className="btn-ghost text-xs px-3 py-2"
          >
            🧹 Clear Console
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
            key={pill.cmd}
            onClick={() => runCommand(pill.cmd)}
            className="px-2.5 py-1 text-xs font-mono rounded-lg bg-black/5 dark:bg-white/10 text-slate-800 dark:text-slate-200 hover:bg-brand hover:text-white dark:hover:bg-brand dark:hover:text-white transition shadow-sm"
          >
            {pill.label}
          </button>
        ))}
      </div>

      {/* Ubuntu Aubergine Terminal Window Frame */}
      <div
        className="rounded-xl border border-purple-950/40 bg-[#1e0018] text-slate-100 font-mono text-xs shadow-2xl overflow-hidden flex flex-col h-[650px] relative"
        onClick={focusTerminal}
      >
        {/* Hidden Input for Keyboard Interception */}
        <input
          ref={hiddenInputRef}
          type="text"
          className="opacity-0 absolute top-0 left-0 h-1 w-1 pointer-events-none"
          value={currentInput}
          onChange={handleInputChange}
          onKeyDown={handleKeyDown}
          onFocus={() => setIsFocused(true)}
          onBlur={() => setIsFocused(false)}
          autoFocus
        />

        {/* Ubuntu Window Title Bar (#300a24) */}
        <div className="flex items-center justify-between px-4 py-2.5 bg-[#300a24] border-b border-purple-900/30 text-slate-300 text-[11px] select-none shrink-0">
          <div className="flex items-center gap-2">
            <span className="inline-block h-3 w-3 rounded-full bg-[#ff5f56] hover:opacity-80 transition cursor-pointer" />
            <span className="inline-block h-3 w-3 rounded-full bg-[#ffbd2e] hover:opacity-80 transition cursor-pointer" />
            <span className="inline-block h-3 w-3 rounded-full bg-[#27c93f] hover:opacity-80 transition cursor-pointer" />
            <span className="ml-3 font-semibold text-white tracking-wide">
              admin@cleverroute: ~ (zsh)
            </span>
          </div>

          <div className="flex items-center gap-4 text-purple-200 text-[11px]">
            <span>Oh My Zsh + Powerline</span>
            <span className="text-emerald-400 font-bold">● Active</span>
          </div>
        </div>

        {/* Ubuntu Terminal Canvas Window */}
        <div className="p-4 flex-1 overflow-y-auto space-y-2 select-text font-mono leading-relaxed">
          {/* Welcome Banner */}
          <div className="text-purple-200/90 pb-2 border-b border-purple-900/30">
            <div className="text-emerald-400 font-bold text-sm mb-1">
              Welcome to CleverRoute Ubuntu Control Shell v1.0 LTS
            </div>
            <div className="text-slate-400 text-[11px]">
              Type commands directly at the prompt cursor position. Press Enter to execute.
            </div>
          </div>

          {/* Past History Execution Output */}
          {history.map((h) => (
            <div key={h.id} className="space-y-1">
              <div className="flex items-center gap-2 flex-wrap">
                {renderPowerlinePrompt(h.promptPath)}
                <span className="text-white font-bold">{h.cmd}</span>
              </div>
              {h.output && (
                <div className="pl-2 text-slate-200 whitespace-pre-wrap break-all leading-normal">
                  {parseAnsi(h.output)}
                </div>
              )}
            </div>
          ))}

          {/* Active Prompt Line with DIRECT INLINE TYPING */}
          <div className="flex items-center gap-2 flex-wrap pt-1">
            {renderPowerlinePrompt(currentPath)}
            <div className="inline-flex items-center text-white font-bold text-xs">
              <span>{currentInput.slice(0, cursorPos)}</span>
              <span
                className={`inline-block w-2.5 h-4 bg-emerald-400 text-black font-extrabold text-center leading-none ${
                  isFocused ? "animate-pulse" : "opacity-40"
                }`}
              >
                {currentInput[cursorPos] || " "}
              </span>
              <span>{currentInput.slice(cursorPos + 1)}</span>
            </div>
          </div>

          <div ref={terminalEndRef} />
        </div>
      </div>
    </div>
  );
}
