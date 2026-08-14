"use client";

import { useCallback, useEffect, useState } from "react";
import { api, getToken, UnauthorizedError } from "../lib/api";
import type { FileItem, S3ObjectItem, SystemMetrics } from "../lib/types";

export default function StoragePage() {
  const [activeTab, setActiveTab] = useState<"s3" | "local" | "monitoring">("s3");
  const [metrics, setMetrics] = useState<SystemMetrics | null>(null);
  const [s3Objects, setS3Objects] = useState<S3ObjectItem[]>([]);
  const [localFiles, setLocalFiles] = useState<FileItem[]>([]);
  const [currentPath, setCurrentPath] = useState<string>("/tmp/clever_router_volumes");
  const [loading, setLoading] = useState<boolean>(false);
  const [err, setErr] = useState<string>("");
  const [notification, setNotification] = useState<{ msg: string; type: "success" | "error" } | null>(null);
  const [previewContent, setPreviewContent] = useState<string | null>(null);
  const [previewFileName, setPreviewFileName] = useState<string | null>(null);
  const [copied, setCopied] = useState<boolean>(false);
  const [syncing, setSyncing] = useState<boolean>(false);
  const [filterNamespace, setFilterNamespace] = useState<string>("all");

  const notify = (msg: string, type: "success" | "error" = "success") => {
    setNotification({ msg, type });
    setTimeout(() => setNotification(null), 4000);
  };

  const fetchMetrics = useCallback(async () => {
    try {
      const res = await api.get<SystemMetrics>("/storage/metrics");
      if (res) setMetrics(res);
    } catch (e: any) {
      if (e instanceof UnauthorizedError) {
        window.dispatchEvent(new Event("cr:auth-changed"));
      }
    }
  }, []);

  const fetchS3Objects = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.get<S3ObjectItem[]>("/storage/s3/objects");
      setS3Objects(Array.isArray(res) ? res : []);
      setErr("");
    } catch (e: any) {
      if (e instanceof UnauthorizedError) {
        window.dispatchEvent(new Event("cr:auth-changed"));
        return;
      }
      setErr(e.message || "Failed to load S3 objects");
    } finally {
      setLoading(false);
    }
  }, []);

  const fetchLocalFiles = useCallback(async (path: string) => {
    setLoading(true);
    try {
      const res = await api.get<FileItem[]>(`/storage/local/tree?path=${encodeURIComponent(path)}`);
      setLocalFiles(Array.isArray(res) ? res : []);
      setCurrentPath(path);
      setErr("");
    } catch (e: any) {
      if (e instanceof UnauthorizedError) {
        window.dispatchEvent(new Event("cr:auth-changed"));
        return;
      }
      setErr(e.message || "Failed to list local directory");
    } finally {
      setLoading(false);
    }
  }, []);

  const previewFile = async (path: string, name: string) => {
    try {
      const res = await fetch(`/admin/api/storage/local/file?path=${encodeURIComponent(path)}`, {
        headers: { Authorization: `Bearer ${getToken()}` },
      });
      if (res.ok) {
        const text = await res.text();
        setPreviewContent(text);
        setPreviewFileName(name);
        setCopied(false);
      } else {
        notify("Failed to read file preview", "error");
      }
    } catch (e: any) {
      notify(e.message || "Preview error", "error");
    }
  };

  const deleteLocalFile = async (path: string, name: string) => {
    if (!confirm(`Are you sure you want to delete ${name}?`)) return;
    try {
      await api.del(`/storage/local/file?path=${encodeURIComponent(path)}`);
      notify(`Deleted ${name}`);
      fetchLocalFiles(currentPath);
    } catch (e: any) {
      notify(e.message || "Delete failed", "error");
    }
  };

  const deleteS3Object = async (key: string) => {
    if (!confirm(`Are you sure you want to delete S3 snapshot "${key}"?`)) return;
    try {
      await api.del(`/storage/s3/object?key=${encodeURIComponent(key)}`);
      notify(`Deleted remote snapshot ${key}`);
      fetchS3Objects();
    } catch (e: any) {
      notify(e.message || "Failed to delete S3 snapshot", "error");
    }
  };

  const triggerManualSync = async (routerId: string = "") => {
    setSyncing(true);
    try {
      await api.post("/storage/s3/sync", { router_id: routerId });
      notify(routerId ? `Synced router ${routerId} to Cellar S3` : "Synced all volumes and DB to Cellar S3");
      fetchS3Objects();
    } catch (e: any) {
      notify(`Sync failed: ${e.message}`, "error");
    } finally {
      setSyncing(false);
    }
  };

  const triggerManualRestore = async (routerId: string) => {
    if (!confirm(`Restore volume for router ${routerId} from Cellar S3? This will overwrite local changes.`)) return;
    try {
      await api.post("/storage/s3/restore", { router_id: routerId });
      notify(`Restored router ${routerId} from Cellar S3`);
      fetchLocalFiles(currentPath);
    } catch (e: any) {
      notify(`Restore failed: ${e.message}`, "error");
    }
  };

  useEffect(() => {
    fetchMetrics();
    fetchS3Objects();
    fetchLocalFiles(currentPath);
    const interval = setInterval(fetchMetrics, 5000);
    return () => clearInterval(interval);
  }, [fetchMetrics, fetchS3Objects, fetchLocalFiles, currentPath]);

  // Navigate up one directory level
  const navigateUp = () => {
    const parts = currentPath.split("/").filter(Boolean);
    if (parts.length <= 1) return;
    parts.pop();
    const parent = "/" + parts.join("/");
    fetchLocalFiles(parent);
  };

  const filteredS3Objects = s3Objects.filter((obj) => {
    if (filterNamespace === "all") return true;
    return obj.namespace === filterNamespace;
  });

  const namespaces = Array.from(new Set(s3Objects.map((o) => o.namespace)));

  const formatBytes = (bytes: number) => {
    if (bytes === 0) return "0 B";
    const k = 1024;
    const sizes = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return (bytes / Math.pow(k, i)).toFixed(1) + " " + sizes[i];
  };

  return (
    <div className="space-y-6">
      {/* Toast Notification */}
      {notification && (
        <div
          className={`fixed top-4 right-4 z-50 px-4 py-2.5 rounded-xl text-sm font-medium shadow-xl border flex items-center gap-2 transition-all transform animate-in fade-in slide-in-from-top-2 ${
            notification.type === "success"
              ? "bg-emerald-500 text-white border-emerald-400"
              : "bg-red-500 text-white border-red-400"
          }`}
        >
          <span>{notification.type === "success" ? "✓" : "⚠"}</span>
          <span>{notification.msg}</span>
        </div>
      )}

      {/* Header & Status Indicator */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight flex items-center gap-2.5">
            <span className="text-amber-500">💾</span>
            <span>Storage & Cellar S3 Manager</span>
          </h1>
          <p className="text-sm text-slate-500 dark:text-slate-400 mt-1">
            Real-time diagnostics, local NVMe scratchpad browser, and Cellar S3 persistent backups
          </p>
        </div>

        <div className="flex items-center gap-3">
          <button
            onClick={() => triggerManualSync()}
            disabled={syncing}
            className="btn-primary flex items-center gap-1.5 shadow-sm"
          >
            <span>{syncing ? "🔄" : "☁️"}</span>
            <span>{syncing ? "Syncing..." : "Sync All to S3"}</span>
          </button>

          <div
            className={`px-3 py-1.5 rounded-full text-xs font-semibold flex items-center gap-2 border ${
              metrics?.s3_connected
                ? "bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-400 dark:border-emerald-800"
                : "bg-amber-50 text-amber-700 border-amber-200 dark:bg-amber-950/40 dark:text-amber-400 dark:border-amber-800"
            }`}
          >
            <span className={`w-2 h-2 rounded-full ${metrics?.s3_connected ? "bg-emerald-500" : "bg-amber-500"}`} />
            Cellar S3: {metrics?.s3_connected ? `Connected (${metrics?.s3_latency_ms}ms)` : "Local Mode"}
          </div>
        </div>
      </div>

      {err && (
        <div className="p-3.5 rounded-xl bg-red-500/10 border border-red-500/20 text-red-600 dark:text-red-400 text-sm flex items-center justify-between">
          <span>{err}</span>
          <button onClick={() => setErr("")} className="text-xs font-bold hover:underline">
            ✕
          </button>
        </div>
      )}

      {/* Tabs */}
      <div className="flex border-b border-black/10 dark:border-white/10 gap-6">
        <button
          onClick={() => setActiveTab("s3")}
          className={`pb-3 text-sm font-semibold flex items-center gap-2 border-b-2 transition ${
            activeTab === "s3"
              ? "border-brand text-brand font-bold"
              : "border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200"
          }`}
        >
          <span>☁️</span>
          <span>Cellar S3 Snapshots ({s3Objects.length})</span>
        </button>
        <button
          onClick={() => setActiveTab("local")}
          className={`pb-3 text-sm font-semibold flex items-center gap-2 border-b-2 transition ${
            activeTab === "local"
              ? "border-brand text-brand font-bold"
              : "border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200"
          }`}
        >
          <span>📁</span>
          <span>Local Volume Explorer</span>
        </button>
        <button
          onClick={() => setActiveTab("monitoring")}
          className={`pb-3 text-sm font-semibold flex items-center gap-2 border-b-2 transition ${
            activeTab === "monitoring"
              ? "border-brand text-brand font-bold"
              : "border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200"
          }`}
        >
          <span>📊</span>
          <span>System & Disk Diagnostics</span>
        </button>
      </div>

      {/* TAB 1: CELLAR S3 SNAPSHOTS */}
      {activeTab === "s3" && (
        <div className="space-y-4">
          <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 bg-black/5 dark:bg-white/5 p-3 rounded-xl border border-black/5 dark:border-white/5">
            <div className="flex items-center gap-2 text-xs">
              <span className="text-slate-500 font-medium">Filter Namespace:</span>
              <button
                onClick={() => setFilterNamespace("all")}
                className={`px-2.5 py-1 rounded-lg transition ${
                  filterNamespace === "all"
                    ? "bg-brand text-slate-950 font-bold shadow-sm"
                    : "bg-black/5 dark:bg-white/10 hover:bg-black/10"
                }`}
              >
                All ({s3Objects.length})
              </button>
              {namespaces.map((ns) => (
                <button
                  key={ns}
                  onClick={() => setFilterNamespace(ns)}
                  className={`px-2.5 py-1 rounded-lg transition ${
                    filterNamespace === ns
                      ? "bg-brand text-slate-950 font-bold shadow-sm"
                      : "bg-black/5 dark:bg-white/10 hover:bg-black/10"
                  }`}
                >
                  {ns}
                </button>
              ))}
            </div>

            <div className="flex items-center gap-3">
              <button
                onClick={fetchS3Objects}
                className="btn-secondary text-xs flex items-center gap-1.5 py-1 px-3"
              >
                <span className={loading ? "animate-spin" : ""}>🔄</span>
                <span>Refresh</span>
              </button>
            </div>
          </div>

          <div className="card overflow-hidden p-0 shadow-lg">
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="bg-black/5 dark:bg-white/5 text-xs uppercase font-semibold text-slate-500 dark:text-slate-400 border-b border-black/5 dark:border-white/5">
                  <tr>
                    <th className="px-4 py-3">Snapshot Key</th>
                    <th className="px-4 py-3">Namespace</th>
                    <th className="px-4 py-3">Size</th>
                    <th className="px-4 py-3">Last Modified</th>
                    <th className="px-4 py-3 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-black/5 dark:divide-white/5 font-mono text-xs">
                  {filteredS3Objects.length === 0 ? (
                    <tr>
                      <td colSpan={5} className="px-4 py-10 text-center text-slate-400 font-sans">
                        No snapshots found in Cellar S3. Click <strong>&quot;Sync All to S3&quot;</strong> to create your first backup.
                      </td>
                    </tr>
                  ) : (
                    filteredS3Objects.map((obj) => (
                      <tr key={obj.key} className="hover:bg-black/5 dark:hover:bg-white/5 transition">
                        <td className="px-4 py-3 font-semibold text-slate-900 dark:text-slate-100 flex items-center gap-2">
                          <span className="text-amber-500">🗄️</span>
                          <span className="truncate max-w-md">{obj.key}</span>
                        </td>
                        <td className="px-4 py-3">
                          <span className="px-2 py-0.5 rounded-md bg-amber-500/10 text-amber-600 dark:text-amber-400 font-sans font-medium text-[11px]">
                            {obj.namespace}
                          </span>
                        </td>
                        <td className="px-4 py-3 text-slate-700 dark:text-slate-300 font-sans">
                          {formatBytes(obj.size)}
                        </td>
                        <td className="px-4 py-3 text-slate-500 font-sans">
                          {new Date(obj.last_modified).toLocaleString()}
                        </td>
                        <td className="px-4 py-3 text-right space-x-2 font-sans">
                          <a
                            href={`/admin/api/storage/s3/download?key=${encodeURIComponent(obj.key)}&token=${getToken()}`}
                            download
                            className="inline-flex items-center gap-1 text-brand font-medium hover:underline"
                          >
                            <span>⬇️</span> Download
                          </a>
                          {obj.namespace !== "root" && obj.namespace !== "system-db" && (
                            <button
                              onClick={() => triggerManualRestore(obj.namespace)}
                              className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400 font-medium hover:underline ml-2"
                              title="Restore snapshot into local router container volume"
                            >
                              <span>♻️</span> Restore
                            </button>
                          )}
                          <button
                            onClick={() => deleteS3Object(obj.key)}
                            className="inline-flex items-center gap-1 text-red-500 hover:text-red-700 font-medium ml-2"
                            title="Delete snapshot from Cellar S3"
                          >
                            <span>🗑️</span> Delete
                          </button>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}

      {/* TAB 2: LOCAL VOLUME EXPLORER */}
      {activeTab === "local" && (
        <div className="space-y-4">
          {/* Breadcrumb & Path Bar */}
          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-black/5 dark:bg-white/5 p-3 rounded-xl border border-black/5 dark:border-white/5 text-xs font-mono">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-slate-400 font-sans">Directory:</span>
              <span className="font-bold text-slate-900 dark:text-slate-100 bg-black/5 dark:bg-white/10 px-2 py-0.5 rounded">
                {currentPath}
              </span>
            </div>

            <div className="flex items-center gap-2 flex-wrap font-sans">
              <button
                onClick={() => fetchLocalFiles("/tmp/clever_router_volumes")}
                className="text-brand hover:underline text-xs"
              >
                /volumes
              </button>
              <span className="text-slate-400">|</span>
              <button
                onClick={() => fetchLocalFiles("/tmp/data")}
                className="text-brand hover:underline text-xs"
              >
                /data
              </button>
              <span className="text-slate-400">|</span>
              <button
                onClick={navigateUp}
                className="btn-secondary py-1 px-2.5 text-xs flex items-center gap-1"
              >
                <span>⬆️</span>
                <span>Up Level</span>
              </button>
              <button
                onClick={() => fetchLocalFiles(currentPath)}
                className="btn-secondary py-1 px-2.5 text-xs flex items-center gap-1"
              >
                <span className={loading ? "animate-spin" : ""}>🔄</span>
                <span>Refresh</span>
              </button>
            </div>
          </div>

          <div className="card overflow-hidden p-0 shadow-lg">
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="bg-black/5 dark:bg-white/5 text-xs uppercase font-semibold text-slate-500 dark:text-slate-400 border-b border-black/5 dark:border-white/5">
                  <tr>
                    <th className="px-4 py-3">File / Folder Name</th>
                    <th className="px-4 py-3">Mode</th>
                    <th className="px-4 py-3">Size</th>
                    <th className="px-4 py-3">Modified</th>
                    <th className="px-4 py-3 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-black/5 dark:divide-white/5 font-mono text-xs">
                  {localFiles.length === 0 ? (
                    <tr>
                      <td colSpan={5} className="px-4 py-10 text-center text-slate-400 font-sans">
                        Empty directory or no files in current path.
                      </td>
                    </tr>
                  ) : (
                    localFiles.map((file) => (
                      <tr key={file.path} className="hover:bg-black/5 dark:hover:bg-white/5 transition">
                        <td className="px-4 py-3">
                          {file.is_dir ? (
                            <button
                              onClick={() => fetchLocalFiles(file.path)}
                              className="flex items-center gap-2 text-brand hover:underline font-bold text-left"
                            >
                              <span>📁</span>
                              <span>{file.name}</span>
                            </button>
                          ) : (
                            <div className="flex items-center gap-2 text-slate-800 dark:text-slate-200">
                              <span>📄</span>
                              <span>{file.name}</span>
                            </div>
                          )}
                        </td>
                        <td className="px-4 py-3 text-slate-500">{file.mode}</td>
                        <td className="px-4 py-3 text-slate-700 dark:text-slate-300 font-sans">
                          {file.is_dir ? "-" : formatBytes(file.size)}
                        </td>
                        <td className="px-4 py-3 text-slate-500 font-sans">
                          {new Date(file.mod_time).toLocaleString()}
                        </td>
                        <td className="px-4 py-3 text-right space-x-2 font-sans">
                          {!file.is_dir && (
                            <>
                              <button
                                onClick={() => previewFile(file.path, file.name)}
                                className="text-slate-600 dark:text-slate-300 hover:text-brand transition font-medium"
                                title="Preview Content"
                              >
                                👁️ Preview
                              </button>
                              <a
                                href={`/admin/api/storage/local/download?path=${encodeURIComponent(file.path)}&token=${getToken()}`}
                                download
                                className="text-brand hover:underline font-medium ml-2"
                                title="Download File"
                              >
                                ⬇️ Download
                              </a>
                            </>
                          )}
                          <button
                            onClick={() => deleteLocalFile(file.path, file.name)}
                            className="text-red-500 hover:text-red-700 font-medium ml-2"
                            title="Delete"
                          >
                            🗑️ Delete
                          </button>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}

      {/* TAB 3: MONITORING & SYSTEM DIAGNOSTICS */}
      {activeTab === "monitoring" && metrics && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
          {/* Scratch Disk Card */}
          <div className="card space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-bold flex items-center gap-2">
                <span>💽</span>
                <span>NVMe Scratch Volume (/tmp)</span>
              </h3>
              <span className="badge badge-amber text-[10px]">Active</span>
            </div>

            <div className="text-2xl font-black text-slate-900 dark:text-slate-100">
              {formatBytes(metrics.scratch_disk.used_bytes)}
              <span className="text-xs font-normal text-slate-500 ml-2">
                / {formatBytes(metrics.scratch_disk.total_bytes)}
              </span>
            </div>

            <div className="w-full bg-black/10 dark:bg-white/10 h-2.5 rounded-full overflow-hidden">
              <div
                className={`h-full rounded-full transition-all duration-500 ${
                  metrics.scratch_disk.used_pct > 80
                    ? "bg-red-500"
                    : metrics.scratch_disk.used_pct > 50
                    ? "bg-amber-500"
                    : "bg-emerald-500"
                }`}
                style={{ width: `${Math.min(100, Math.max(2, metrics.scratch_disk.used_pct))}%` }}
              />
            </div>

            <div className="flex justify-between text-xs text-slate-500 font-mono">
              <span>Used: {metrics.scratch_disk.used_pct.toFixed(1)}%</span>
              <span>Free: {formatBytes(metrics.scratch_disk.free_bytes)}</span>
            </div>
          </div>

          {/* Gateway Heap Card */}
          <div className="card space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-bold flex items-center gap-2">
                <span>⚡</span>
                <span>Gateway Runtime Heap</span>
              </h3>
              <span className="badge badge-green text-[10px]">Go Runtime</span>
            </div>

            <div className="text-2xl font-black text-slate-900 dark:text-slate-100">
              {metrics.alloc_mb} MB
              <span className="text-xs font-normal text-slate-500 ml-2">Allocated Heap</span>
            </div>

            <div className="text-xs font-mono text-slate-500 space-y-1.5 bg-black/5 dark:bg-white/5 p-3 rounded-lg">
              <div className="flex justify-between">
                <span>Total Sys Memory:</span>
                <span className="font-bold text-slate-700 dark:text-slate-300">{metrics.sys_mb} MB</span>
              </div>
              <div className="flex justify-between">
                <span>Active Goroutines:</span>
                <span className="font-bold text-slate-700 dark:text-slate-300">{metrics.num_goroutine}</span>
              </div>
              <div className="flex justify-between">
                <span>GC Cycles Completed:</span>
                <span className="font-bold text-slate-700 dark:text-slate-300">{metrics.num_gc}</span>
              </div>
            </div>
          </div>

          {/* S3 Latency Card */}
          <div className="card space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-bold flex items-center gap-2">
                <span>☁️</span>
                <span>Cellar S3 Bridge</span>
              </h3>
              <span
                className={`badge text-[10px] ${
                  metrics.s3_connected ? "badge-green" : "badge-red"
                }`}
              >
                {metrics.s3_connected ? "Online" : "Offline"}
              </span>
            </div>

            <div className="text-2xl font-black text-slate-900 dark:text-slate-100">
              {metrics.s3_connected ? `${metrics.s3_latency_ms} ms` : "Disconnected"}
            </div>

            <div className="text-xs font-mono text-slate-500 space-y-1.5 bg-black/5 dark:bg-white/5 p-3 rounded-lg">
              <div className="flex justify-between">
                <span>Endpoint:</span>
                <span className="font-bold text-slate-700 dark:text-slate-300">cellar-c2.services...</span>
              </div>
              <div className="flex justify-between">
                <span>Inotify Watcher:</span>
                <span className="font-bold text-emerald-600 dark:text-emerald-400">800ms Debounce</span>
              </div>
              <div className="flex justify-between">
                <span>Compression:</span>
                <span className="font-bold text-slate-700 dark:text-slate-300">Parallel Zstandard</span>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* File Preview Modal */}
      {previewContent !== null && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4 animate-in fade-in">
          <div className="card max-w-4xl w-full p-6 space-y-4 shadow-2xl border border-black/10 dark:border-white/10">
            <div className="flex items-center justify-between border-b border-black/10 dark:border-white/10 pb-3">
              <h4 className="text-sm font-mono font-bold flex items-center gap-2 text-slate-900 dark:text-slate-100">
                <span>📄</span>
                <span>{previewFileName}</span>
              </h4>
              <div className="flex items-center gap-2">
                <button
                  onClick={() => {
                    navigator.clipboard.writeText(previewContent);
                    setCopied(true);
                    setTimeout(() => setCopied(false), 2000);
                  }}
                  className="btn-secondary py-1 px-3 text-xs flex items-center gap-1"
                >
                  <span>{copied ? "✓" : "📋"}</span>
                  <span>{copied ? "Copied" : "Copy"}</span>
                </button>
                <button
                  onClick={() => setPreviewContent(null)}
                  className="btn-ghost py-1 px-2.5 text-xs text-slate-400 hover:text-slate-100"
                >
                  ✕
                </button>
              </div>
            </div>

            <pre className="p-4 bg-[#0a0f18] text-slate-100 rounded-xl text-xs font-mono max-h-[65vh] overflow-auto whitespace-pre-wrap leading-relaxed border border-white/5">
              {previewContent || "(Empty file)"}
            </pre>

            <div className="flex justify-end">
              <button
                onClick={() => setPreviewContent(null)}
                className="btn-secondary py-1.5 px-4 text-xs font-medium"
              >
                Close Preview
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
