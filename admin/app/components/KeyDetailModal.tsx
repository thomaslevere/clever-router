"use client";

import { useState } from "react";
import type { VirtualKey } from "../lib/types";

export default function KeyDetailModal({
  vkey,
  onClose,
}: {
  vkey: VirtualKey;
  onClose: () => void;
}) {
  const [copiedId, setCopiedId] = useState(false);

  const budgetPct = vkey.budget_cents
    ? Math.min(100, Math.round((vkey.spent_cents / vkey.budget_cents) * 100))
    : 0;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4">
      <div className="card w-full max-w-lg shadow-2xl border border-black/10 dark:border-white/10 p-6 bg-slate-900 text-slate-100">
        <div className="flex items-center justify-between border-b border-white/10 pb-4">
          <div className="flex items-center gap-2.5">
            <span className="grid h-9 w-9 place-items-center rounded-xl bg-brand/20 text-brand font-bold text-base border border-brand/30">
              🔑
            </span>
            <div>
              <h2 className="text-base font-bold text-white flex items-center gap-2">
                <span>{vkey.name}</span>
                <span className={vkey.status === "active" ? "badge-green" : "badge-gray"}>
                  {vkey.status}
                </span>
              </h2>
              <p className="text-[11px] text-slate-400 font-mono">
                ID: {vkey.id}
              </p>
            </div>
          </div>
          <button className="btn-ghost text-xs px-2.5 py-1" onClick={onClose}>
            ✕
          </button>
        </div>

        <div className="mt-5 space-y-4 text-xs">
          {/* Key ID & Copy */}
          <div>
            <label className="block text-[11px] font-semibold text-slate-400 uppercase tracking-wider mb-1">
              Virtual Key ID
            </label>
            <div className="flex items-center gap-2">
              <code className="block flex-1 rounded-lg border border-white/10 bg-black/40 p-2.5 font-mono text-slate-200 truncate">
                {vkey.id}
              </code>
              <button
                className="btn-primary text-xs px-3 py-2 shrink-0"
                onClick={() => {
                  navigator.clipboard.writeText(vkey.id);
                  setCopiedId(true);
                  setTimeout(() => setCopiedId(false), 2000);
                }}
              >
                {copiedId ? "Copied! ✓" : "Copy ID"}
              </button>
            </div>
          </div>

          {/* Budget & Spend Progress */}
          <div>
            <div className="flex items-center justify-between mb-1.5">
              <label className="text-[11px] font-semibold text-slate-400 uppercase tracking-wider">
                Spend Budget & Quota
              </label>
              <span className="font-mono text-slate-300 font-medium">
                {vkey.budget_cents ? `${vkey.spent_cents}¢ / ${vkey.budget_cents}¢ (${budgetPct}%)` : "Unlimited (∞)"}
              </span>
            </div>
            {vkey.budget_cents > 0 ? (
              <div className="h-2 w-full rounded-full bg-slate-800 overflow-hidden">
                <div
                  className={`h-full transition-all duration-300 ${
                    budgetPct >= 90
                      ? "bg-red-500"
                      : budgetPct >= 75
                      ? "bg-amber-500"
                      : "bg-emerald-500"
                  }`}
                  style={{ width: `${budgetPct}%` }}
                />
              </div>
            ) : (
              <p className="text-[11px] text-slate-400">No spend limit cap configured.</p>
            )}
          </div>

          {/* Rate Limits & Timestamps */}
          <div className="grid grid-cols-2 gap-3 pt-2">
            <div className="rounded-lg bg-white/5 p-3 border border-white/5">
              <span className="block text-[10px] text-slate-400 uppercase font-semibold">Rate Limit (RPM)</span>
              <span className="text-sm font-bold font-mono text-slate-200 mt-0.5 block">
                {vkey.rate_limit_rpm ? `${vkey.rate_limit_rpm} RPM` : "Unlimited (∞)"}
              </span>
            </div>

            <div className="rounded-lg bg-white/5 p-3 border border-white/5">
              <span className="block text-[10px] text-slate-400 uppercase font-semibold">Created Date</span>
              <span className="text-xs font-medium text-slate-300 mt-0.5 block">
                {new Date(vkey.created_at).toLocaleDateString()}
              </span>
            </div>
          </div>

          {/* Model Allowlist */}
          <div>
            <label className="block text-[11px] font-semibold text-slate-400 uppercase tracking-wider mb-1.5">
              Allowed AI Models
            </label>
            <div className="flex flex-wrap gap-1.5">
              {vkey.model_allowlist && vkey.model_allowlist.length > 0 ? (
                vkey.model_allowlist.map((m, i) => (
                  <span key={i} className="badge badge-blue font-mono text-[10px]">
                    {m}
                  </span>
                ))
              ) : (
                <span className="text-slate-400 italic">All models permitted (wildcard *)</span>
              )}
            </div>
          </div>

          {/* Router Allowlist */}
          <div>
            <label className="block text-[11px] font-semibold text-slate-400 uppercase tracking-wider mb-1.5">
              Allowed Router Slugs
            </label>
            <div className="flex flex-wrap gap-1.5">
              {vkey.router_allowlist && vkey.router_allowlist.length > 0 ? (
                vkey.router_allowlist.map((r, i) => (
                  <span key={i} className="badge badge-green font-mono text-[10px]">
                    {r}
                  </span>
                ))
              ) : (
                <span className="text-slate-400 italic">All routers permitted (wildcard *)</span>
              )}
            </div>
          </div>
        </div>

        <div className="flex justify-end pt-5 border-t border-white/10 mt-5">
          <button className="btn-secondary text-xs px-4 py-2" onClick={onClose}>
            Close Inspection Modal
          </button>
        </div>
      </div>
    </div>
  );
}
