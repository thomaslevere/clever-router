"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { api, clearToken, getToken, getUser, setToken, setUser, type AdminUser } from "../lib/api";
import { useTheme } from "./ThemeProvider";

const navItems = [
  { href: "/", label: "Dashboard", icon: "📊" },
  { href: "/routers", label: "Routers", icon: "⚡" },
  { href: "/keys", label: "Virtual Keys", icon: "🔑" },
  { href: "/logs", label: "Live Logs", icon: "📜" },
  { href: "/terminal", label: "Terminal", icon: "💻" },
  { href: "/audit", label: "Audit Log", icon: "🛡️" },
];

export default function Shell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const { theme, toggleTheme } = useTheme();
  const [authed, setAuthed] = useState<boolean | null>(null);
  const [currentUser, setCurrentUser] = useState<AdminUser | null>(null);

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    const token = getToken();
    if (token) {
      setAuthed(true);
      setCurrentUser(getUser());
      // Validate session with backend and refresh user state
      api.get<{ username: string; role: string; user_id: string }>("/auth/me")
        .then((res) => {
          if (res && res.username) {
            const u = { username: res.username, role: res.role, id: res.user_id };
            setUser(u);
            setCurrentUser(u);
          }
        })
        .catch(() => {
          // Token expired or invalid
          clearToken();
          setAuthed(false);
          setCurrentUser(null);
        });
    } else {
      setAuthed(false);
    }
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
      <div className="flex min-h-screen items-center justify-center bg-[#070a0f]">
        <div className="h-9 w-9 animate-spin rounded-full border-2 border-brand/30 border-t-brand" />
      </div>
    );
  }

  if (!authed) {
    return (
      <div className="flex min-h-screen items-center justify-center p-6 bg-slate-50 dark:bg-[#070a0f] text-slate-900 dark:text-slate-100 transition-colors duration-200">
        <div className="card w-full max-w-md shadow-2xl border border-black/10 dark:border-white/10 p-8 backdrop-blur-xl">
          <div className="mb-6 flex items-center justify-between">
            <div className="flex items-center gap-3">
              <span className="grid h-11 w-11 place-items-center rounded-xl bg-brand text-white font-bold text-lg shadow-lg shadow-brand/30">
                CR
              </span>
              <div>
                <h1 className="text-xl font-bold leading-tight tracking-tight">
                  CleverRoute
                </h1>
                <p className="text-xs text-slate-500 dark:text-slate-400">
                  AI Router Control Plane
                </p>
              </div>
            </div>
            <button
              onClick={toggleTheme}
              aria-label="Toggle Theme"
              className="p-2 rounded-lg border border-black/10 dark:border-white/10 bg-black/5 dark:bg-white/5 hover:bg-black/10 dark:hover:bg-white/10 transition text-sm"
              title="Toggle light/dark theme"
            >
              {theme === "dark" ? "☀️" : "🌙"}
            </button>
          </div>

          <p className="mb-6 text-sm text-slate-500 dark:text-slate-400">
            Sign in with your administrator credentials to manage router runtimes.
          </p>

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
              <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 mb-1.5 uppercase tracking-wider">
                Username
              </label>
              <input
                id="login-username-input"
                className="input"
                type="text"
                autoComplete="username"
                placeholder="Username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
              />
            </div>

            <div>
              <label className="block text-xs font-semibold text-slate-600 dark:text-slate-300 mb-1.5 uppercase tracking-wider">
                Password
              </label>
              <input
                id="login-password-input"
                className="input"
                type="password"
                autoComplete="current-password"
                placeholder="Password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>

            {err && (
              <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-xs text-red-500 dark:text-red-400 flex items-center gap-2">
                <span>⚠️</span>
                <span>{err}</span>
              </div>
            )}

            <button
              id="login-submit-btn"
              className="btn-primary w-full py-2.5 font-medium flex items-center justify-center gap-2 shadow-lg"
              type="submit"
              disabled={submitting}
            >
              {submitting ? (
                <>
                  <div className="h-4 w-4 animate-spin rounded-full border-2 border-white/30 border-t-white" />
                  Signing in…
                </>
              ) : (
                "Sign In to Control Plane"
              )}
            </button>
          </form>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-slate-50 dark:bg-[#070a0f] text-slate-900 dark:text-slate-100 transition-colors duration-200">
      <header className="sticky top-0 z-30 border-b border-black/10 dark:border-white/10 bg-white/80 dark:bg-[#0b0f17]/80 backdrop-blur-md shadow-sm">
        <div className="mx-auto flex max-w-6xl items-center gap-6 px-6 py-3">
          <Link href="/" className="flex items-center gap-2.5 font-semibold group">
            <span className="grid h-8 w-8 place-items-center rounded-xl bg-brand text-white text-xs font-bold shadow-md shadow-brand/30 group-hover:scale-105 transition">
              CR
            </span>
            <span className="font-bold tracking-tight">CleverRoute</span>
          </Link>

          <nav className="flex items-center gap-1 text-sm font-medium">
            {navItems.map((n) => {
              const active =
                pathname === n.href ||
                (n.href !== "/" && pathname.startsWith(n.href));
              return (
                <Link
                  key={n.href}
                  href={n.href}
                  className={
                    "flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs transition " +
                    (active
                      ? "bg-brand/10 text-brand font-semibold dark:bg-white/10 dark:text-white"
                      : "text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-200 hover:bg-black/5 dark:hover:bg-white/5")
                  }
                >
                  <span className="text-sm">{n.icon}</span>
                  {n.label}
                </Link>
              );
            })}
          </nav>

          <div className="ml-auto flex items-center gap-3">
            <button
              onClick={toggleTheme}
              aria-label="Toggle Theme"
              className="p-1.5 rounded-lg border border-black/10 dark:border-white/10 bg-black/5 dark:bg-white/5 hover:bg-black/10 dark:hover:bg-white/10 transition text-sm flex items-center gap-1"
              title={theme === "dark" ? "Switch to Light Mode" : "Switch to Dark Mode"}
            >
              <span>{theme === "dark" ? "☀️" : "🌙"}</span>
              <span className="text-[11px] font-medium hidden sm:inline capitalize">{theme}</span>
            </button>

            {currentUser && (
              <span className="text-xs text-slate-600 dark:text-slate-300 bg-black/5 dark:bg-white/5 border border-black/10 dark:border-white/10 px-2.5 py-1 rounded-lg flex items-center gap-1.5">
                <span>👤</span>
                <span className="font-semibold">{currentUser.username}</span>
                {currentUser.role && (
                  <span className="text-[10px] text-brand uppercase font-mono tracking-wider">
                    ({currentUser.role})
                  </span>
                )}
              </span>
            )}

            <button
              id="sign-out-btn"
              className="btn-ghost text-xs py-1 px-2.5"
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
