"use client";

import { useCallback, useEffect, useState } from "react";
import { api, UnauthorizedError } from "../lib/api";
import type { Router } from "../lib/types";
import RouterCard from "../components/RouterCard";
import AddRouterModal from "../components/AddRouterModal";

export default function RoutersPage() {
  const [routers, setRouters] = useState<Router[]>([]);
  const [showAdd, setShowAdd] = useState(false);

  const load = useCallback(async () => {
    try {
      const res = await api.get<Router[]>("/routers");
      setRouters(Array.isArray(res) ? res : []);
    } catch (e: any) {
      if (e instanceof UnauthorizedError) window.dispatchEvent(new Event("cr:auth-changed"));
    }
  }, []);

  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [load]);

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Managed Routers</h1>
          <p className="text-sm text-slate-500 dark:text-slate-400 mt-1">
            Active gateway runtimes, sibling containers, and routing proxies.
          </p>
        </div>
        <button className="btn-primary flex items-center gap-1.5 shadow-md hover:shadow-glow-brand" onClick={() => setShowAdd(true)}>
          <span>＋</span>
          <span>Add Router</span>
        </button>
      </div>

      {routers.length === 0 ? (
        <div className="card p-12 text-center shadow-lg">
          <p className="text-sm text-slate-500 dark:text-slate-400 mb-3">No routers configured yet.</p>
          <button className="btn-primary mx-auto" onClick={() => setShowAdd(true)}>
            Deploy Your First Router
          </button>
        </div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {routers.map((r) => (
            <RouterCard key={r.id} r={r} />
          ))}
        </div>
      )}

      {showAdd && (
        <AddRouterModal onClose={() => setShowAdd(false)} onCreated={() => load()} />
      )}
    </div>
  );
}
