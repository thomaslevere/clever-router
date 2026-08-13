"use client";

import Link from "next/link";
import type { Router } from "../lib/types";

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

export default function RouterCard({ r }: { r: Router }) {
  const healthy = r.health_status === "healthy";
  return (
    <Link
      href={`/routers/${r.id}`}
      className="card card-hover block border border-black/10 dark:border-white/10 group cursor-pointer"
    >
      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-2 flex-wrap">
            <h3 className="font-semibold text-base text-slate-900 dark:text-slate-100 group-hover:text-brand transition">
              {r.name}
            </h3>
            <span className={stateBadge(r.runtime_state)}>
              {r.runtime_state}
            </span>
            {healthy && (
              <span className="badge badge-green">
                ● healthy
              </span>
            )}
          </div>
          <p className="mt-1 text-xs text-slate-500 dark:text-slate-400 font-mono">
            <span className="text-brand font-medium">{r.endpoint_path}/v1/…</span> · {r.adapter_type}
          </p>
        </div>

        <div className="text-right shrink-0">
          <div className="text-xs font-semibold text-slate-800 dark:text-slate-200">
            {r.providers_count} {r.providers_count === 1 ? "provider" : "providers"}
          </div>
          <div className="text-[11px] text-slate-500 dark:text-slate-400">
            {r.models_count} {r.models_count === 1 ? "model" : "models"}
          </div>
        </div>
      </div>

      <div className="mt-4 pt-3 border-t border-black/5 dark:border-white/5 flex items-center justify-between text-[11px] text-slate-500 dark:text-slate-400">
        <span className="truncate max-w-[260px] font-mono text-[10px] bg-black/5 dark:bg-white/5 px-2 py-0.5 rounded">
          {r.image_ref}
        </span>
        <span className="text-brand font-medium group-hover:translate-x-0.5 transition inline-flex items-center gap-1">
          Manage Router →
        </span>
      </div>
    </Link>
  );
}
