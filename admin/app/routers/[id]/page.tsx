"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, UnauthorizedError, getRouterPanelUrl } from "../../lib/api";
import type { Credential, EnvVariable, Model, Router } from "../../lib/types";
import DeleteRouterModal from "../../components/DeleteRouterModal";
import EnvironmentVariablesCard from "../../components/EnvironmentVariablesCard";

function stateBadge(s: string) {
  const map: Record<string, string> = {
    running: "badge-green",
    stopped: "badge-gray",
    failed: "badge-red",
    unhealthy: "badge-amber",
    starting: "badge-amber",
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
  const [busy, setBusy] = useState("");
  const [logs, setLogs] = useState("");
  const [logOn, setLogOn] = useState(false);
  const [showDeleteModal, setShowDeleteModal] = useState(false);
  const logAbort = useRef<AbortController | null>(null);

  const [credProvider, setCredProvider] = useState("");
  const [credKey, setCredKey] = useState("");
  const [panelUrl, setPanelUrl] = useState("");
  const [copiedUrl, setCopiedUrl] = useState(false);

  useEffect(() => {
    if (r) {
      setPanelUrl(getRouterPanelUrl(r));
    }
  }, [r]);

  const load = useCallback(async () => {
    try {
      const [router, ms, cs, envData] = await Promise.all([
        api.get<Router>(`/routers/${id}`),
        api.get<Model[]>(`/routers/${id}/models`).catch(() => []),
        api.get<Credential[]>(`/routers/${id}/credentials`).catch(() => []),
        api
          .get<{ env_vars: EnvVariable[]; auto_restart_on_env_change: boolean }>(`/routers/${id}/env`)
          .catch(() => ({ env_vars: [], auto_restart_on_env_change: false })),
      ]);
      setR(router);
      setModels(ms);
      setCreds(cs);
      if (envData) {
        setEnvVars(envData.env_vars || []);
        setAutoRestart(!!envData.auto_restart_on_env_change);
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
    load();
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [load]);

  async function act(name: string, path: string) {
    setBusy(name);
    try {
      await api.post(path);
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy("");
      setTimeout(load, 500);
    }
  }

  async function saveCred(e: React.FormEvent) {
    e.preventDefault();
    if (!credProvider || !credKey) return;
    try {
      await api.put(`/routers/${id}/credentials/${encodeURIComponent(credProvider)}`, {
        key: credKey,
      });
      setCredProvider("");
      setCredKey("");
      load();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  async function toggleLogs() {
    if (logOn) {
      logAbort.current?.abort();
      setLogOn(false);
      return;
    }
    setLogOn(true);
    setLogs("");
    const ac = new AbortController();
    logAbort.current = ac;
    try {
      const res = await fetch(
        `${process.env.NEXT_PUBLIC_API_BASE || "/admin/api"}/routers/${id}/logs`,
        { headers: { Authorization: `Bearer ${localStorage.getItem("cr_admin_token")}` }, signal: ac.signal },
      );
      const reader = res.body?.getReader();
      if (!reader) return;
      const dec = new TextDecoder();
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        setLogs((p) => (p + dec.decode(value)).slice(-20000));
      }
    } catch (e: any) {
      if (e.name !== "AbortError") setErr(e.message);
    } finally {
      setLogOn(false);
    }
  }

  useEffect(() => () => logAbort.current?.abort(), []);

  if (err && !r) return <div className="card border-red-500/30 text-xs text-red-500 bg-red-500/10 p-4">{err}</div>;
  if (!r) return <p className="text-xs text-slate-500">Loading router details…</p>;

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400">
        <Link href="/routers" className="hover:text-brand font-medium transition">
          Routers
        </Link>
        <span>/</span>
        <span className="text-slate-800 dark:text-slate-200 font-semibold">{r.slug}</span>
      </div>

      {/* Main Header Card */}
      <div className="card shadow-lg p-6 border border-black/10 dark:border-white/10">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <div className="flex items-center gap-2.5 flex-wrap">
              <h1 className="text-xl font-bold text-slate-900 dark:text-slate-100">{r.name}</h1>
              <span className={stateBadge(r.runtime_state)}>{r.runtime_state}</span>
              {r.health_status === "healthy" && (
                <span className="badge badge-green">● healthy</span>
              )}
            </div>
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-400 font-mono">
              <code className="text-brand font-medium">{r.endpoint_path}/v1/…</code> · {r.adapter_type} ·{" "}
              {r.image_ref}
            </p>
            {r.target_addr && (
              <div className="mt-2 flex flex-col gap-1 text-xs text-slate-500 dark:text-slate-400">
                <p>
                  Target internal: <code className="font-mono text-slate-700 dark:text-slate-300">{r.target_addr}</code>
                </p>
                {r.native_panel_url && (
                  <div className="flex items-center gap-2 flex-wrap pt-1">
                    <span className="font-medium text-slate-600 dark:text-slate-300">Native Dashboard:</span>
                    <a
                      className="text-brand hover:underline font-semibold font-mono text-xs inline-flex items-center gap-1.5 bg-brand/10 dark:bg-brand/20 text-brand px-2.5 py-1 rounded-md transition hover:bg-brand/20 dark:hover:bg-brand/30"
                      target="_blank"
                      rel="noreferrer"
                      href={panelUrl || getRouterPanelUrl(r)}
                      onClick={(e) => {
                        const url = getRouterPanelUrl(r);
                        if (url && url !== e.currentTarget.href) {
                          e.currentTarget.href = url;
                        }
                      }}
                    >
                      <span>Open Native Panel ↗</span>
                    </a>
                    <button
                      type="button"
                      className="text-[11px] px-2 py-0.5 rounded border border-black/10 dark:border-white/10 hover:bg-black/5 dark:hover:bg-white/5 text-slate-600 dark:text-slate-400 font-mono transition"
                      onClick={() => {
                        const url = getRouterPanelUrl(r);
                        if (url) {
                          navigator.clipboard.writeText(url);
                          setCopiedUrl(true);
                          setTimeout(() => setCopiedUrl(false), 2000);
                        }
                      }}
                    >
                      {copiedUrl ? "✓ Copied!" : "📋 Copy URL"}
                    </button>
                  </div>
                )}
              </div>
            )}
          </div>

          <div className="flex flex-wrap gap-2">
            <button
              className="btn-primary text-xs shadow-sm"
              disabled={busy === "start"}
              onClick={() => act("start", `/routers/${id}/start`)}
            >
              {busy === "start" ? "Starting…" : "▶ Start"}
            </button>
            <button
              className="btn-secondary text-xs"
              disabled={busy === "restart"}
              onClick={() => act("restart", `/routers/${id}/restart`)}
            >
              🔄 Restart
            </button>
            <button
              className="btn-danger text-xs"
              disabled={busy === "stop"}
              onClick={() => act("stop", `/routers/${id}/stop`)}
            >
              ⏹ Stop
            </button>
            <button
              className="btn-ghost text-xs"
              disabled={busy === "discover"}
              onClick={() => act("discover", `/routers/${id}/discover`)}
            >
              🔍 Discover Models
            </button>
            <button
              className="btn-danger text-xs bg-red-800/80 hover:bg-red-700"
              onClick={() => setShowDeleteModal(true)}
            >
              🗑️ Delete Router
            </button>
          </div>
        </div>
      </div>

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
                    load();
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
            {r.providers_count} {r.providers_count === 1 ? "provider" : "providers"} · {r.models_count} models discovered
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
        onUpdated={load}
      />

      {/* Container Output */}
      <div className="card shadow-md p-5 border border-black/10 dark:border-white/10">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-sm font-bold text-slate-900 dark:text-slate-100 flex items-center gap-2">
            <span>🖥️</span>
            <span>Container Stdout / Stderr Stream</span>
          </h2>
          <button className="btn-ghost text-xs px-3 py-1" onClick={toggleLogs}>
            {logOn ? "⏹ Disconnect" : "🔌 Attach Stream"}
          </button>
        </div>
        <pre className="h-60 overflow-auto rounded-lg bg-[#090d16] p-3 text-xs font-mono text-slate-300 border border-black/10 dark:border-white/10 select-text">
{logs || "Click 'Attach Stream' to inspect live container logs."}
        </pre>
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
