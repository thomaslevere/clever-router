"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { clearToken, getToken, setToken } from "../lib/api";

const navItems = [
  { href: "/admin", label: "Dashboard" },
  { href: "/admin/routers", label: "Routers" },
  { href: "/admin/keys", label: "Virtual Keys" },
  { href: "/admin/audit", label: "Audit Log" },
];

export default function Shell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  // L-9 FIX: initialise authed from the token synchronously so the sign-in
  // form is never shown for a frame before hydration completes. The token read
  // is guarded for SSR via typeof window check inside getToken().
  const [authed, setAuthed] = useState<boolean | null>(null); // null = not hydrated
  const [tokenInput, setTokenInput] = useState("");
  const [err, setErr] = useState("");

  useEffect(() => {
    // Resolve auth state on first client render only.
    setAuthed(!!getToken());
  }, []);

  // When another tab clears the token or a 401 happens, reflect it.
  useEffect(() => {
    const handler = () => setAuthed(!!getToken());
    window.addEventListener("cr:auth-changed", handler);
    return () => window.removeEventListener("cr:auth-changed", handler);
  }, []);

  // Show nothing until we know whether the user is authenticated.
  // This prevents both the sign-in flash (L-9) and hydration mismatches.
  if (authed === null) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-white/20 border-t-brand" />
      </div>
    );
  }

  if (!authed) {
    return (
      <div className="flex min-h-screen items-center justify-center p-6">
        <div className="card w-full max-w-md">
          <div className="mb-5 flex items-center gap-3">
            <span className="grid h-9 w-9 place-items-center rounded-xl bg-brand text-white font-bold text-sm">
              CR
            </span>
            <div>
              <h1 className="text-lg font-semibold text-white leading-tight">
                CleverRoute
              </h1>
              <p className="text-xs text-gray-400">AI Router Control Plane</p>
            </div>
          </div>
          <p className="text-sm text-gray-400">
            Enter the admin API key to manage your AI routers.
          </p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const t = tokenInput.trim();
              if (!t) return;
              setToken(t);
              setAuthed(true);
              setErr("");
            }}
            className="mt-4 space-y-3"
          >
            <input
              id="admin-token-input"
              className="input"
              type="password"
              autoComplete="current-password"
              placeholder="Admin API key"
              value={tokenInput}
              onChange={(e) => setTokenInput(e.target.value)}
            />
            {err && <p className="text-sm text-red-400">{err}</p>}
            <button className="btn-primary w-full" type="submit">
              Sign in
            </button>
          </form>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-20 border-b border-white/10 bg-[#0b0f17]/80 backdrop-blur">
        <div className="mx-auto flex max-w-6xl items-center gap-6 px-6 py-3">
          <Link href="/admin" className="flex items-center gap-2 font-semibold">
            <span className="grid h-7 w-7 place-items-center rounded-lg bg-brand text-white text-xs font-bold">
              CR
            </span>
            <span className="text-white">CleverRoute</span>
          </Link>
          <nav className="flex items-center gap-1 text-sm">
            {navItems.map((n) => {
              const active =
                pathname === n.href || pathname.startsWith(n.href + "/");
              return (
                <Link
                  key={n.href}
                  href={n.href}
                  className={
                    "rounded-lg px-3 py-1.5 transition " +
                    (active
                      ? "bg-white/10 text-white"
                      : "text-gray-400 hover:text-gray-200")
                  }
                >
                  {n.label}
                </Link>
              );
            })}
          </nav>
          <button
            id="sign-out-btn"
            className="btn-ghost ml-auto"
            onClick={() => {
              clearToken();
              window.dispatchEvent(new Event("cr:auth-changed"));
              setAuthed(false);
            }}
          >
            Sign out
          </button>
        </div>
      </header>
      <main className="mx-auto max-w-6xl px-6 py-6">{children}</main>
    </div>
  );
}
