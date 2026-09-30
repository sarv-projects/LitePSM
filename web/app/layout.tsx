import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "LitePSM Market - Universal AI Agent Capability Registry",
  description:
    "Discover, verify, and seamlessly install MCP servers, portable skills, and plugins across Cline, Pi Agent, Grok Build, Codex, and Claude Code.",
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
    <html lang="en" className="dark">
      <body className="min-h-screen bg-[#090a0f] text-gray-100 antialiased bg-grid-pattern selection:bg-emerald-500 selection:text-black">
        {children}
      </body>
    </html>
  );
}
