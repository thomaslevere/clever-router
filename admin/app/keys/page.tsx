"use client";

import { useCallback, useEffect, useState } from "react";
import { api, UnauthorizedError } from "../lib/api";
import type { VirtualKey } from "../lib/types";

export default function KeysPage() {
  const [keys, setKeys] = useState<VirtualKey[]>([]);
  const [err, setErr] = useState("");
  const [created, setCreated] = useState<string>("");
  const [form, setForm] = useState({
    name: "",
    budget_cents: 0,
    rate_limit_rpm: 0,
    model_allowlist: "",
    router_allowlist: "",
  });

  const load = useCallback(async () => {
    try {
      setKeys(await api.get<VirtualKey[]>("/keys"));
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
      setForm({ name: "", budget_cents: 0, rate_limit_rpm: 0, model_allowlist: "", router_allowlist: "" });
      load();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold text-white">Virtual Keys</h1>
        <p className="text-sm text-gray-400">
          Clients authenticate with these keys. Stored only as a hash.
        </p>
      </div>

      {created && (
        <div className="card border-emerald-500/30">
          <p className="text-sm text-emerald-300">
            Key created — copy it now (shown only once):
          </p>
          <code className="mt-2 block break-all rounded-lg bg-black/40 p-3 text-sm">
            {created}
          </code>
          <button className="btn-ghost mt-2" onClick={() => navigator.clipboard.writeText(created)}>
            Copy
          </button>
        </div>
      )}

      <div className="card">
        <h2 className="text-sm font-semibold text-white">Create key</h2>
        <form onSubmit={create} className="mt-3 grid gap-3 sm:grid-cols-2">
          <input className="input" placeholder="Name" required value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))} />
          <input className="input" type="number" placeholder="Budget (cents, 0=unlimited)"
            value={form.budget_cents}
            onChange={(e) => setForm((f) => ({ ...f, budget_cents: Number(e.target.value) }))} />
          <input className="input" type="number" placeholder="Rate limit (RPM, 0=unlimited)"
            value={form.rate_limit_rpm}
            onChange={(e) => setForm((f) => ({ ...f, rate_limit_rpm: Number(e.target.value) }))} />
          <input className="input" placeholder="Models (comma, empty=all)"
            value={form.model_allowlist}
            onChange={(e) => setForm((f) => ({ ...f, model_allowlist: e.target.value }))} />
          <input className="input sm:col-span-2" placeholder="Routers (comma slugs, empty=all)"
            value={form.router_allowlist}
            onChange={(e) => setForm((f) => ({ ...f, router_allowlist: e.target.value }))} />
          <div className="sm:col-span-2 flex justify-end">
            <button className="btn-primary" type="submit">Create key</button>
          </div>
        </form>
      </div>

      <div className="card">
        <h2 className="text-sm font-semibold text-white">Keys</h2>
        <table className="mt-3 w-full text-sm">
          <thead className="text-xs text-gray-400">
            <tr>
              <th className="text-left font-medium">Name</th>
              <th className="text-left font-medium">Budget</th>
              <th className="text-left font-medium">RPM</th>
              <th className="text-left font-medium">Status</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <tr key={k.id} className="border-t border-white/5">
                <td className="py-2 text-gray-200">{k.name}</td>
                <td className="py-2 text-gray-400">
                  {k.budget_cents ? `${k.spent_cents}/${k.budget_cents}c` : "∞"}
                </td>
                <td className="py-2 text-gray-400">
                  {k.rate_limit_rpm || "∞"}
                </td>
                <td className="py-2">
                  <span className={k.status === "active" ? "badge-green" : "badge-gray"}>
                    {k.status}
                  </span>
                </td>
                <td className="py-2 text-right">
                  {k.status === "active" && (
                    <button className="text-xs text-red-400 hover:underline"
                      onClick={async () => { await api.post(`/keys/${k.id}/revoke`); load(); }}>
                      revoke
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {err && <div className="card border-red-500/30 text-sm text-red-300">{err}</div>}
    </div>
  );
}
