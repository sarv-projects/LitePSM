/** @type {import('tailwindcss').Config} */
const tokens = {
  paper: "var(--paper)",
  surface: "var(--surface)",
  sunken: "var(--sunken)",
  "sunken-2": "var(--sunken-2)",
  hover: "var(--hover)",
  rule: "var(--rule)",
  "rule-2": "var(--rule-2)",
  ink: "var(--ink)",
  "ink-2": "var(--ink-2)",
  "ink-3": "var(--ink-3)",
  mcp: "var(--mcp)",
  skill: "var(--skill)",
  plugin: "var(--plugin)",
  pop: "var(--pop)",
  dark: "var(--dark)",
  "dark-2": "var(--dark-2)",
  "dark-rule": "var(--dark-rule)",
  "dark-ink": "var(--dark-ink)",
  "dark-ink-2": "var(--dark-ink-2)",
};

module.exports = {
  content: [
    "./app/**/*.{js,ts,jsx,tsx,mdx}",
    "./components/**/*.{js,ts,jsx,tsx,mdx}",
    "./lib/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: tokens,
      borderRadius: {
        chip: "var(--r-chip)",
        ctl: "var(--r-ctl)",
        panel: "var(--r-panel)",
        hero: "var(--r-hero)",
      },
      fontFamily: {
        sans: ["var(--font-sans)", "ui-sans-serif", "system-ui", "sans-serif"],
        cond: ["var(--font-cond)", "var(--font-sans)", "ui-sans-serif", "sans-serif"],
        mono: ["var(--font-mono)", "ui-monospace", "SFMono-Regular", "monospace"],
      },
      maxWidth: {
        shell: "1440px",
      },
    },
  },
  plugins: [],
};
