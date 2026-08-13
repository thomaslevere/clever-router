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
      const res = await api.get<Router[]>("/routers");
      setRouters(Array.isArray(res) ? res : []);
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

  const handleCreated = useCallback((r: Router) => {
    setRouters((prev) => [...prev, r]);
    load();
  }, [load]);

  const serving = routers.filter((r) => r.runtime_state === "running").length;
  const healthy = routers.filter((r) => r.health_status === "healthy").length;
  const totalModels = routers.reduce((a, r) => a + r.models_count, 0);

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Control Plane Dashboard</h1>
          <p className="text-sm text-slate-500 dark:text-slate-400 mt-1">
            Enterprise orchestration and hot routing for AI gateways
          </p>
        </div>
        <button
          id="add-router-btn"
          className="btn-primary flex items-center gap-1.5 shadow-md hover:shadow-glow-brand"
          onClick={() => setShowAdd(true)}
        >
          <span>＋</span>
          <span>Add Router</span>
        </button>
      </div>

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Total Routers" value={routers.length} icon="⚡" />
        <Stat label="Serving Runtimes" value={serving} accent="emerald" icon="🟢" />
        <Stat label="Healthy Backends" value={healthy} accent="emerald" icon="🛡️" />
        <Stat label="Discovered Models" value={totalModels} icon="🤖" />
      </div>

      {err && (
        <div className="card border-red-500/30 text-xs text-red-500 bg-red-500/10 p-3">
          {err}
        </div>
      )}

      {loading ? (
        <div className="card p-12 flex flex-col items-center justify-center gap-3 text-slate-400">
          <div className="h-7 w-7 animate-spin rounded-full border-2 border-brand/30 border-t-brand" />
          <p className="text-xs font-medium">Loading router topology…</p>
        </div>
      ) : routers.length === 0 ? (
        <div className="card p-12 text-center shadow-lg">
          <div className="mx-auto mb-3 grid h-12 w-12 place-items-center rounded-2xl bg-brand/10 text-brand text-2xl">
            ⚡
          </div>
          <h3 className="text-base font-semibold mb-1">No AI routers configured</h3>
          <p className="text-xs text-slate-500 dark:text-slate-400 max-w-sm mx-auto mb-4">
            Create your first managed router runtime to start routing traffic through stable OpenAI-compatible endpoints.
          </p>
          <button
            className="btn-primary mx-auto shadow-md"
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
  icon,
}: {
  label: string;
  value: number;
  accent?: string;
  icon?: string;
}) {
  return (
    <div className="card card-hover relative overflow-hidden group">
      <div className="flex items-center justify-between text-slate-500 dark:text-slate-400 text-xs font-medium">
        <span>{label}</span>
        {icon && <span className="text-sm opacity-70 group-hover:opacity-100 transition">{icon}</span>}
      </div>
      <div
        className={
          "mt-2 text-3xl font-bold tracking-tight " +
          (accent === "emerald"
            ? "text-emerald-500 dark:text-emerald-400"
            : "text-slate-900 dark:text-slate-100")
        }
      >
        {value}
      </div>
    </div>
  );
}
