export const API_BASE =
  process.env.NEXT_PUBLIC_API_BASE || "/admin/api";

export interface AdminUser {
  id?: string;
  username: string;
  email?: string;
  role: string;
}

export function getToken(): string {
  if (typeof window === "undefined") return "";
  return localStorage.getItem("cr_admin_token") || "";
}

export function setToken(t: string) {
  if (typeof window !== "undefined") localStorage.setItem("cr_admin_token", t);
}

export function clearToken() {
  if (typeof window !== "undefined") {
    localStorage.removeItem("cr_admin_token");
    localStorage.removeItem("cr_admin_user");
  }
}

export function getUser(): AdminUser | null {
  if (typeof window === "undefined") return null;
  const raw = localStorage.getItem("cr_admin_user");
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

export function setUser(u: AdminUser) {
  if (typeof window !== "undefined") {
    localStorage.setItem("cr_admin_user", JSON.stringify(u));
  }
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
    if (typeof window !== "undefined") {
      clearToken();
      window.dispatchEvent(new Event("cr:auth-changed"));
    }
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
  login: async (username: string, password: string): Promise<{ token: string; user: AdminUser }> => {
    const res = await fetch(`${API_BASE}/auth/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || `Login failed (HTTP ${res.status})`);
    }
    setToken(data.token);
    setUser(data.user);
    return data;
  },
  logout: async () => {
    try {
      await fetch(`${API_BASE}/auth/logout`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${getToken()}`,
        },
      });
    } catch {
      /* ignore */
    }
    clearToken();
  },
};

export function getRouterPanelUrl(router: { endpoint_path?: string; slug?: string; native_panel_url?: string; adapter_type?: string }): string {
  if (typeof window === "undefined") return "";
  const token = getToken();
  const slug = router.slug || (router.endpoint_path ? router.endpoint_path.replace(/^\//, "") : "");
  const base = window.location.origin;

  let targetPath = `/${slug}/open`;
  if (router.adapter_type === "llmgateway" || slug.toLowerCase().includes("llmgateway")) {
    targetPath = `/${slug}/open`;
  } else if (router.adapter_type === "coai" || slug.toLowerCase().includes("coai")) {
    targetPath = `/${slug}/open`;
  } else if (router.adapter_type === "bifrost" || slug.toLowerCase().includes("bifrost")) {
    targetPath = `/${slug}/open`;
  } else if (router.adapter_type === "portkey" || slug.toLowerCase().includes("portkey")) {
    targetPath = `/${slug}/open`;
  } else if (router.adapter_type === "litellm" || slug.toLowerCase().includes("litellm")) {
    targetPath = `/${slug}/ui/`;
  } else if (router.native_panel_url && router.native_panel_url.trim() !== "" && router.native_panel_url !== "/dashboard") {
    targetPath = router.native_panel_url.startsWith("http")
      ? router.native_panel_url
      : router.native_panel_url.startsWith("/")
      ? router.native_panel_url
      : `/${router.native_panel_url}`;
  }

  const url = targetPath.startsWith("http") ? new URL(targetPath) : new URL(targetPath, base);
  if (token && !url.searchParams.has("token")) {
    url.searchParams.set("token", token);
  }
  return url.toString();
}
