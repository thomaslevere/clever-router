"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, UnauthorizedError, getRouterPanelUrl } from "../../lib/api";
import type { Credential, EnvVariable, Model, Router } from "../../lib/types";
import DeleteRouterModal from "../../components/DeleteRouterModal";
import EnvironmentVariablesCard from "../../components/EnvironmentVariablesCard";
import { useRouterRealtime } from "../../lib/useRouterRealtime";

function stateBadge(s: string) {
  const map: Record<string, string> = {
    running: "badge-green",
    stopped: "badge-gray",
    failed: "badge-red",
    unhealthy: "badge-amber",
    starting: "badge-amber animate-pulse",
    stopping: "badge-red animate-pulse",
  };
  return map[s] || "badge-gray";
}

export default function RouterDetailPage() {
  const params = useParams();
  const routerNav = useRouter();
  const id = params.id as string;
  const [r, setR] = useState<Router | null>(null);
  const [models, setModels] = useState<Model[]>([]);
  const [creds, setCreds] = useState<Credential[]>([]);
  const [envVars, setEnvVars] = useState<EnvVariable[]>([]);
  const [autoRestart, setAutoRestart] = useState(false);
  const [err, setErr] = useState("");
  const [showDeleteModal, setShowDeleteModal] = useState(false);
  const [autoScrollLogs, setAutoScrollLogs] = useState(true);
  const terminalEndRef = useRef<HTMLDivElement>(null);

  const [credProvider, setCredProvider] = useState("");
  const [credKey, setCredKey] = useState("");
  const [panelUrl, setPanelUrl] = useState("");
  const [copiedUrl, setCopiedUrl] = useState(false);
  const [copiedBaseUrl, setCopiedBaseUrl] = useState(false);
  const [initialPassword, setInitialPassword] = useState<string>("");
  const [isDefaultPassword, setIsDefaultPassword] = useState<boolean>(false);
  const [copiedPass, setCopiedPass] = useState(false);
  const [isWiping, setIsWiping] = useState(false);

  const loadFull = useCallback(async () => {
    try {
      const [router, ms, cs, envData, passData] = await Promise.all([
        api.get<Router>(`/routers/${id}`),
        api.get<Model[]>(`/routers/${id}/models`).catch(() => []),
        api.get<Credential[]>(`/routers/${id}/credentials`).catch(() => []),
        api
          .get<{ env_vars: EnvVariable[]; auto_restart_on_env_change: boolean }>(`/routers/${id}/env`)
          .catch(() => ({ env_vars: [], auto_restart_on_env_change: false })),
        api
          .get<{ initial_password: string; is_default: boolean; adapter_type: string }>(`/routers/${id}/initial-password`)
          .catch(() => null),
      ]);
      setR(router);
      setModels(ms);
      setCreds(cs);
      if (envData) {
        setEnvVars(envData.env_vars || []);
        setAutoRestart(!!envData.auto_restart_on_env_change);
      }
      if (passData) {
        setInitialPassword(passData.initial_password);
        setIsDefaultPassword(passData.is_default);
      }
      setErr("");
    } catch (e: any) {
      if (e instanceof UnauthorizedError) {
        window.dispatchEvent(new Event("cr:auth-changed"));
        return;
      }
      setErr(e.message);
    }
  }, [id]);

  useEffect(() => {
    loadFull();
  }, [loadFull]);

  const realtime = useRouterRealtime(id, r, loadFull);

  useEffect(() => {
    if (r) {
      const live = {
        ...r,
        runtime_state: realtime.runtimeState,
        health_status: realtime.healthStatus,
        target_addr: realtime.targetAddr || r.target_addr,
        native_panel_url: realtime.nativePanelUrl || r.native_panel_url,
      };
      setPanelUrl(getRouterPanelUrl(live));
    }
  }, [r, realtime.runtimeState, realtime.healthStatus, realtime.targetAddr, realtime.nativePanelUrl]);

  // Auto-scroll terminal on new log lines
  useEffect(() => {
    if (autoScrollLogs) {
      terminalEndRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [realtime.logs, autoScrollLogs]);

  async function saveCred(e: React.FormEvent) {
    e.preventDefault();
    if (!credProvider || !credKey) return;
    try {
      await api.put(`/routers/${id}/credentials/${encodeURIComponent(credProvider)}`, {
        key: credKey,
      });
      setCredProvider("");
      setCredKey("");
      loadFull();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  async function handleWipe() {
    const confirmWipe = confirm(
      `⚠️ WARNING: This will stop ${r?.name || id}, permanently DELETE all SQLite databases in /app/data, and PURGE its Cellar S3 snapshot backups.\n\nAre you sure you want to perform a fresh factory reset?`
    );
    if (!confirmWipe) return;

    setIsWiping(true);
    try {
      await api.post(`/routers/${id}/wipe`);
      setErr("");
      alert("Router storage purged from local disk and S3. Resetting state...");
      loadFull();
    } catch (e: any) {
      setErr(e.message || "Wipe failed");
    } finally {
      setIsWiping(false);
    }
  }

  function copyPassword() {
    if (!initialPassword) return;
    navigator.clipboard.writeText(initialPassword);
    setCopiedPass(true);
    setTimeout(() => setCopiedPass(false), 2000);
  }

  if (err && !r) return <div className="card border-red-500/30 text-xs text-red-500 bg-red-500/10 p-4">{err}</div>;
  if (!r) return <p className="text-xs text-slate-500">Loading router details…</p>;

  const currentRuntime = realtime.runtimeState || r.runtime_state;
  const isStarting = currentRuntime === "starting" || realtime.busyAction === "start";
  const isStopping = currentRuntime === "stopping" || realtime.busyAction === "stop";
  const isRunning = currentRuntime === "running";
  const isStopped = currentRuntime === "stopped";

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400">
        <Link href="/routers" className="hover:text-brand font-medium transition">
          Routers
        </Link>
        <span>/</span>
        <span className="text-slate-800 dark:text-slate-200 font-semibold">{r.slug}</span>
      </div>

      {realtime.actionError && (
        <div className="card border-red-500/30 text-xs text-red-500 bg-red-500/10 p-3 flex items-center justify-between">
          <span>{realtime.actionError}</span>
        </div>
      )}

      {/* Main Header Card with Real-Time Interactivity */}
      <div className="card shadow-lg p-6 border border-black/10 dark:border-white/10">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <div className="flex items-center gap-2.5 flex-wrap">
              <h1 className="text-xl font-bold text-slate-900 dark:text-slate-100">{r.name}</h1>
              
              {/* Reactive Status Badge */}
              <span className={stateBadge(currentRuntime)}>
                {isStarting ? "● starting…" : isStopping ? "● stopping…" : `● ${currentRuntime}`}
              </span>

              {realtime.healthStatus === "healthy" && isRunning && (
                <span className="badge badge-green">● healthy</span>
              )}
              {realtime.healthStatus === "unhealthy" && isRunning && (
                <span className="badge badge-amber animate-pulse">● booting…</span>
              )}
            </div>
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-400 font-mono">
              <code className="text-brand font-medium">
                {r.endpoint_path.endsWith("/v1") ? r.endpoint_path : `${r.endpoint_path}/v1`}/…
              </code> · {r.adapter_type} ·{" "}
              {r.image_ref}
            </p>
            <div className="mt-2 flex flex-col gap-1 text-xs text-slate-500 dark:text-slate-400">
              <div className="flex items-center gap-2 flex-wrap pt-0.5">
                <span className="font-medium text-slate-600 dark:text-slate-300">OpenAI Base URL:</span>
                <code className="bg-brand/10 dark:bg-brand/20 border border-brand/20 dark:border-brand/30 px-2 py-0.5 rounded text-brand font-mono font-semibold text-xs">
                  {typeof window !== "undefined"
                    ? `${window.location.origin}${r.endpoint_path.endsWith("/v1") ? r.endpoint_path : `${r.endpoint_path}/v1`}`
                    : `${r.endpoint_path.endsWith("/v1") ? r.endpoint_path : `${r.endpoint_path}/v1`}`}
                </code>
                <button
                  type="button"
                  className="text-[11px] px-2 py-0.5 rounded border border-black/10 dark:border-white/10 hover:bg-black/5 dark:hover:bg-white/5 text-slate-600 dark:text-slate-400 font-mono transition"
                  onClick={() => {
                    const cleanPath = r.endpoint_path.endsWith("/v1") ? r.endpoint_path : `${r.endpoint_path}/v1`;
                    const base = typeof window !== "undefined" ? `${window.location.origin}${cleanPath}` : cleanPath;
                    navigator.clipboard.writeText(base);
                    setCopiedBaseUrl(true);
                    setTimeout(() => setCopiedBaseUrl(false), 2000);
                  }}
                >
                  {copiedBaseUrl ? "✓ Copied!" : "📋 Copy Base URL"}
                </button>
              </div>
              {realtime.targetAddr && (
                <p>
                  Target internal: <code className="font-mono text-slate-700 dark:text-slate-300">{realtime.targetAddr}</code>
                </p>
              )}
              {(panelUrl || realtime.nativePanelUrl) && (
                <div className="flex items-center gap-2 flex-wrap pt-1">
                  <span className="font-medium text-slate-600 dark:text-slate-300">Native Dashboard:</span>
                  {isRunning || currentRuntime === "running" ? (
                    <>
                      <a
                        className="text-brand hover:underline font-semibold font-mono text-xs inline-flex items-center gap-1.5 bg-brand/10 dark:bg-brand/20 text-brand px-2.5 py-1 rounded-md transition hover:bg-brand/20 dark:hover:bg-brand/30"
                        target="_blank"
                        rel="noreferrer"
                        href={panelUrl || realtime.nativePanelUrl}
                      >
                        <span>Open Native Panel ↗</span>
                      </a>
                      <button
                        type="button"
                        className="text-[11px] px-2 py-0.5 rounded border border-black/10 dark:border-white/10 hover:bg-black/5 dark:hover:bg-white/5 text-slate-600 dark:text-slate-400 font-mono transition"
                        onClick={() => {
                          const url = panelUrl || realtime.nativePanelUrl;
                          if (url) {
                            navigator.clipboard.writeText(url);
                            setCopiedUrl(true);
                            setTimeout(() => setCopiedUrl(false), 2000);
                          }
                        }}
                      >
                        {copiedUrl ? "✓ Copied!" : "📋 Copy URL"}
                      </button>
                    </>
                  ) : (
                    <span className="text-xs text-slate-400 italic font-mono">
                      {isStarting ? "Launching container & running migrations..." : "Available when router is running & healthy"}
                    </span>
                  )}
                </div>
              )}
            </div>
          </div>

          {/* Interactive Button Bar */}
          <div className="flex flex-wrap gap-2 items-center">
            {/* Start Button */}
            <button
              className="btn-primary text-xs shadow-sm flex items-center gap-1.5 transition-all"
              disabled={isRunning || isStarting || isStopping || isWiping}
              onClick={realtime.handleStart}
            >
              {isStarting && <span className="inline-block animate-spin text-xs">⏳</span>}
              <span>{isStarting ? "Starting…" : "▶ Start"}</span>
            </button>

            {/* Restart Button */}
            <button
              className="btn-secondary text-xs flex items-center gap-1.5 transition-all"
              disabled={!isRunning || isStarting || isStopping || isWiping || realtime.busyAction === "restart"}
              onClick={realtime.handleRestart}
            >
              {realtime.busyAction === "restart" && <span className="inline-block animate-spin text-xs">🔄</span>}
              <span>{realtime.busyAction === "restart" ? "Restarting…" : "🔄 Restart"}</span>
            </button>

            {/* Stop / Cancel Button */}
            <button
              className="btn-danger text-xs flex items-center gap-1.5 transition-all"
              disabled={(isStopped && !isStarting) || isStopping || isWiping}
              onClick={realtime.handleStop}
            >
              {isStopping && <span className="inline-block animate-spin text-xs">⏳</span>}
              <span>{isStarting ? "⏹ Cancel Startup" : isStopping ? "Stopping…" : "⏹ Stop"}</span>
            </button>

            {/* Discover Models Button */}
            <button
              className="btn-ghost text-xs flex items-center gap-1.5 transition-all"
              disabled={!isRunning || isStarting || isStopping || isWiping || realtime.busyAction === "discover"}
              onClick={realtime.handleDiscover}
            >
              {realtime.busyAction === "discover" && <span className="inline-block animate-spin text-xs">🔍</span>}
              <span>{realtime.busyAction === "discover" ? "Discovering…" : "🔍 Discover Models"}</span>
            </button>

            {/* Delete Router Button */}
            <button
              className="btn-danger text-xs bg-red-800/80 hover:bg-red-700"
              disabled={isStarting || isStopping || isWiping}
              onClick={() => setShowDeleteModal(true)}
            >
              🗑️ Delete Router
            </button>
          </div>
        </div>
      </div>

      {/* Failure State Alert Banner */}
      {currentRuntime === "failed" && (
        <div className="card p-4 shadow-sm border bg-red-500/10 dark:bg-red-500/15 border-red-500/30 text-red-600 dark:text-red-400">
          <div className="flex items-center gap-3">
            <div className="text-2xl">⚠️</div>
            <div>
              <h4 className="text-xs font-bold uppercase tracking-wider text-red-700 dark:text-red-300">
                Container Startup Failed
              </h4>
              <p className="text-xs mt-0.5 text-red-600 dark:text-red-400">
                The container could not be started. Check the Docker daemon and system logs below for exact details, or click <strong>▶ Start</strong> to retry.
              </p>
            </div>
          </div>
        </div>
      )}

      {/* Native Dashboard Credentials & Factory Reset Banner for OmniRoute and 9Router */}
      {(r.adapter_type === "omniroute" || r.adapter_type === "9router") && (
        <div className={`card p-4 shadow-sm border ${
          initialPassword
            ? "bg-amber-500/5 dark:bg-amber-500/10 border-amber-500/20"
            : "bg-brand/5 dark:bg-brand/10 border-brand/20"
        }`}>
          <div className="flex flex-wrap items-center justify-between gap-4">
            <div className="flex items-center gap-3">
              <div className={`grid h-9 w-9 place-items-center rounded-xl text-lg ${
                initialPassword ? "bg-amber-500/10 text-amber-600 dark:text-amber-400" : "bg-brand/10 text-brand"
              }`}>
                {initialPassword ? "🔑" : "🧙‍♂️"}
              </div>
              <div>
                <div className={`text-xs font-bold uppercase tracking-wider ${
                  initialPassword ? "text-amber-900 dark:text-amber-200" : "text-brand font-semibold"
                }`}>
                  {initialPassword ? "Native Dashboard Login Credentials" : "Initial Setup Wizard Ready"}
                </div>
                {initialPassword ? (
                  <div className="flex items-center gap-2 mt-0.5 text-xs text-slate-600 dark:text-slate-300 flex-wrap">
                    <span>Initial Admin Password:</span>
                    <code className="px-2 py-0.5 rounded bg-black/5 dark:bg-white/10 font-mono font-bold text-amber-600 dark:text-amber-400 text-xs border border-amber-500/30 select-all">
                      {initialPassword}
                    </code>
                    {isDefaultPassword && (
                      <span className="text-[11px] text-amber-600/80 italic font-sans">(OmniRoute fallback)</span>
                    )}
                  </div>
                ) : (
                  <p className="text-xs text-slate-600 dark:text-slate-400 mt-0.5">
                    No predefined password set. Open the Native Dashboard to complete initial setup and create your admin account.
                  </p>
                )}
              </div>
            </div>

            <div className="flex items-center gap-2">
              {initialPassword ? (
                <button
                  type="button"
                  onClick={copyPassword}
                  className="btn-secondary text-xs flex items-center gap-1.5 py-1.5 px-3"
                >
                  <span>{copiedPass ? "✓" : "📋"}</span>
                  <span>{copiedPass ? "Copied" : "Copy Password"}</span>
                </button>
              ) : null}
              {panelUrl && (
                <a
                  href={panelUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="btn-primary text-xs flex items-center gap-1.5 py-1.5 px-3"
                >
                  <span>Open Panel ↗</span>
                </a>
              )}

              <button
                type="button"
                disabled={isWiping || isStarting || isStopping}
                onClick={handleWipe}
                className="btn text-xs bg-red-500/10 hover:bg-red-500/20 text-red-600 dark:text-red-400 border border-red-500/30 flex items-center gap-1.5 py-1.5 px-3 transition"
                title="Purge SQLite state from local disk and Cellar S3, restarting completely fresh into setup wizard"
              >
                <span>{isWiping ? "🔄" : "🧹"}</span>
                <span>{isWiping ? "Wiping..." : "Wipe & Fresh Reset"}</span>
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Headless API Gateway Card for FreeLLMAPI */}
      {r.adapter_type === "freellmapi" && (
        <div className="card p-4 shadow-sm border bg-brand/5 dark:bg-brand/10 border-brand/20">
          <div className="flex flex-wrap items-center justify-between gap-4">
            <div className="flex items-center gap-3">
              <div className="grid h-9 w-9 place-items-center rounded-xl text-lg bg-brand/10 text-brand">
                ⚡
              </div>
              <div>
                <div className="text-xs font-bold uppercase tracking-wider text-brand">
                  Headless OpenAI API Bridge Active
                </div>
                <p className="text-xs text-slate-600 dark:text-slate-400 mt-0.5">
                  FreeLLMAPI is a headless aggregator (pure OpenAI-compatible REST backend without a web GUI). Point your AI chat apps and OpenAI SDKs directly to the Base URL.
                </p>
              </div>
            </div>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => {
                  const cleanPath = r.endpoint_path.endsWith("/v1") ? r.endpoint_path : `${r.endpoint_path}/v1`;
                  const curlCmd = `curl ${typeof window !== "undefined" ? window.location.origin : ""}${cleanPath}/models`;
                  navigator.clipboard.writeText(curlCmd);
                  setCopiedBaseUrl(true);
                  setTimeout(() => setCopiedBaseUrl(false), 2000);
                }}
                className="btn-secondary text-xs flex items-center gap-1.5 py-1.5 px-3 font-mono"
              >
                <span>📋</span>
                <span>Copy Test curl</span>
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Grid: Credentials + Discovered Models */}
      <div className="grid gap-6 md:grid-cols-2">
        {/* Credentials */}
        <div className="card shadow-md p-5 border border-black/10 dark:border-white/10">
          <h2 className="text-sm font-bold text-slate-900 dark:text-slate-100 flex items-center gap-2">
            <span>🔐</span>
            <span>Provider Credentials</span>
          </h2>
          <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
            Encrypted with AES-256-GCM envelope encryption. Write-only for security.
          </p>

          <ul className="mt-4 space-y-1.5">
            {creds.length === 0 && (
              <li className="text-xs text-slate-400 italic">No provider credentials saved yet.</li>
            )}
            {creds.map((c) => (
              <li
                key={c.id}
                className="flex items-center justify-between rounded-lg bg-black/5 dark:bg-white/5 px-3 py-2 text-xs border border-black/5 dark:border-white/5"
              >
                <span className="font-semibold text-slate-800 dark:text-slate-200 uppercase tracking-wider">{c.provider}</span>
                <button
                  className="text-xs text-red-500 hover:text-red-700 dark:hover:text-red-400 font-medium"
                  onClick={async () => {
                    if (!confirm(`Remove credential for "${c.provider}"?`)) return;
                    await api.del(`/routers/${id}/credentials/${encodeURIComponent(c.provider)}`);
                    loadFull();
                  }}
                >
                  Remove
                </button>
              </li>
            ))}
          </ul>

          <form onSubmit={saveCred} className="mt-4 space-y-2 pt-3 border-t border-black/10 dark:border-white/10">
            <input
              className="input text-xs"
              placeholder="Provider (e.g. openai, anthropic, groq)"
              value={credProvider}
              onChange={(e) => setCredProvider(e.target.value)}
            />
            <input
              className="input text-xs font-mono"
              type="password"
              placeholder="Provider API key"
              value={credKey}
              onChange={(e) => setCredKey(e.target.value)}
            />
            <button className="btn-primary w-full text-xs shadow-sm" type="submit">
              Save Provider Credential
            </button>
          </form>
        </div>

        {/* Models */}
        <div className="card shadow-md p-5 border border-black/10 dark:border-white/10 flex flex-col">
          <h2 className="text-sm font-bold text-slate-900 dark:text-slate-100 flex items-center gap-2">
            <span>🤖</span>
            <span>Discovered AI Models</span>
          </h2>
          <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
            {realtime.providersCount || r.providers_count} {(realtime.providersCount || r.providers_count) === 1 ? "provider" : "providers"} · {realtime.modelsCount || r.models_count} models discovered
          </p>

          <div className="mt-4 max-h-72 overflow-y-auto space-y-1.5 flex-1 pr-1">
            {models.length === 0 ? (
              <p className="text-xs text-slate-400 italic py-6 text-center">
                No models discovered yet. Start the router and click &quot;Discover Models&quot;.
              </p>
            ) : (
              models.map((m) => (
                <div
                  key={m.id}
                  className="flex items-center justify-between rounded-lg bg-black/5 dark:bg-white/5 px-3 py-2 text-xs border border-black/5 dark:border-white/5 hover:border-brand/40 transition"
                >
                  <span className="font-mono font-medium text-slate-800 dark:text-slate-200">{m.model_id}</span>
                  <span className="text-[10px] font-semibold text-slate-500 uppercase px-2 py-0.5 rounded bg-black/5 dark:bg-white/10">
                    {m.provider}
                  </span>
                </div>
              ))
            )}
          </div>
        </div>
      </div>

      {/* Environment Variables & Secrets */}
      <EnvironmentVariablesCard
        routerId={r.id}
        routerSlug={r.slug}
        adapterType={r.adapter_type}
        initialEnvVars={envVars}
        autoRestartEnabled={autoRestart}
        onUpdated={loadFull}
      />

      {/* Real-time Container Logs Console */}
      <div className="card shadow-md p-5 border border-black/10 dark:border-white/10">
        <div className="flex items-center justify-between mb-3 flex-wrap gap-2">
          <div className="flex items-center gap-3">
            <h2 className="text-sm font-bold text-slate-900 dark:text-slate-100 flex items-center gap-2">
              <span>🖥️</span>
              <span>Container Real-Time Logs</span>
            </h2>
            <div className="text-[11px] font-mono flex items-center gap-1.5">
              {realtime.wsStatus === "connected" ? (
                <span className="text-emerald-500 flex items-center gap-1 font-medium">
                  <span className="inline-block w-1.5 h-1.5 rounded-full bg-emerald-500"></span> Live WebSocket Stream
                </span>
              ) : (
                <span className="text-amber-500 flex items-center gap-1 font-medium animate-pulse">
                  <span className="inline-block w-1.5 h-1.5 rounded-full bg-amber-500"></span> Reconnecting…
                </span>
              )}
            </div>
          </div>

          <div className="flex items-center gap-2">
            <button
              type="button"
              className={`text-[11px] px-2.5 py-1 rounded font-mono border transition ${
                autoScrollLogs
                  ? "bg-brand/10 border-brand/30 text-brand font-medium"
                  : "border-black/10 dark:border-white/10 text-slate-500"
              }`}
              onClick={() => setAutoScrollLogs(!autoScrollLogs)}
            >
              {autoScrollLogs ? "✓ Auto-scroll ON" : "Auto-scroll OFF"}
            </button>
            <button
              type="button"
              className="btn-ghost text-xs px-2.5 py-1"
              onClick={realtime.clearLogs}
            >
              Clear
            </button>
          </div>
        </div>

        <div className="h-64 overflow-auto rounded-lg bg-[#090d16] p-3 text-xs font-mono text-slate-300 border border-black/10 dark:border-white/10 select-text space-y-0.5">
          {realtime.logs.length === 0 ? (
            <div className="text-slate-600 italic py-4">
              {isRunning
                ? "Listening for live container logs... output will appear here as container processes execute."
                : "Router container is stopped. Click 'Start' to launch container and view live booting logs."}
            </div>
          ) : (
            realtime.logs.map((line, idx) => (
              <div key={idx} className="whitespace-pre-wrap leading-relaxed opacity-90 hover:opacity-100 hover:text-white">
                {line}
              </div>
            ))
          )}
          <div ref={terminalEndRef} />
        </div>
      </div>

      {showDeleteModal && r && (
        <DeleteRouterModal
          router={r}
          onClose={() => setShowDeleteModal(false)}
          onDeleted={() => routerNav.push("/routers")}
        />
      )}
    </div>
  );
}
