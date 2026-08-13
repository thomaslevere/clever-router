export const API_BASE =
  process.env.NEXT_PUBLIC_API_BASE || "/admin/api";

export function getToken(): string {
  if (typeof window === "undefined") return "";
  return localStorage.getItem("cr_admin_token") || "";
}

export function setToken(t: string) {
  if (typeof window !== "undefined") localStorage.setItem("cr_admin_token", t);
}

export function clearToken() {
  if (typeof window !== "undefined")
    localStorage.removeItem("cr_admin_token");
}

async function req<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...opts,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${getToken()}`,
      ...(opts.headers || {}),
    },
  });
  if (res.status === 401) {
    throw new UnauthorizedError();
  }
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const j = await res.json();
      if (j.error) msg = j.error;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

export class UnauthorizedError extends Error {
  constructor() {
    super("unauthorized");
  }
}

export const api = {
  get: <T = any>(p: string) => req<T>(p),
  post: <T = any>(p: string, body?: any) =>
    req<T>(p, { method: "POST", body: body ? JSON.stringify(body) : undefined }),
  put: <T = any>(p: string, body?: any) =>
    req<T>(p, { method: "PUT", body: body ? JSON.stringify(body) : undefined }),
  patch: <T = any>(p: string, body?: any) =>
    req<T>(p, { method: "PATCH", body: body ? JSON.stringify(body) : undefined }),
  del: <T = any>(p: string) => req<T>(p, { method: "DELETE" }),
};
