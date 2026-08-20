"use client";

import { useState } from "react";
import { api } from "../lib/api";
import type { Router } from "../lib/types";

const adapters = [
  { value: "omniroute", label: "OmniRoute", image: "diegosouzapw/omniroute:latest" },
  { value: "openconnector", label: "OpenConnector (OOMOL Lab)", image: "ghcr.io/oomol-lab/open-connector:latest" },
  { value: "llmgateway", label: "LLM Gateway (TheOpenCo)", image: "ghcr.io/theopenco/llmgateway-unified:latest" },
  { value: "coai", label: "CoAI Gateway (Chat Nio)", image: "coaidev/coai:latest" },
  { value: "bifrost", label: "Bifrost (Maxim AI)", image: "maximhq/bifrost:latest" },
  { value: "new-api", label: "New-API (QuantumNous)", image: "calciumion/new-api:latest" },
  { value: "portkey", label: "Portkey AI Gateway", image: "portkeyai/gateway:latest" },
  { value: "litellm", label: "LiteLLM", image: "ghcr.io/berriai/litellm:main-stable" },
  { value: "9router", label: "9Router", image: "decolua/9router:latest" },
  { value: "freellmapi", label: "FreeLLMAPI", image: "ghcr.io/tashfeenahmed/freellmapi:latest" },
  { value: "custom", label: "Custom Gateway", image: "" },
];

export default function AddRouterModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (r: Router) => void;
}) {
  const [form, setForm] = useState({
    slug: "",
    name: "",
    adapter_type: "omniroute",
    endpoint_path: "",
    image_ref: adapters[0].image,
    desired_state: "stopped",
  });
  const [usePreset, setUsePreset] = useState(false);
  const [adminPassword, setAdminPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  function pickAdapter(v: string) {
    const a = adapters.find((x) => x.value === v)!;
    setForm((f) => ({
      ...f,
      adapter_type: v,
      image_ref: a.image || f.image_ref,
      slug: f.slug === "" || ["omniroute", "openconnector", "llmgateway", "coai", "bifrost", "new-api", "portkey", "9router", "freellmapi", "litellm"].includes(f.slug) ? v : f.slug,
      endpoint_path: f.endpoint_path === "" || ["/omniroute", "/openconnector", "/llmgateway", "/coai", "/bifrost", "/new-api", "/portkey", "/9router", "/freellmapi", "/litellm", "/omniroute/v1", "/openconnector/v1", "/llmgateway/v1", "/coai/v1", "/bifrost/v1", "/new-api/v1", "/portkey/v1", "/9router/v1", "/freellmapi/v1", "/litellm/v1"].includes(f.endpoint_path) ? `/${v}` : f.endpoint_path,
    }));
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const env_vars =
        usePreset && form.adapter_type === "omniroute" && adminPassword.trim() !== ""
          ? [
              { key: "INITIAL_PASSWORD", value: adminPassword.trim(), is_secret: true },
            ]
          : [];

      const r = await api.post<Router>("/routers", {
        slug: form.slug,
        name: form.name || form.slug,
        adapter_type: form.adapter_type,
        provider_type: form.adapter_type,
        endpoint_path: form.endpoint_path || "/" + form.slug,
        route_path: form.endpoint_path || "/" + form.slug,
        image_ref: form.image_ref,
        desired_state: form.desired_state,
        env_vars,
      });
      onCreated(r);
      onClose();
    } catch (e: any) {
      setErr(e.message || "failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4">
      <div className="card w-full max-w-lg shadow-2xl border border-black/10 dark:border-white/10 p-6">
        <div className="flex items-center justify-between border-b border-black/10 dark:border-white/10 pb-4">
          <div className="flex items-center gap-2.5">
            <span className="grid h-8 w-8 place-items-center rounded-lg bg-brand text-white font-bold text-sm">
              ＋
            </span>
            <h2 className="text-base font-bold text-slate-900 dark:text-slate-100">
              Deploy AI Router Runtime
            </h2>
          </div>
          <button className="btn-ghost text-xs px-2.5 py-1" onClick={onClose}>
            ✕
          </button>
        </div>

        <form onSubmit={submit} className="mt-5 space-y-4">
          <div>
            <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
              Slug Identifier
            </label>
            <input
              id="new-router-slug"
              className="input"
              required
              placeholder="e.g. 9router or omniroute-prod"
              value={form.slug}
              onChange={(e) => {
                const s = e.target.value.toLowerCase();
                setForm((f) => ({
                  ...f,
                  slug: s,
                  endpoint_path: f.endpoint_path === "" || f.endpoint_path === "/" + f.slug ? (s ? "/" + s : "") : f.endpoint_path,
                }));
              }}
            />
            <p className="mt-1 text-[11px] text-slate-500 dark:text-slate-400">
              Identifier for internal routing and Docker container naming.
            </p>
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
              External Route Path
            </label>
            <input
              id="new-router-endpoint-path"
              className="input font-mono text-xs"
              placeholder="e.g. /9router or /9router/v1"
              value={form.endpoint_path || (form.slug ? "/" + form.slug : "")}
              onChange={(e) => setForm((f) => ({ ...f, endpoint_path: e.target.value }))}
            />
            <p className="mt-1 text-[11px] text-slate-500 dark:text-slate-400">
              Exposed gateway API path: <code className="text-brand">{form.endpoint_path || (form.slug ? "/" + form.slug : "/{slug}")}/v1/…</code>
            </p>
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
              Display Name
            </label>
            <input
              id="new-router-name"
              className="input"
              placeholder="e.g. 9Router Production Aggregator"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
                Adapter Engine
              </label>
              <select
                className="input cursor-pointer"
                value={form.adapter_type}
                onChange={(e) => pickAdapter(e.target.value)}
              >
                {adapters.map((a) => (
                  <option key={a.value} value={a.value}>
                    {a.label}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
                Initial State
              </label>
              <select
                className="input cursor-pointer"
                value={form.desired_state}
                onChange={(e) =>
                  setForm((f) => ({ ...f, desired_state: e.target.value }))
                }
              >
                <option value="stopped">Stopped (Manual Start)</option>
                <option value="running">Running (Start Now)</option>
              </select>
            </div>
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
              Docker Image Reference
            </label>
            <input
              className="input font-mono text-xs"
              required
              placeholder="diegosouzapw/omniroute:latest"
              value={form.image_ref}
              onChange={(e) =>
                setForm((f) => ({ ...f, image_ref: e.target.value }))
              }
            />
            <p className="mt-1 text-[11px] text-slate-500 dark:text-slate-400">
              Must match the server allowlist (<code className="text-slate-400">ALLOWED_IMAGES</code>).
            </p>
          </div>

          {form.adapter_type === "9router" && (
            <div className="space-y-3 rounded-lg bg-emerald-500/5 dark:bg-emerald-500/10 p-3.5 border border-emerald-500/20">
              <div className="flex items-center gap-2 font-semibold text-emerald-600 dark:text-emerald-400 text-xs">
                <span>⚡</span>
                <span>9Router High-Performance AI Gateway</span>
              </div>
              <p className="text-[11px] text-slate-600 dark:text-slate-300">
                Optimized for high-throughput proxying with RTK token compression, 3-tier fallback routing (Subscription → Cheap → Free), and OpenAI-compatible endpoints.
              </p>
              <div className="text-[11px] text-slate-500 dark:text-slate-400 flex items-center gap-2">
                <span>Multi-core acceleration:</span>
                <span className="font-mono text-emerald-600 dark:text-emerald-400 font-bold">12 vCPUs / 8 GB</span>
              </div>
            </div>
          )}

          {form.adapter_type === "openconnector" && (
            <div className="space-y-2 rounded-lg bg-teal-500/5 dark:bg-teal-500/10 p-3.5 border border-teal-500/20">
              <div className="flex items-center gap-2 font-semibold text-teal-600 dark:text-teal-400 text-xs">
                <span>🔌</span>
                <span>OpenConnector (OOMOL Lab)</span>
              </div>
              <p className="text-[11px] text-slate-600 dark:text-slate-300">
                Auth and tool gateway connecting 1,000+ SaaS providers and 10,000+ Actions to AI agents via HTTP, MCP, and OpenAPI. Automatic secret generation (<code className="font-mono text-teal-600 dark:text-teal-400">AUTH_SECRET</code>, <code className="font-mono text-teal-600 dark:text-teal-400">ENCRYPTION_KEY</code>) and S3 persistence enabled.
              </p>
              <div className="text-[11px] text-slate-500 dark:text-slate-400 flex items-center gap-2">
                <span>Multi-core acceleration:</span>
                <span className="font-mono text-teal-600 dark:text-teal-400 font-bold">12 vCPUs / 6 GB</span>
              </div>
            </div>
          )}

          {form.adapter_type === "llmgateway" && (
            <div className="space-y-2 rounded-lg bg-indigo-500/5 dark:bg-indigo-500/10 p-3.5 border border-indigo-500/20">
              <div className="flex items-center gap-2 font-semibold text-indigo-600 dark:text-indigo-400 text-xs">
                <span>🛡️</span>
                <span>LLM Gateway (TheOpenCo)</span>
              </div>
              <p className="text-[11px] text-slate-600 dark:text-slate-300">
                Universal AI gateway with multi-provider routing, fallbacks, cost analytics, and guardrails. Automatic secret generation (<code className="font-mono text-indigo-600 dark:text-indigo-400">AUTH_SECRET</code>) and S3 persistence enabled.
              </p>
              <div className="text-[11px] text-slate-500 dark:text-slate-400 flex items-center gap-2">
                <span>Multi-core acceleration:</span>
                <span className="font-mono text-indigo-600 dark:text-indigo-400 font-bold">12 vCPUs / 6 GB</span>
              </div>
            </div>
          )}

          {form.adapter_type === "coai" && (
            <div className="space-y-2 rounded-lg bg-sky-500/5 dark:bg-sky-500/10 p-3.5 border border-sky-500/20">
              <div className="flex items-center gap-2 font-semibold text-sky-600 dark:text-sky-400 text-xs">
                <span>⚡</span>
                <span>CoAI / Chat Nio Gateway</span>
              </div>
              <p className="text-[11px] text-slate-600 dark:text-slate-300">
                Enterprise AI gateway with multi-channel load balancing and model caching. Requires a MySQL database (<code className="font-mono text-sky-600 dark:text-sky-400">MYSQL_HOST</code>, <code className="font-mono text-sky-600 dark:text-sky-400">MYSQL_USER</code>, <code className="font-mono text-sky-600 dark:text-sky-400">MYSQL_PASSWORD</code>, <code className="font-mono text-sky-600 dark:text-sky-400">MYSQL_DB</code>) configured in the router&apos;s Environment Variables.
              </p>
              <div className="text-[11px] text-slate-500 dark:text-slate-400 flex items-center gap-2">
                <span>Multi-core acceleration:</span>
                <span className="font-mono text-sky-600 dark:text-sky-400 font-bold">12 vCPUs / 6 GB</span>
              </div>
            </div>
          )}

          {form.adapter_type === "freellmapi" && (
            <div className="space-y-2 rounded-lg bg-emerald-500/5 dark:bg-emerald-500/10 p-3.5 border border-emerald-500/20">
              <div className="flex items-center gap-2 font-semibold text-emerald-600 dark:text-emerald-400 text-xs">
                <span>🌐</span>
                <span>FreeLLMAPI AI Aggregator Engine</span>
              </div>
              <p className="text-[11px] text-slate-600 dark:text-slate-300">
                Aggregates free-tier AI providers (HuggingFace, Groq, Cohere, Cloudflare, etc.) with automatic rate-limit tracking, failover routing, and AES-256 encrypted credential storage.
              </p>
              <div className="text-[11px] text-slate-500 dark:text-slate-400 flex items-center gap-2">
                <span>Multi-core acceleration:</span>
                <span className="font-mono text-emerald-600 dark:text-emerald-400 font-bold">12 vCPUs / 8 GB</span>
              </div>
            </div>
          )}

          {form.adapter_type === "omniroute" && (
            <div className="space-y-3 rounded-lg bg-brand/5 dark:bg-brand/10 p-3.5 border border-brand/20">
              <div className="flex items-center gap-2 font-semibold text-brand text-xs">
                <span>🧙‍♂️</span>
                <span>Interactive Initial Setup Wizard (Default)</span>
              </div>
              <p className="text-[11px] text-slate-600 dark:text-slate-300">
                Starts with clean state and no predefined environment variables. On first launch, open the Native Dashboard to create your initial administrator credentials.
              </p>

              <div className="pt-2 border-t border-brand/15">
                <label className="flex items-center gap-2 text-xs text-slate-700 dark:text-slate-200 cursor-pointer select-none font-medium">
                  <input
                    type="checkbox"
                    checked={usePreset}
                    onChange={(e) => setUsePreset(e.target.checked)}
                    className="rounded border-slate-400 text-brand focus:ring-brand h-4 w-4"
                  />
                  <span>Bypass wizard with custom predefined admin password</span>
                </label>

                {usePreset && (
                  <div className="space-y-1.5 mt-2 pt-2 border-t border-black/5 dark:border-white/5">
                    <label className="block text-[11px] font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider">
                      Initial Admin Password
                    </label>
                    <input
                      className="input font-mono text-xs"
                      type="text"
                      placeholder="e.g. MySecurePassword123!"
                      value={adminPassword}
                      onChange={(e) => setAdminPassword(e.target.value)}
                    />
                  </div>
                )}
              </div>
            </div>
          )}

          {err && (
            <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-2.5 text-xs text-red-500">
              ⚠️ {err}
            </div>
          )}

          <div className="flex justify-end gap-2.5 pt-3 border-t border-black/10 dark:border-white/10">
            <button type="button" className="btn-ghost text-xs" onClick={onClose}>
              Cancel
            </button>
            <button
              id="modal-submit-btn"
              type="submit"
              className="btn-primary text-xs font-semibold shadow-md"
              disabled={busy}
            >
              {busy ? "Deploying Runtime…" : "Deploy Router"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
