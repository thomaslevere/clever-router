"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, UnauthorizedError } from "../../lib/api";
import type { Credential, Model, Router } from "../../lib/types";

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
  const id = params.id as string;
  const [r, setR] = useState<Router | null>(null);
  const [models, setModels] = useState<Model[]>([]);
  const [creds, setCreds] = useState<Credential[]>([]);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [logs, setLogs] = useState("");
  const [logOn, setLogOn] = useState(false);
  const logAbort = useRef<AbortController | null>(null);

  const [credProvider, setCredProvider] = useState("");
  const [credKey, setCredKey] = useState("");

  const load = useCallback(async () => {
    try {
      const [router, ms, cs] = await Promise.all([
        api.get<Router>(`/routers/${id}`),
        api.get<Model[]>(`/routers/${id}/models`).catch(() => []),
        api.get<Credential[]>(`/routers/${id}/credentials`).catch(() => []),
      ]);
      setR(router);
      setModels(ms);
      setCreds(cs);
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

  if (err && !r) return <p className="text-sm text-red-400">{err}</p>;
  if (!r) return <p className="text-sm text-gray-400">Loading…</p>;

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2 text-sm text-gray-400">
        <Link href="/admin/routers" className="hover:text-gray-200">
          Routers
        </Link>
        <span>/</span>
        <span className="text-gray-200">{r.slug}</span>
      </div>

      <div className="card">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-lg font-semibold text-white">{r.name}</h1>
              <span className={stateBadge(r.runtime_state)}>{r.runtime_state}</span>
              {r.health_status === "healthy" && (
                <span className="badge-green">healthy</span>
              )}
            </div>
            <p className="mt-1 text-xs text-gray-400">
              <code>{r.endpoint_path}/v1/...</code> · {r.adapter_type} ·{" "}
              {r.image_ref}
            </p>
            {r.target_addr && (
              <p className="mt-1 text-xs text-gray-500">
                target: <code>{r.target_addr}</code>
                {r.native_panel_url && (
                  <>
                    {" · "}
                    <a
                      className="text-brand hover:underline"
                      target="_blank"
                      rel="noreferrer"
                      href={r.native_panel_url}
                    >
                      Open native panel ↗
                    </a>
                  </>
                )}
              </p>
            )}
          </div>
          <div className="flex flex-wrap gap-2">
            <button
              className="btn-primary"
              disabled={busy === "start"}
              onClick={() => act("start", `/routers/${id}/start`)}
            >
              {busy === "start" ? "Starting…" : "Start"}
            </button>
            <button
              className="btn-ghost"
              disabled={busy === "restart"}
              onClick={() => act("restart", `/routers/${id}/restart`)}
            >
              Restart
            </button>
            <button
              className="btn-danger"
              disabled={busy === "stop"}
              onClick={() => act("stop", `/routers/${id}/stop`)}
            >
              Stop
            </button>
            <button
              className="btn-ghost"
              disabled={busy === "discover"}
              onClick={() => act("discover", `/routers/${id}/discover`)}
            >
              Discover models
            </button>
          </div>
        </div>
      </div>

      <div className="grid gap-6 md:grid-cols-2">
        <div className="card">
          <h2 className="text-sm font-semibold text-white">Provider credentials</h2>
          <p className="mt-1 text-xs text-gray-500">
            Encrypted at rest (AES-256-GCM). Write-only after save.
          </p>
          <ul className="mt-3 space-y-1">
            {creds.length === 0 && (
              <li className="text-xs text-gray-500">None configured.</li>
            )}
            {creds.map((c) => (
              <li
                key={c.id}
                className="flex items-center justify-between rounded-lg bg-black/20 px-3 py-2 text-sm"
              >
                <span className="text-gray-200">{c.provider}</span>
                <button
                  className="text-xs text-red-400 hover:underline"
                  onClick={async () => {
                    if (!confirm(`Remove credential for "${c.provider}"?`)) return;
                    await api.del(`/routers/${id}/credentials/${encodeURIComponent(c.provider)}`);
                    load();
                  }}
                >
                  remove
                </button>
              </li>
            ))}
          </ul>
          <form onSubmit={saveCred} className="mt-3 space-y-2">
            <input
              className="input"
              placeholder="provider (e.g. openai)"
              value={credProvider}
              onChange={(e) => setCredProvider(e.target.value)}
            />
            <input
              className="input"
              type="password"
              placeholder="API key"
              value={credKey}
              onChange={(e) => setCredKey(e.target.value)}
            />
            <button className="btn-primary w-full" type="submit">
              Save credential
            </button>
          </form>
        </div>

        <div className="card">
          <h2 className="text-sm font-semibold text-white">Models</h2>
          <p className="mt-1 text-xs text-gray-500">
            {r.providers_count} providers · {r.models_count} models discovered
          </p>
          <div className="mt-3 max-h-64 overflow-auto pr-1">
            {models.length === 0 ? (
              <p className="text-xs text-gray-500">
                No models yet. Start the router and click Discover models.
              </p>
            ) : (
              <ul className="space-y-1">
                {models.map((m) => (
                  <li
                    key={m.id}
                    className="flex items-center justify-between rounded-lg bg-black/20 px-3 py-1.5 text-xs"
                  >
                    <span className="text-gray-200">{m.model_id}</span>
                    <span className="text-gray-500">{m.provider}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </div>

      <div className="card">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-white">Live logs</h2>
          <button className="btn-ghost" onClick={toggleLogs}>
            {logOn ? "Stop" : "Attach"}
          </button>
        </div>
        <pre className="mt-3 h-64 overflow-auto rounded-lg bg-black/40 p-3 text-xs text-gray-300">
{logs || "Click Attach to stream container logs."}
        </pre>
      </div>
    </div>
  );
}
