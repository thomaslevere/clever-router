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
    <Link href={`/admin/routers/${r.id}`} className="card block hover:border-brand/50">
      <div className="flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h3 className="font-semibold text-white">{r.name}</h3>
            <span className={stateBadge(r.runtime_state)}>{r.runtime_state}</span>
            {healthy && <span className="badge-green">healthy</span>}
          </div>
          <p className="mt-0.5 text-xs text-gray-400">
            <code>{r.endpoint_path}/v1/...</code> · {r.adapter_type}
          </p>
        </div>
        <div className="text-right">
          <div className="text-sm text-gray-200">
            {r.providers_count} providers
          </div>
          <div className="text-xs text-gray-500">{r.models_count} models</div>
        </div>
      </div>
      <div className="mt-3 flex items-center gap-2 text-xs text-gray-500">
        <span className="truncate">{r.image_ref}</span>
      </div>
    </Link>
  );
}
