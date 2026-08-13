"use client";

import { useCallback, useEffect, useState } from "react";
import { api, UnauthorizedError } from "../lib/api";
import type { VirtualKey } from "../lib/types";

export default function KeysPage() {
  const [keys, setKeys] = useState<VirtualKey[]>([]);
  const [err, setErr] = useState("");
  const [created, setCreated] = useState<string>("");
  const [copied, setCopied] = useState(false);
  const [form, setForm] = useState({
    name: "",
    budget_cents: 0,
    rate_limit_rpm: 0,
    model_allowlist: "",
    router_allowlist: "",
  });

  const load = useCallback(async () => {
    try {
      const res = await api.get<VirtualKey[]>("/keys");
      setKeys(Array.isArray(res) ? res : []);
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

  async function create(e: React.FormEvent) {
    e.preventDefault();
    try {
      const res = await api.post<{ key: string }>("/keys", {
        name: form.name,
        budget_cents: Number(form.budget_cents) || 0,
        rate_limit_rpm: Number(form.rate_limit_rpm) || 0,
        model_allowlist: form.model_allowlist
          ? form.model_allowlist.split(",").map((s) => s.trim())
          : [],
        router_allowlist: form.router_allowlist
          ? form.router_allowlist.split(",").map((s) => s.trim())
          : [],
      });
      setCreated(res.key);
      setCopied(false);
      setForm({ name: "", budget_cents: 0, rate_limit_rpm: 0, model_allowlist: "", router_allowlist: "" });
      load();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Virtual Keys</h1>
        <p className="text-sm text-slate-500 dark:text-slate-400 mt-1">
          Issue scoped bearer keys (<code className="text-brand">Bearer cr-…</code>) for downstream AI clients and applications.
        </p>
      </div>

      {created && (
        <div className="card border-emerald-500/30 bg-emerald-500/5 p-5 shadow-lg">
          <div className="flex items-center gap-2 text-sm font-semibold text-emerald-600 dark:text-emerald-400">
            <span>✅</span>
            <span>Virtual Key Generated Successfully — Copy it now (shown once only):</span>
          </div>
          <div className="mt-3 flex items-center gap-2">
            <code className="block flex-1 break-all rounded-lg border border-black/10 dark:border-white/10 bg-black/20 dark:bg-black/50 p-3 text-xs font-mono text-emerald-400">
              {created}
            </code>
            <button
              className="btn-primary text-xs px-4 py-3 shrink-0 shadow-md"
              onClick={() => {
                navigator.clipboard.writeText(created);
                setCopied(true);
                setTimeout(() => setCopied(false), 2000);
              }}
            >
              {copied ? "Copied! ✓" : "Copy Key"}
            </button>
          </div>
        </div>
      )}

      {/* Create Key Form */}
      <div className="card p-6 shadow-md border border-black/10 dark:border-white/10">
        <h2 className="text-base font-bold mb-4 flex items-center gap-2">
          <span>🔑</span>
          <span>Generate New Virtual Key</span>
        </h2>
        <form onSubmit={create} className="grid gap-4 sm:grid-cols-2">
          <div>
            <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
              Key Name / Description
            </label>
            <input
              id="new-key-name"
              className="input"
              placeholder="e.g. production-app-frontend"
              required
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
              Spend Budget (cents, 0 = unlimited)
            </label>
            <input
              className="input"
              type="number"
              placeholder="0"
              value={form.budget_cents}
              onChange={(e) => setForm((f) => ({ ...f, budget_cents: Number(e.target.value) }))}
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
              Rate Limit (RPM, 0 = unlimited)
            </label>
            <input
              className="input"
              type="number"
              placeholder="0"
              value={form.rate_limit_rpm}
              onChange={(e) => setForm((f) => ({ ...f, rate_limit_rpm: Number(e.target.value) }))}
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
              Model Allowlist (comma separated)
            </label>
            <input
              className="input"
              placeholder="e.g. gpt-4o, claude-3-5-sonnet (empty = all)"
              value={form.model_allowlist}
              onChange={(e) => setForm((f) => ({ ...f, model_allowlist: e.target.value }))}
            />
          </div>

          <div className="sm:col-span-2">
            <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
              Router Allowlist (comma separated router slugs)
            </label>
            <input
              className="input"
              placeholder="e.g. omniroute-main, litellm-eu (empty = all)"
              value={form.router_allowlist}
              onChange={(e) => setForm((f) => ({ ...f, router_allowlist: e.target.value }))}
            />
          </div>

          <div className="sm:col-span-2 flex justify-end pt-2">
            <button id="create-key-submit-btn" className="btn-primary text-xs font-semibold shadow-md" type="submit">
              ＋ Generate Virtual Key
            </button>
          </div>
        </form>
      </div>

      {/* Keys Table */}
      <div className="card overflow-hidden p-0 shadow-lg border border-black/10 dark:border-white/10">
        <div className="p-4 border-b border-black/10 dark:border-white/10 bg-slate-100/70 dark:bg-white/[0.02]">
          <h2 className="text-sm font-bold text-slate-900 dark:text-slate-100">Active Virtual Keys</h2>
        </div>
        <table className="w-full text-xs">
          <thead className="border-b border-black/10 dark:border-white/10 text-slate-500 dark:text-slate-400 uppercase tracking-wider font-semibold">
            <tr>
              <th className="px-4 py-3 text-left">Key Name</th>
              <th className="px-4 py-3 text-left">Budget Usage</th>
              <th className="px-4 py-3 text-left">Rate Limit</th>
              <th className="px-4 py-3 text-left">Status</th>
              <th className="px-4 py-3 text-right">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-black/5 dark:divide-white/5">
            {keys.map((k) => (
              <tr key={k.id} className="hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition">
                <td className="px-4 py-3 font-semibold text-slate-800 dark:text-slate-200">
                  <div className="flex items-center gap-2">
                    <span>🔑</span>
                    <span>{k.name}</span>
                  </div>
                </td>
                <td className="px-4 py-3 text-slate-600 dark:text-slate-400 font-mono">
                  {k.budget_cents ? `${k.spent_cents}/${k.budget_cents}¢` : "Unlimited (∞)"}
                </td>
                <td className="px-4 py-3 text-slate-600 dark:text-slate-400 font-mono">
                  {k.rate_limit_rpm ? `${k.rate_limit_rpm} RPM` : "Unlimited (∞)"}
                </td>
                <td className="px-4 py-3">
                  <span className={k.status === "active" ? "badge-green" : "badge-gray"}>
                    {k.status}
                  </span>
                </td>
                <td className="px-4 py-3 text-right">
                  {k.status === "active" && (
                    <button
                      className="text-xs text-red-500 hover:text-red-700 dark:hover:text-red-400 font-semibold transition"
                      onClick={async () => {
                        if (confirm(`Revoke key "${k.name}"?`)) {
                          await api.post(`/keys/${k.id}/revoke`);
                          load();
                        }
                      }}
                    >
                      Revoke
                    </button>
                  )}
                </td>
              </tr>
            ))}
            {keys.length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-slate-400">
                  No virtual keys created yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {err && <div className="card border-red-500/30 text-xs text-red-500 bg-red-500/10 p-3">{err}</div>}
    </div>
  );
}
