"use client";

import React from "react";

/**
 * The one-bridge product diagram: agents on top, a single LiteSPM box in the
 * middle, the three kinds underneath — the shape the whole product is a
 * description of, drawn once as inline SVG so it inherits the palette (and the
 * dark theme) instead of shipping as a bitmap.
 *
 * Motion: only the connector paths animate, once, via `stroke-dashoffset`, and
 * only on a wide viewport that has not asked for reduced motion (the gate lives
 * in globals.css). Two SVGs, CSS-switched:
 *
 *   >= 768px  four agents, curved connectors, staggered 90ms apart
 *   < 768px   a single grouped agent row and straight-ish connectors — the
 *             simplified form, static by construction rather than by
 *             disabling something that was already running
 *
 * Nothing here is a marquee, a parallax layer or a particle field; the spec
 * forbade those, and a diagram that has to move to be understood is a diagram
 * that was drawn wrong.
 */

const KINDS: Array<{ label: string; note: string; token: string }> = [
  { label: "MCP servers", note: "apps & data", token: "mcp" },
  { label: "Agent skills", note: "workflows", token: "skill" },
  { label: "Plugins", note: "toolkits", token: "plugin" },
];

const AGENTS = [
  { label: "Claude Code", x: 7 },
  { label: "Codex", x: 147 },
  { label: "OpenCode", x: 287 },
  { label: "Cursor / Cline", x: 427 },
];

const BOX_W = 126;
const BOX_H = 44;
const CENTER = 280;

function TopBox({ label, x }: { label: string; x: number }) {
  return (
    <g>
      <rect
        x={x}
        y={8}
        width={BOX_W}
        height={BOX_H}
        rx={8}
        fill="var(--surface)"
        stroke="var(--rule-2)"
        strokeWidth={1}
      />
      <text
        x={x + BOX_W / 2}
        y={8 + BOX_H / 2 + 4}
        textAnchor="middle"
        fontSize={13}
        fontWeight={500}
        fill="var(--ink)"
        style={{ fontFamily: "var(--font-sans), sans-serif" }}
      >
        {label}
      </text>
    </g>
  );
}

export function BridgeDiagram() {
  return (
    <>
      {/* ---- desktop: four agents, curved connectors, animated once ---- */}
      <svg
        viewBox="0 0 560 424"
        role="img"
        aria-labelledby="bridge-title bridge-desc"
        className="hidden h-auto w-full md:block"
      >
        <title id="bridge-title">One bridge between your agents and every capability</title>
        <desc id="bridge-desc">
          Claude Code, Codex, OpenCode and Cursor or Cline each connect into a single LiteSPM box that
          discovers, installs and manages MCP servers, agent skills and plugins.
        </desc>

        {/* connectors first, so the boxes sit on top of their own lines */}
        {AGENTS.map((agent, i) => {
          const cx = agent.x + BOX_W / 2;
          return (
            <path
              key={`in-${agent.label}`}
              className="bridge-path"
              pathLength={1}
              d={`M ${cx},52 C ${cx},132 ${CENTER},124 ${CENTER},196`}
              fill="none"
              stroke="var(--accent)"
              strokeWidth={1.5}
              strokeOpacity={0.55}
              style={{ animationDelay: `${i * 90}ms` }}
            />
          );
        })}
        {KINDS.map((kind, i) => {
          const cx = 14 + i * 184 + 82;
          return (
            <path
              key={`out-${kind.token}`}
              className="bridge-path"
              pathLength={1}
              d={`M ${CENTER},280 C ${CENTER},322 ${cx},316 ${cx},348`}
              fill="none"
              stroke={`var(--${kind.token})`}
              strokeWidth={1.5}
              strokeOpacity={0.6}
              style={{ animationDelay: `${(AGENTS.length + i) * 90}ms` }}
            />
          );
        })}

        {AGENTS.map((agent) => (
          <TopBox key={agent.label} label={agent.label} x={agent.x} />
        ))}

        {/* the one bridge */}
        <rect x={160} y={196} width={240} height={84} rx={12} fill="var(--accent)" />
        <text
          x={CENTER}
          y={234}
          textAnchor="middle"
          fontSize={26}
          fontWeight={600}
          fill="#ffffff"
          style={{ fontFamily: "var(--font-sans), sans-serif", letterSpacing: "-0.01em" }}
        >
          LiteSPM
        </text>
        <text
          x={CENTER}
          y={258}
          textAnchor="middle"
          fontSize={12}
          fill="#ffffff"
          fillOpacity={0.88}
          style={{ fontFamily: "var(--font-mono), monospace" }}
        >
          discover · install · manage
        </text>

        {KINDS.map((kind, i) => {
          const x = 14 + i * 184;
          return (
            <g key={kind.token}>
              <rect
                x={x}
                y={348}
                width={164}
                height={64}
                rx={10}
                fill={`var(--${kind.token}-wash)`}
                stroke={`var(--${kind.token})`}
                strokeWidth={1}
                strokeOpacity={0.5}
              />
              <text
                x={x + 82}
                y={374}
                textAnchor="middle"
                fontSize={14}
                fontWeight={600}
                fill={`var(--${kind.token})`}
                style={{ fontFamily: "var(--font-sans), sans-serif" }}
              >
                {kind.label}
              </text>
              <text
                x={x + 82}
                y={394}
                textAnchor="middle"
                fontSize={11}
                fill={`var(--${kind.token})`}
                fillOpacity={0.85}
                style={{ fontFamily: "var(--font-mono), monospace" }}
              >
                {kind.note}
              </text>
            </g>
          );
        })}
      </svg>

      {/* ---- mobile: the simplified, static form ------------------------ */}
      <svg
        viewBox="0 0 360 296"
        role="img"
        aria-labelledby="bridge-title-sm bridge-desc-sm"
        className="block h-auto w-full md:hidden"
      >
        <title id="bridge-title-sm">One bridge between your agents and every capability</title>
        <desc id="bridge-desc-sm">
          Your agents connect into a single LiteSPM box, which discovers, installs and manages MCP
          servers, agent skills and plugins.
        </desc>

        <rect
          x={8}
          y={8}
          width={344}
          height={40}
          rx={8}
          fill="var(--surface)"
          stroke="var(--rule-2)"
          strokeWidth={1}
        />
        <text
          x={180}
          y={33}
          textAnchor="middle"
          fontSize={12}
          fill="var(--ink-2)"
          style={{ fontFamily: "var(--font-sans), sans-serif" }}
        >
          Claude Code · Codex · OpenCode · Cursor
        </text>

        <path
          d="M 180,48 L 180,96"
          fill="none"
          stroke="var(--accent)"
          strokeWidth={1.5}
          strokeOpacity={0.55}
        />

        <rect x={70} y={96} width={220} height={72} rx={12} fill="var(--accent)" />
        <text
          x={180}
          y={130}
          textAnchor="middle"
          fontSize={24}
          fontWeight={600}
          fill="#ffffff"
          style={{ fontFamily: "var(--font-sans), sans-serif" }}
        >
          LiteSPM
        </text>
        <text
          x={180}
          y={152}
          textAnchor="middle"
          fontSize={11}
          fill="#ffffff"
          fillOpacity={0.88}
          style={{ fontFamily: "var(--font-mono), monospace" }}
        >
          discover · install · manage
        </text>

        {KINDS.map((kind, i) => {
          const x = 8 + i * 120;
          const cx = x + 52;
          return (
            <g key={`sm-${kind.token}`}>
              <path
                d={`M 180,168 C 180,200 ${cx},198 ${cx},224`}
                fill="none"
                stroke={`var(--${kind.token})`}
                strokeWidth={1.5}
                strokeOpacity={0.6}
              />
              <rect
                x={x}
                y={224}
                width={104}
                height={60}
                rx={10}
                fill={`var(--${kind.token}-wash)`}
                stroke={`var(--${kind.token})`}
                strokeWidth={1}
                strokeOpacity={0.5}
              />
              <text
                x={cx}
                y={250}
                textAnchor="middle"
                fontSize={12}
                fontWeight={600}
                fill={`var(--${kind.token})`}
                style={{ fontFamily: "var(--font-sans), sans-serif" }}
              >
                {kind.label}
              </text>
              <text
                x={cx}
                y={268}
                textAnchor="middle"
                fontSize={10}
                fill={`var(--${kind.token})`}
                fillOpacity={0.85}
                style={{ fontFamily: "var(--font-mono), monospace" }}
              >
                {kind.note}
              </text>
            </g>
          );
        })}
      </svg>
    </>
  );
}
