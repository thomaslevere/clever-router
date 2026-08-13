import type { Metadata } from "next";
import "./globals.css";
import Shell from "./components/Shell";
import { ThemeProvider } from "./components/ThemeProvider";

export const metadata: Metadata = {
  title: "CleverRoute — AI Router Control Plane",
  description: "Self-hosted control plane for AI routers and gateways.",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className="dark" suppressHydrationWarning>
      <body className="transition-colors duration-200">
        <ThemeProvider>
          <Shell>{children}</Shell>
        </ThemeProvider>
      </body>
    </html>
  );
}
