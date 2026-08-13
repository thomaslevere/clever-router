"use client";

import { useCallback, useEffect, useState } from "react";
import { api, UnauthorizedError } from "./lib/api";
import type { Router } from "./lib/types";
import RouterCard from "./components/RouterCard";
import AddRouterModal from "./components/AddRouterModal";

export default function DashboardPage() {
  const [routers, setRouters] = useState<Router[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState("");
  const [showAdd, setShowAdd] = useState(false);

  const load = useCallback(async () => {
    try {
      setRouters(await api.get<Router[]>("/routers"));
      setErr("");
    } catch (e: any) {
      if (e instanceof UnauthorizedError) {
        window.dispatchEvent(new Event("cr:auth-changed"));
        return;
      }
      setErr(e.message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [load]);

  // L-3 FIX: onCreated receives the newly created Router and immediately adds
  // it to state so the dashboard updates without waiting for the next poll.
  const handleCreated = useCallback((r: Router) => {
    setRouters((prev) => [...prev, r]);
    load(); // refresh to get server-authoritative state
  }, [load]);

  const serving = routers.filter((r) => r.runtime_state === "running").length;
  const healthy = routers.filter((r) => r.health_status === "healthy").length;
  const totalModels = routers.reduce((a, r) => a + r.models_count, 0);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Dashboard</h1>
          <p className="text-sm text-gray-400">
            Managed AI routers and gateways
          </p>
        </div>
        <button className="btn-primary" onClick={() => setShowAdd(true)}>
          + Add Router
        </button>
      </div>

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Routers" value={routers.length} />
        <Stat label="Serving" value={serving} accent="emerald" />
        <Stat label="Healthy" value={healthy} accent="emerald" />
        <Stat label="Models" value={totalModels} />
      </div>

      {err && (
        <div className="card border-red-500/30 text-sm text-red-300">{err}</div>
      )}

      {loading ? (
        <p className="text-sm text-gray-400">Loading…</p>
      ) : routers.length === 0 ? (
        <div className="card text-center">
          <p className="text-gray-400">No routers yet.</p>
          <button
            className="btn-primary mx-auto mt-3"
            onClick={() => setShowAdd(true)}
          >
            Add your first router
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
        <AddRouterModal
          onClose={() => setShowAdd(false)}
          onCreated={handleCreated}
        />
      )}
    </div>
  );
}

function Stat({
  label,
  value,
  accent,
}: {
  label: string;
  value: number;
  accent?: string;
}) {
  return (
    <div className="card">
      <div className="text-xs text-gray-400">{label}</div>
      <div
        className={
          "mt-1 text-2xl font-semibold " +
          (accent === "emerald" ? "text-emerald-400" : "text-white")
        }
      >
        {value}
      </div>
    </div>
  );
}
