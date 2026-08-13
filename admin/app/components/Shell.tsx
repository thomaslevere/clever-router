"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { api, clearToken, getToken, getUser, setToken, setUser, type AdminUser } from "../lib/api";

const navItems = [
  { href: "/", label: "Dashboard" },
  { href: "/routers", label: "Routers" },
  { href: "/keys", label: "Virtual Keys" },
  { href: "/audit", label: "Audit Log" },
];

export default function Shell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const [authed, setAuthed] = useState<boolean | null>(null);
  const [currentUser, setCurrentUser] = useState<AdminUser | null>(null);

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    setAuthed(!!getToken());
    setCurrentUser(getUser());
  }, []);

  useEffect(() => {
    const handler = () => {
      setAuthed(!!getToken());
      setCurrentUser(getUser());
    };
    window.addEventListener("cr:auth-changed", handler);
    return () => window.removeEventListener("cr:auth-changed", handler);
  }, []);

  if (authed === null) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-white/20 border-t-brand" />
      </div>
    );
  }

  if (!authed) {
    return (
      <div className="flex min-h-screen items-center justify-center p-6 bg-[#070a0f]">
        <div className="card w-full max-w-md border border-white/10 shadow-2xl bg-[#0d121c]">
          <div className="mb-6 flex items-center gap-3">
            <span className="grid h-10 w-10 place-items-center rounded-xl bg-brand text-white font-bold text-base shadow-lg shadow-brand/20">
              CR
            </span>
            <div>
              <h1 className="text-xl font-bold text-white leading-tight">
                CleverRoute
              </h1>
              <p className="text-xs text-gray-400">AI Router Control Plane</p>
            </div>
          </div>

          <div className="mb-5 rounded-lg border border-brand/20 bg-brand/5 p-3 text-xs text-gray-300 space-y-1">
            <p className="font-semibold text-brand">Preseeded Admin Accounts:</p>
            <div className="grid grid-cols-2 gap-2 text-[11px] text-gray-400">
              <div>• User: <span className="text-gray-200 font-mono">salman</span></div>
              <div>• Pass: <span className="text-gray-200 font-mono">136517</span></div>
              <div>• User: <span className="text-gray-200 font-mono">azam</span></div>
              <div>• Pass: <span className="text-gray-200 font-mono">136517</span></div>
            </div>
          </div>

          <form
            onSubmit={async (e) => {
              e.preventDefault();
              const u = username.trim();
              const p = password.trim();
              if (!u) {
                setErr("Please enter a username");
                return;
              }
              if (!p) {
                // If only single field entered, allow master API key login
                setToken(u);
                setUser({ username: "admin", role: "owner" });
                setAuthed(true);
                setCurrentUser({ username: "admin", role: "owner" });
                setErr("");
                return;
              }
              setSubmitting(true);
              setErr("");
              try {
                const res = await api.login(u, p);
                setAuthed(true);
                setCurrentUser(res.user);
              } catch (error: any) {
                setErr(error.message || "Invalid username or password");
              } finally {
                setSubmitting(false);
              }
            }}
            className="space-y-4"
          >
            <div>
              <label className="block text-xs font-medium text-gray-300 mb-1.5">
                Username
              </label>
              <input
                id="login-username-input"
                className="input w-full bg-[#0b0f17] border-white/10 focus:border-brand"
                type="text"
                autoComplete="username"
                placeholder="Username (e.g. salman)"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-gray-300 mb-1.5">
                Password
              </label>
              <input
                id="login-password-input"
                className="input w-full bg-[#0b0f17] border-white/10 focus:border-brand"
                type="password"
                autoComplete="current-password"
                placeholder="Password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>

            {err && (
              <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-2.5 text-xs text-red-400">
                {err}
              </div>
            )}

            <button
              id="login-submit-btn"
              className="btn-primary w-full py-2.5 font-medium flex items-center justify-center gap-2"
              type="submit"
              disabled={submitting}
            >
              {submitting ? (
                <>
                  <div className="h-4 w-4 animate-spin rounded-full border-2 border-white/20 border-t-white" />
                  Signing in…
                </>
              ) : (
                "Sign in"
              )}
            </button>
          </form>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-[#070a0f]">
      <header className="sticky top-0 z-20 border-b border-white/10 bg-[#0b0f17]/80 backdrop-blur">
        <div className="mx-auto flex max-w-6xl items-center gap-6 px-6 py-3">
          <Link href="/" className="flex items-center gap-2 font-semibold">
            <span className="grid h-7 w-7 place-items-center rounded-lg bg-brand text-white text-xs font-bold shadow-sm shadow-brand/30">
              CR
            </span>
            <span className="text-white">CleverRoute</span>
          </Link>
          <nav className="flex items-center gap-1 text-sm">
            {navItems.map((n) => {
              const active =
                pathname === n.href ||
                (n.href !== "/" && pathname.startsWith(n.href));
              return (
                <Link
                  key={n.href}
                  href={n.href}
                  className={
                    "rounded-lg px-3 py-1.5 transition " +
                    (active
                      ? "bg-white/10 text-white font-medium"
                      : "text-gray-400 hover:text-gray-200")
                  }
                >
                  {n.label}
                </Link>
              );
            })}
          </nav>
          <div className="ml-auto flex items-center gap-3">
            {currentUser && (
              <span className="text-xs text-gray-400 bg-white/5 border border-white/10 px-2.5 py-1 rounded-md">
                👤 <span className="text-gray-200 font-medium">{currentUser.username}</span>
                {currentUser.role && (
                  <span className="ml-1 text-[10px] text-brand uppercase font-mono">({currentUser.role})</span>
                )}
              </span>
            )}
            <button
              id="sign-out-btn"
              className="btn-ghost text-xs"
              onClick={async () => {
                await api.logout();
                clearToken();
                window.dispatchEvent(new Event("cr:auth-changed"));
                setAuthed(false);
                setCurrentUser(null);
              }}
            >
              Sign out
            </button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-6xl px-6 py-6">{children}</main>
    </div>
  );
}
