import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "LitePSM Market - Universal AI Agent Capability Registry",
  description:
    "Discover, verify, and seamlessly install MCP servers, portable skills, and plugins for Claude Code, Codex, OpenCode, and many more.",
  icons: {
    icon: "/favicon.ico",
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body className="min-h-screen bg-[#f0f2f6] text-slate-900 antialiased bg-grid-pattern selection:bg-emerald-500 selection:text-black">
        {children}
      </body>
    </html>
  );
}
