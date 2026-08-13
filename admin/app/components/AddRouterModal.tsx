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
      const r = await api.post<Router>("/routers", {
        slug: form.slug,
        name: form.name || form.slug,
        adapter_type: form.adapter_type,
        image_ref: form.image_ref,
        desired_state: form.desired_state,
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
    <div className="fixed inset-0 z-30 flex items-center justify-center bg-black/60 p-4">
      <div className="card w-full max-w-lg">
        <div className="flex items-center justify-between">
          <h2 className="text-base font-semibold text-white">Add Router</h2>
          <button className="btn-ghost" onClick={onClose}>
            Close
          </button>
        </div>
        <form onSubmit={submit} className="mt-4 space-y-3">
          <div>
            <label className="text-xs text-gray-400">Slug</label>
            <input
              className="input mt-1"
              required
              placeholder="omniroute-prod"
              value={form.slug}
              onChange={(e) =>
                setForm((f) => ({ ...f, slug: e.target.value.toLowerCase() }))
              }
            />
            <p className="mt-1 text-xs text-gray-500">
              Exposed at <code>/{"{slug}"}/v1/...</code>
            </p>
          </div>
          <div>
            <label className="text-xs text-gray-400">Name</label>
            <input
              className="input mt-1"
              placeholder="OmniRoute Production"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
          </div>
          <div>
            <label className="text-xs text-gray-400">Adapter</label>
            <select
              className="input mt-1"
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
            <label className="text-xs text-gray-400">Image</label>
            <input
              className="input mt-1"
              required
              placeholder="diegosouzapw/omniroute:latest"
              value={form.image_ref}
              onChange={(e) =>
                setForm((f) => ({ ...f, image_ref: e.target.value }))
              }
            />
            <p className="mt-1 text-xs text-gray-500">
              Must be in the server ALLOWED_IMAGES allowlist.
            </p>
          </div>
          <div>
            <label className="text-xs text-gray-400">On create</label>
            <select
              className="input mt-1"
              value={form.desired_state}
              onChange={(e) =>
                setForm((f) => ({ ...f, desired_state: e.target.value }))
              }
            >
              <option value="stopped">Stopped (start manually)</option>
              <option value="running">Running (start now)</option>
            </select>
          </div>
          {err && <p className="text-sm text-red-400">{err}</p>}
          <div className="flex justify-end gap-2 pt-2">
            <button type="button" className="btn-ghost" onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className="btn-primary" disabled={busy}>
              {busy ? "Creating…" : "Create"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
