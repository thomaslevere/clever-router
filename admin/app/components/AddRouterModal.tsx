"use client";

import { useState } from "react";
import { api } from "../lib/api";
import type { Router } from "../lib/types";

const adapters = [
  { value: "omniroute", label: "OmniRoute", image: "diegosouzapw/omniroute:latest" },
  { value: "litellm", label: "LiteLLM", image: "ghcr.io/berriai/litellm:main-stable" },
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
    image_ref: adapters[0].image,
    desired_state: "stopped",
  });
  const [usePreset, setUsePreset] = useState(false);
  const [adminPassword, setAdminPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  function pickAdapter(v: string) {
    const a = adapters.find((x) => x.value === v)!;
    setForm((f) => ({ ...f, adapter_type: v, image_ref: a.image || f.image_ref }));
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
              placeholder="e.g. omniroute-prod"
              value={form.slug}
              onChange={(e) =>
                setForm((f) => ({ ...f, slug: e.target.value.toLowerCase() }))
              }
            />
            <p className="mt-1 text-[11px] text-slate-500 dark:text-slate-400">
              Exposed namespaced endpoint at: <code className="text-brand">/{form.slug || "{slug}"}/v1/…</code>
            </p>
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 uppercase tracking-wider mb-1">
              Display Name
            </label>
            <input
              id="new-router-name"
              className="input"
              placeholder="e.g. OmniRoute Production Gateway"
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
