"use client";

import { useCallback, useEffect, useState } from "react";
import { api, UnauthorizedError } from "../lib/api";
import type { AuditEntry } from "../lib/types";

export default function AuditPage() {
  const [rows, setRows] = useState<AuditEntry[]>([]);
  const [err, setErr] = useState("");

  const load = useCallback(async () => {
    try {
      const res = await api.get<AuditEntry[]>("/audit");
      setRows(Array.isArray(res) ? res : []);
      setErr("");
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
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [load]);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold text-white">Audit Log</h1>
        <p className="text-sm text-gray-400">
          Every credential and configuration change, recorded.
        </p>
      </div>
      <div className="card overflow-hidden p-0">
        <table className="w-full text-sm">
          <thead className="text-xs text-gray-400">
            <tr className="border-b border-white/10">
              <th className="px-4 py-2 text-left font-medium">Time</th>
              <th className="px-4 py-2 text-left font-medium">Actor</th>
              <th className="px-4 py-2 text-left font-medium">Action</th>
              <th className="px-4 py-2 text-left font-medium">Target</th>
              <th className="px-4 py-2 text-left font-medium">Details</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} className="border-b border-white/5">
                <td className="px-4 py-2 text-gray-400">
                  {new Date(r.ts).toLocaleString()}
                </td>
                <td className="px-4 py-2 text-gray-300">{r.actor}</td>
                <td className="px-4 py-2 text-gray-200">
                  <code>{r.action}</code>
                </td>
                <td className="px-4 py-2 text-gray-400">
                  {r.target_type}
                  {r.target_id ? `:${r.target_id.slice(0, 8)}` : ""}
                </td>
                <td className="px-4 py-2 text-gray-500">
                  {r.after ? JSON.stringify(r.after) : "-"}
                </td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-6 text-center text-gray-500">
                  No audit entries yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {err && <div className="card border-red-500/30 text-sm text-red-300">{err}</div>}
    </div>
  );
}
