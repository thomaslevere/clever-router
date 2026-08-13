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
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-white">Routers</h1>
        <button className="btn-primary" onClick={() => setShowAdd(true)}>
          + Add Router
        </button>
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        {routers.map((r) => (
          <RouterCard key={r.id} r={r} />
        ))}
      </div>
      {showAdd && (
        <AddRouterModal onClose={() => setShowAdd(false)} onCreated={() => load()} />
      )}
    </div>
  );
}
