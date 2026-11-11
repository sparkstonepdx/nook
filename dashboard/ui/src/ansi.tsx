import { For, type JSX } from "solid-js";

// Terminal output (logs, command output) often carries ANSI escape codes: colours from a logger like
// tracing or zap, bold, dim. Ansi renders the SGR ones (ESC[...m) as styled spans and drops every other
// control sequence, so a log reads like it does in a terminal instead of showing "[2m".

type Style = { bold?: boolean; dim?: boolean; italic?: boolean; underline?: boolean; fg?: string };
type Part = { text: string; style: Style };

// The 16 basic colours, mapped to tokens that keep their contrast in both themes.
const basic = ["var(--muted)", "var(--bad)", "var(--good)", "var(--warn)", "var(--accent-text)", "var(--accent-text)", "var(--accent-text)", "var(--ink)"];

const xterm = (n: number): string | undefined => {
  if (n < 16) return basic[n % 8];
  if (n >= 232) return `oklch(${Math.round(30 + ((n - 232) / 23) * 50)}% 0 0)`; // greys
  return undefined; // the 6x6x6 cube: keep the text colour, which always reads
};

function sgr(codes: number[], s: Style): Style {
  s = { ...s };
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i];
    if (c === 0) s = {};
    else if (c === 1) s.bold = true;
    else if (c === 2) s.dim = true;
    else if (c === 3) s.italic = true;
    else if (c === 4) s.underline = true;
    else if (c === 22) s.bold = s.dim = false;
    else if (c === 23) s.italic = false;
    else if (c === 24) s.underline = false;
    else if (c === 39) s.fg = undefined;
    else if (c >= 30 && c <= 37) s.fg = basic[c - 30];
    else if (c >= 90 && c <= 97) s.fg = basic[c - 90];
    else if (c === 38 && codes[i + 1] === 5) { s.fg = xterm(codes[i + 2] ?? 7); i += 2; }
    else if (c === 38 && codes[i + 1] === 2) { i += 4; } // 24-bit colour: keep the text colour
    else if (c === 48 && codes[i + 1] === 5) i += 2;
    else if (c === 48 && codes[i + 1] === 2) i += 4;
  }
  return s;
}

// ESC [ params final, ESC ] ... BEL/ST (titles, links), and any other lone ESC sequence.
const escRE = /\x1b\[([0-9;:?]*)([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]/g;

export function parseAnsi(input: string): Part[] {
  // a carriage return redraws the line in a terminal: keep what the line ends up as
  const text = input.replace(/[^\n]*\r(?!\n)/g, "").replace(/\r\n/g, "\n");
  const parts: Part[] = [];
  let style: Style = {};
  let last = 0;
  for (const m of text.matchAll(escRE)) {
    if (m.index! > last) parts.push({ text: text.slice(last, m.index), style });
    if (m[2] === "m") style = sgr((m[1] || "0").split(/[;:]/).map((x) => Number(x) || 0), style);
    last = m.index! + m[0].length;
  }
  if (last < text.length) parts.push({ text: text.slice(last), style });
  // other control characters (not tab or newline) would show as boxes
  return parts.map((p) => ({ ...p, text: p.text.replace(/[\x00-\x08\x0b-\x1f\x7f]/g, "") })).filter((p) => p.text);
}

const css = (s: Style): JSX.CSSProperties => ({
  color: s.fg,
  "font-weight": s.bold ? 700 : undefined,
  opacity: s.dim ? 0.7 : undefined,
  "font-style": s.italic ? "italic" : undefined,
  "text-decoration": s.underline ? "underline" : undefined,
});

export function Ansi(props: { text: string }) {
  return (
    <For each={parseAnsi(props.text)}>
      {(p) => (Object.keys(p.style).length ? <span style={css(p.style)}>{p.text}</span> : p.text)}
    </For>
  );
}
