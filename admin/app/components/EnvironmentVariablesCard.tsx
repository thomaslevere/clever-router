"use client";

import React, { useState, useEffect } from "react";
import { api } from "../lib/api";
import type { EnvVariable } from "../lib/types";

interface Props {
  routerId: string;
  routerSlug: string;
  adapterType: string;
  initialEnvVars?: EnvVariable[];
  autoRestartEnabled?: boolean;
  onUpdated?: () => void;
}

export default function EnvironmentVariablesCard({
  routerId,
  routerSlug,
  adapterType,
  initialEnvVars = [],
  autoRestartEnabled = false,
  onUpdated,
}: Props) {
  const [envs, setEnvs] = useState<EnvVariable[]>(initialEnvVars);
  const [autoRestart, setAutoRestart] = useState<boolean>(autoRestartEnabled);
  const [showSecrets, setShowSecrets] = useState<{ [index: number]: boolean }>({});
  const [rawModalOpen, setRawModalOpen] = useState(false);
  const [rawTab, setRawTab] = useState<"import" | "export">("import");
  const [rawEnvText, setRawEnvText] = useState("");
  const [isSaving, setIsSaving] = useState(false);
  const [isRestarting, setIsRestarting] = useState(false);
  const [statusMsg, setStatusMsg] = useState<{ type: "success" | "error"; text: string } | null>(null);
  const [copiedExport, setCopiedExport] = useState(false);

  // Track if user has modified the form locally so background poll never resets in-progress typing
  const isDirty = React.useRef(false);

  // Sync state if props change, BUT NEVER if the user has active edits
  useEffect(() => {
    if (!isDirty.current) {
      setEnvs(initialEnvVars);
    }
  }, [initialEnvVars]);

  useEffect(() => {
    if (!isDirty.current) {
      setAutoRestart(autoRestartEnabled);
    }
  }, [autoRestartEnabled]);

  const addRow = () => {
    isDirty.current = true;
    setEnvs((prev) => [...prev, { key: "", value: "", is_secret: false }]);
  };

  const removeRow = (index: number) => {
    isDirty.current = true;
    setEnvs((prev) => prev.filter((_, i) => i !== index));
  };

  const updateField = (index: number, field: keyof EnvVariable, val: any) => {
    isDirty.current = true;
    setEnvs((prev) => {
      const updated = [...prev];
      const item = { ...updated[index], [field]: val };

      // Auto-detect secret flag when typing common secret keys
      if (field === "key" && typeof val === "string") {
        const isSecretKey = /SECRET|KEY|PASSWORD|TOKEN|AUTH|CREDENTIAL/i.test(val);
        if (isSecretKey && !item.is_secret) {
          item.is_secret = true;
        }
      }

      updated[index] = item;
      return updated;
    });
  };

  // Load baseline preset for OmniRoute
  const loadOmniRoutePreset = () => {
    isDirty.current = true;
    const preset: EnvVariable[] = [
      { key: "INITIAL_PASSWORD", value: "AdminSecurePassword123!", is_secret: true },
      { key: "DATA_DIR", value: "/app/data", is_secret: false },
      { key: "PORT", value: "20128", is_secret: false },
      { key: "NODE_ENV", value: "production", is_secret: false },
      { key: "STORAGE_ENCRYPTION_KEY_VERSION", value: "v1", is_secret: false },
      { key: "HTTP_PROXY", value: "", is_secret: false },
      { key: "HTTPS_PROXY", value: "", is_secret: false },
    ];

    // Merge preset without overriding existing non-empty keys
    const currentKeys = new Set(envs.map((e) => e.key.trim().toUpperCase()));
    const merged = [...envs];
    for (const p of preset) {
      if (!currentKeys.has(p.key)) {
        merged.push(p);
      }
    }
    setEnvs(merged);
    setStatusMsg({ type: "success", text: "OmniRoute preset variables added to list." });
    setTimeout(() => setStatusMsg(null), 3000);
  };

  // Load baseline preset for 9Router
  const load9RouterPreset = () => {
    isDirty.current = true;
    const preset: EnvVariable[] = [
      { key: "DATA_DIR", value: "/app/data", is_secret: false },
      { key: "PORT", value: "20128", is_secret: false },
      { key: "NODE_ENV", value: "production", is_secret: false },
      { key: "HOSTNAME", value: "0.0.0.0", is_secret: false },
      { key: "UV_THREADPOOL_SIZE", value: "12", is_secret: false },
      { key: "NODE_OPTIONS", value: "--max-old-space-size=4096", is_secret: false },
      { key: "GOMAXPROCS", value: "12", is_secret: false },
      { key: "WEB_CONCURRENCY", value: "auto", is_secret: false },
    ];

    const currentKeys = new Set(envs.map((e) => e.key.trim().toUpperCase()));
    const merged = [...envs];
    for (const p of preset) {
      if (!currentKeys.has(p.key)) {
        merged.push(p);
      }
    }
    setEnvs(merged);
    setStatusMsg({ type: "success", text: "9Router preset variables added to list." });
    setTimeout(() => setStatusMsg(null), 3000);
  };

  // Convert raw .env text to structured array
  const parseRawEnv = (text: string) => {
    isDirty.current = true;
    const lines = text.split("\n");
    const parsed: EnvVariable[] = [];
    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith("#")) continue;
      const idx = trimmed.indexOf("=");
      if (idx !== -1) {
        const key = trimmed.substring(0, idx).trim().toUpperCase();
        let value = trimmed.substring(idx + 1).trim();
        // Remove surrounding quotes if present
        if (
          (value.startsWith('"') && value.endsWith('"')) ||
          (value.startsWith("'") && value.endsWith("'"))
        ) {
          value = value.slice(1, -1);
        }
        const is_secret = /SECRET|KEY|PASSWORD|TOKEN|AUTH|CREDENTIAL/i.test(key);
        parsed.push({ key, value, is_secret });
      }
    }

    if (parsed.length === 0) {
      setStatusMsg({ type: "error", text: "No valid KEY=VALUE pairs found in raw .env." });
      return;
    }

    setEnvs(parsed);
    setRawModalOpen(false);
    setRawEnvText("");
    setStatusMsg({ type: "success", text: `Imported ${parsed.length} environment variables.` });
    setTimeout(() => setStatusMsg(null), 3000);
  };

  // Generate exported .env text
  const generateExportText = () => {
    const lines = [
      `# CleverRoute Environment Export for ${routerSlug || routerId}`,
      `# Exported at ${new Date().toISOString()}`,
      "",
    ];
    for (const env of envs) {
      if (!env.key) continue;
      if (env.is_secret) {
        lines.push(`${env.key}=******** # (Secret encrypted at rest)`);
      } else {
        lines.push(`${env.key}=${env.value}`);
      }
    }
    return lines.join("\n");
  };

  const handleSave = async (restartNow: boolean = false) => {
    if (restartNow) {
      setIsRestarting(true);
    } else {
      setIsSaving(true);
    }
    setStatusMsg(null);

    // Validate keys
    for (const env of envs) {
      const k = env.key.trim();
      if (k && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(k)) {
        setStatusMsg({
          type: "error",
          text: `Invalid key "${k}": keys must start with a letter/underscore and contain only alphanumeric characters and underscores.`,
        });
        setIsSaving(false);
        setIsRestarting(false);
        return;
      }
    }

    try {
      const res = await api.put(`/routers/${encodeURIComponent(routerId)}/env`, {
        env_vars: envs.filter((e) => e.key.trim() !== ""),
        auto_restart_on_env_change: autoRestart,
        restart_now: restartNow,
      });

      isDirty.current = false; // Reset dirty flag after successful save

      setStatusMsg({
        type: "success",
        text: restartNow
          ? "Environment variables saved. Container restart initiated! 🚀"
          : res.restart_triggered
          ? "Environment saved and automatic restart triggered."
          : "Environment variables saved securely.",
      });

      if (onUpdated) {
        onUpdated();
      }
    } catch (e: any) {
      setStatusMsg({ type: "error", text: e.message || "Failed to save environment variables." });
    } finally {
      setIsSaving(false);
      setIsRestarting(false);
    }
  };

  return (
    <div className="card shadow-md p-6 border border-black/10 dark:border-white/10">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-4 pb-4 border-b border-black/10 dark:border-white/10">
        <div>
          <div className="flex items-center gap-2.5">
            <span className="text-lg">⚙️</span>
            <h2 className="text-base font-bold text-slate-900 dark:text-slate-100">
              Environment Variables & Secrets
            </h2>
            <span className="text-xs px-2 py-0.5 rounded-full bg-brand/15 text-brand font-mono font-semibold">
              {envs.filter((e) => e.key.trim() !== "").length} vars
            </span>
          </div>
          <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
            Configure container runtime variables, AES-256-GCM encrypted secrets, and outbound proxies.
          </p>
        </div>

        <div className="flex items-center gap-2 flex-wrap">
          {adapterType === "omniroute" && (
            <button
              type="button"
              onClick={loadOmniRoutePreset}
              className="btn-ghost text-xs px-2.5 py-1.5 flex items-center gap-1.5 hover:border-brand/40"
              title="Load standard OmniRoute baseline presets"
            >
              <span>⚡</span>
              <span>OmniRoute Preset</span>
            </button>
          )}

          {adapterType === "9router" && (
            <button
              type="button"
              onClick={load9RouterPreset}
              className="btn-ghost text-xs px-2.5 py-1.5 flex items-center gap-1.5 hover:border-emerald-500/40 text-emerald-600 dark:text-emerald-400"
              title="Load high-performance 9Router multi-core presets"
            >
              <span>⚡</span>
              <span>9Router Preset</span>
            </button>
          )}

          <button
            type="button"
            onClick={() => {
              setRawTab("import");
              setRawModalOpen(true);
            }}
            className="btn-ghost text-xs px-2.5 py-1.5 flex items-center gap-1.5"
          >
            <span>📄</span>
            <span>Bulk .env</span>
          </button>

          <button
            type="button"
            onClick={addRow}
            className="btn-secondary text-xs px-3 py-1.5 flex items-center gap-1.5"
          >
            <span>＋</span>
            <span>Add Variable</span>
          </button>
        </div>
      </div>

      {/* Status banner */}
      {statusMsg && (
        <div
          className={`mt-4 p-3 rounded-lg text-xs font-medium border ${
            statusMsg.type === "success"
              ? "bg-emerald-500/10 border-emerald-500/20 text-emerald-600 dark:text-emerald-400"
              : "bg-red-500/10 border-red-500/20 text-red-600 dark:text-red-400"
          }`}
        >
          {statusMsg.type === "success" ? "✓ " : "⚠️ "}
          {statusMsg.text}
        </div>
      )}

      {/* Variables List */}
      <div className="mt-4 space-y-2.5">
        {envs.length === 0 ? (
          <div className="text-center py-8 border border-dashed border-black/10 dark:border-white/10 rounded-xl">
            <p className="text-xs text-slate-400">No environment variables defined for this router.</p>
            <div className="mt-3 flex justify-center gap-2">
              <button
                type="button"
                onClick={addRow}
                className="btn-ghost text-xs px-3 py-1"
              >
                ＋ Add First Variable
              </button>
              {adapterType === "omniroute" && (
                <button
                  type="button"
                  onClick={loadOmniRoutePreset}
                  className="btn-primary text-xs px-3 py-1 font-semibold"
                >
                  ⚡ Load OmniRoute Preset
                </button>
              )}
              {adapterType === "9router" && (
                <button
                  type="button"
                  onClick={load9RouterPreset}
                  className="btn-primary text-xs px-3 py-1 font-semibold bg-emerald-600 hover:bg-emerald-500"
                >
                  ⚡ Load 9Router Preset
                </button>
              )}
            </div>
          </div>
        ) : (
          <div className="space-y-2">
            <div className="grid grid-cols-12 gap-2 text-[11px] font-semibold text-slate-400 dark:text-slate-500 uppercase tracking-wider px-2">
              <div className="col-span-5">Variable Key</div>
              <div className="col-span-5">Value</div>
              <div className="col-span-1 text-center">Secret</div>
              <div className="col-span-1 text-right">Action</div>
            </div>

            {envs.map((env, index) => {
              const isMasked = env.is_secret && !showSecrets[index];
              return (
                <div
                  key={index}
                  className="grid grid-cols-12 gap-2 items-center rounded-lg bg-black/5 dark:bg-white/5 p-2 border border-black/5 dark:border-white/5 hover:border-brand/30 transition"
                >
                  {/* Key input */}
                  <div className="col-span-5">
                    <input
                      type="text"
                      placeholder="KEY (e.g. JWT_SECRET)"
                      value={env.key}
                      onChange={(e) => updateField(index, "key", e.target.value.toUpperCase())}
                      className="input font-mono text-xs py-1.5"
                    />
                  </div>

                  {/* Value input with secret eye toggle */}
                  <div className="col-span-5 relative">
                    <input
                      type={isMasked ? "password" : "text"}
                      placeholder={env.is_secret ? "Encrypted Secret Value" : "Value"}
                      value={env.value}
                      onChange={(e) => updateField(index, "value", e.target.value)}
                      className="input font-mono text-xs py-1.5 pr-8"
                    />
                    {env.is_secret && (
                      <button
                        type="button"
                        onClick={() =>
                          setShowSecrets((prev) => ({ ...prev, [index]: !prev[index] }))
                        }
                        className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 text-xs px-1"
                        title={showSecrets[index] ? "Mask secret" : "Reveal secret"}
                      >
                        {showSecrets[index] ? "🙈" : "👁️"}
                      </button>
                    )}
                  </div>

                  {/* Secret checkbox */}
                  <div className="col-span-1 flex justify-center">
                    <label className="flex items-center gap-1 cursor-pointer" title="Encrypt with AES-256-GCM at rest">
                      <input
                        type="checkbox"
                        checked={env.is_secret}
                        onChange={(e) => updateField(index, "is_secret", e.target.checked)}
                        className="rounded border-slate-400 text-brand focus:ring-brand cursor-pointer h-3.5 w-3.5"
                      />
                    </label>
                  </div>

                  {/* Remove button */}
                  <div className="col-span-1 flex justify-end">
                    <button
                      type="button"
                      onClick={() => removeRow(index)}
                      className="p-1 text-slate-400 hover:text-red-500 transition rounded hover:bg-red-500/10"
                      title="Remove variable"
                    >
                      🗑️
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* Footer controls & actions */}
      <div className="mt-6 pt-4 border-t border-black/10 dark:border-white/10 flex flex-wrap items-center justify-between gap-4">
        <label className="flex items-center gap-2 text-xs text-slate-600 dark:text-slate-300 cursor-pointer select-none">
          <input
            type="checkbox"
            checked={autoRestart}
            onChange={(e) => setAutoRestart(e.target.checked)}
            className="rounded border-slate-400 text-brand focus:ring-brand h-3.5 w-3.5"
          />
          <span>Auto-restart container on environment changes</span>
        </label>

        <div className="flex items-center gap-2">
          <button
            type="button"
            disabled={isSaving || isRestarting}
            onClick={() => handleSave(false)}
            className="btn-ghost text-xs px-3.5 py-2 font-medium"
          >
            {isSaving ? "Saving…" : "Save Variables"}
          </button>

          <button
            type="button"
            disabled={isSaving || isRestarting}
            onClick={() => handleSave(true)}
            className="btn-primary text-xs px-4 py-2 font-bold shadow-md flex items-center gap-1.5"
          >
            <span>{isRestarting ? "Restarting…" : "Save & Restart Router 🚀"}</span>
          </button>
        </div>
      </div>

      {/* Bulk .env Modal */}
      {rawModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4">
          <div className="card w-full max-w-xl shadow-2xl border border-black/10 dark:border-white/10 p-6 space-y-4">
            <div className="flex items-center justify-between border-b border-black/10 dark:border-white/10 pb-3">
              <div className="flex items-center gap-2">
                <span className="text-base font-bold text-slate-900 dark:text-slate-100">
                  Bulk .env Import / Export
                </span>
              </div>
              <button
                type="button"
                className="btn-ghost text-xs px-2 py-1"
                onClick={() => setRawModalOpen(false)}
              >
                ✕
              </button>
            </div>

            {/* Modal Tabs */}
            <div className="flex border-b border-black/10 dark:border-white/10">
              <button
                type="button"
                className={`px-4 py-2 text-xs font-semibold border-b-2 transition ${
                  rawTab === "import"
                    ? "border-brand text-brand"
                    : "border-transparent text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
                }`}
                onClick={() => setRawTab("import")}
              >
                📥 Import .env Content
              </button>
              <button
                type="button"
                className={`px-4 py-2 text-xs font-semibold border-b-2 transition ${
                  rawTab === "export"
                    ? "border-brand text-brand"
                    : "border-transparent text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
                }`}
                onClick={() => {
                  setRawTab("export");
                  setRawEnvText(generateExportText());
                }}
              >
                📤 Export .env Format
              </button>
            </div>

            {rawTab === "import" ? (
              <div className="space-y-3">
                <p className="text-xs text-slate-500 dark:text-slate-400">
                  Paste multi-line <code>KEY=VALUE</code> entries below. Secret keys (e.g. containing <code>KEY</code>, <code>SECRET</code>, <code>PASSWORD</code>) are automatically tagged for encryption.
                </p>
                <textarea
                  rows={10}
                  className="input font-mono text-xs leading-relaxed"
                  placeholder={`JWT_SECRET=super_secret_key\nINITIAL_PASSWORD=Admin123!\nHTTP_PROXY=http://10.0.0.1:8080\nPORT=20128`}
                  value={rawEnvText}
                  onChange={(e) => setRawEnvText(e.target.value)}
                />
                <div className="flex justify-end gap-2 pt-2">
                  <button
                    type="button"
                    className="btn-ghost text-xs"
                    onClick={() => setRawModalOpen(false)}
                  >
                    Cancel
                  </button>
                  <button
                    type="button"
                    className="btn-primary text-xs font-bold"
                    onClick={() => parseRawEnv(rawEnvText)}
                  >
                    Parse & Apply Variables
                  </button>
                </div>
              </div>
            ) : (
              <div className="space-y-3">
                <p className="text-xs text-slate-500 dark:text-slate-400">
                  Current configuration exported in standard <code>.env</code> format (secret values masked):
                </p>
                <textarea
                  readOnly
                  rows={10}
                  className="input font-mono text-xs leading-relaxed bg-black/5 dark:bg-white/5"
                  value={generateExportText()}
                />
                <div className="flex justify-end gap-2 pt-2">
                  <button
                    type="button"
                    className="btn-ghost text-xs"
                    onClick={() => setRawModalOpen(false)}
                  >
                    Close
                  </button>
                  <button
                    type="button"
                    className="btn-primary text-xs font-bold"
                    onClick={() => {
                      navigator.clipboard.writeText(generateExportText());
                      setCopiedExport(true);
                      setTimeout(() => setCopiedExport(false), 2000);
                    }}
                  >
                    {copiedExport ? "✓ Copied!" : "📋 Copy to Clipboard"}
                  </button>
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
