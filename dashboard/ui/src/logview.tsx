import { For, Show } from "solid-js";
import { parseAnsi } from "./ansi";

// Reading a log: lines from Rust's tracing (Subduction) or anything shaped like it ("TIME LEVEL target: message
// key=value ...") are split into their time, level, target, message and fields, with long hex ids cut to their first
// 8 characters (hover for all of it). Any other line shows as it is. The raw view and Copy stay the server's text.

export type Level = "TRACE" | "DEBUG" | "INFO" | "WARN" | "ERROR";
export type LogLine = { raw: string; plain: string; time?: Date; level?: Level; span?: string; target?: string; msg?: string; fields?: [string, string][] };

/** The text without colour codes. */
export const plain = (s: string) => parseAnsi(s).map((p) => p.text).join("");

const LINE = /^(\d{4}-\d\d-\d\dT[\d:.]+Z?)\s+(TRACE|DEBUG|INFO|WARN|ERROR)\s+(.*)$/;
// "span: target: message", "target: message", or just a message
const HEAD = /^(?:([\w{}=". -]+?): )?([\w]+(?:::[\w]+)+): ?(.*)$/;
// key=value: the value quoted, a "Name { ... }" struct, or everything up to the next key= (values can hold spaces)
const FIELD = /(?:^|\s)([A-Za-z_][\w.]*)=("(?:[^"\\]|\\.)*"|[A-Z]\w* \{[^}]*\}|.*?(?=\s[A-Za-z_][\w.]*=|$))/g;

export function parseLine(raw: string): LogLine {
  const text = plain(raw);
  const m = LINE.exec(text);
  if (!m) return { raw, plain: text };
  const time = new Date(m[1]);
  const h = HEAD.exec(m[3]);
  const rest = h ? h[3] : m[3];
  const fields: [string, string][] = [];
  // the message is what comes before the first key=value
  const first = rest.search(/(?:^|\s)[A-Za-z_][\w.]*=/);
  let msg = rest;
  if (first >= 0) {
    msg = rest.slice(0, first).trim();
    for (const f of rest.slice(first).matchAll(FIELD)) fields.push([f[1], f[2].replace(/^"(.*)"$/, "$1")]);
  }
  return { raw, plain: text, time: isNaN(+time) ? undefined : time, level: m[2] as Level, span: h?.[1], target: h?.[2], msg, fields };
}

/** Long hex runs (peer ids, tree ids, hashes) cut to 8 characters, the whole one on hover. */
function Ids(props: { text: string }) {
  const parts = () => props.text.split(/([0-9a-f]{24,})/);
  return (
    <For each={parts()}>
      {(p, i) => (i() % 2 ? <abbr class="lv-id" title={p}>{p.slice(0, 8)}…</abbr> : p)}
    </For>
  );
}

const clock = (d: Date) => d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" });

export function LogView(props: { lines: LogLine[] }) {
  return (
    <ol class="lv">
      <For each={props.lines}>
        {(l) => (
          <Show when={l.level} fallback={<li class="lv-line lv-other">{l.plain || " "}</li>}>
            <li class={`lv-line lv-${l.level!.toLowerCase()}`}>
              <time class="lv-time" title={l.time?.toISOString()}>{l.time ? clock(l.time) : ""}</time>
              <span class="lv-level">{l.level}</span>
              <span class="lv-body">
                <Show when={l.target}><span class="lv-target" title={l.target}>{l.target!.split("::").at(-1)}</span></Show>
                <span class="lv-msg"><Ids text={l.msg ?? ""} /></span>
                <For each={l.fields}>{([k, v]) => <span class="lv-field"><span class="lv-key">{k}</span><Ids text={v} /></span>}</For>
              </span>
            </li>
          </Show>
        )}
      </For>
    </ol>
  );
}
