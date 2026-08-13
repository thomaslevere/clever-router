/** @type {import('next').NextConfig} */
const nextConfig = {
  // Standalone output produces a minimal self-contained server — required for
  // the multi-stage Dockerfile that runs Next.js as an in-container child of
  // the Go gateway on Clever Cloud (single exposed port 8080).
  output: "standalone",
  // The gateway serves this UI behind /admin/* and forwards the full path, so
  // Next must treat /admin as its base path. The REST API lives at /admin/api/*.
  basePath: "/admin",
  reactStrictMode: true,
};

export default nextConfig;
