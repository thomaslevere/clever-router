"use client";

import { useState } from "react";
import { api } from "../lib/api";
import type { Router } from "../lib/types";

export default function DeleteRouterModal({
  router,
  onClose,
  onDeleted,
}: {
  router: Router;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [confirmSlug, setConfirmSlug] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const isMatched = confirmSlug.trim().toLowerCase() === router.slug.toLowerCase();

  async function handleDelete(e: React.FormEvent) {
    e.preventDefault();
    if (!isMatched) return;
    setBusy(true);
    setErr("");
    try {
      await api.del(`/routers/${router.id}`);
      onDeleted();
      onClose();
    } catch (e: any) {
      setErr(e.message || "Failed to delete router");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4">
      <div className="card w-full max-w-md shadow-2xl border border-red-500/20 p-6 bg-slate-900 text-slate-100">
        <div className="flex items-center gap-3 border-b border-white/10 pb-4">
          <span className="grid h-10 w-10 place-items-center rounded-xl bg-red-500/20 text-red-400 font-bold text-lg border border-red-500/30">
            ⚠️
          </span>
          <div>
            <h2 className="text-base font-bold text-white">
              Delete Router &quot;{router.name}&quot;
            </h2>
            <p className="text-xs text-slate-400">
              This action permanently deletes the router configuration.
            </p>
          </div>
        </div>

        <form onSubmit={handleDelete} className="mt-4 space-y-4">
          <div className="rounded-lg bg-red-500/10 border border-red-500/20 p-3 text-xs text-red-300">
            <p className="font-semibold mb-1">Warning: Irreversible Action</p>
            <p>
              Stopping sibling container and deleting route <code className="font-mono text-red-200">/{router.slug}/v1/…</code>.
            </p>
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
              Type <span className="font-mono text-red-400 font-bold">{router.slug}</span> to confirm:
            </label>
            <input
              id="delete-confirm-input"
              className="input font-mono text-xs border-red-500/30 focus:border-red-500"
              placeholder={router.slug}
              value={confirmSlug}
              onChange={(e) => setConfirmSlug(e.target.value)}
              required
            />
          </div>

          {err && (
            <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-2.5 text-xs text-red-400">
              {err}
            </div>
          )}

          <div className="flex justify-end gap-2.5 pt-3 border-t border-white/10">
            <button type="button" className="btn-ghost text-xs" onClick={onClose}>
              Cancel
            </button>
            <button
              id="confirm-delete-btn"
              type="submit"
              disabled={!isMatched || busy}
              className="btn-danger text-xs font-semibold px-4 py-2 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {busy ? "Deleting Router…" : "Permanently Delete Router"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
