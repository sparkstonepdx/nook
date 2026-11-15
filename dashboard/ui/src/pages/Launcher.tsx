import { For, Match, Show, Switch, createSignal, onMount } from "solid-js";
import { directory, faviconURL, iconURL, loadDirectory, maskableURL, type DirCard } from "../directory";

// Every site on the server as a tile: one tap opens it. PR previews are left out. The tile shows what Android
// shows for an installed PWA: the manifest's maskable icon filling it, else its app icon shrunk onto the site's
// background color, else its favicon, else its initial.
const FIRST = 8;

export default function Launcher() {
  onMount(loadDirectory);
  const [all, setAll] = createSignal(false);
  // listed sites in their order, then installable ones (a manifest), then ones with an icon, then the rest
  const sites = () => {
    const l = (directory() ?? []).filter((c) => !c.preview);
    const rank = (c: DirCard) => (c.listed ? 0 : c.installable ? 1 : c.maskable || c.icon || c.favicon ? 2 : 3);
    return l.map((c, i) => ({ c, i })).sort((a, b) => rank(a.c) - rank(b.c) || a.i - b.i).map((x) => x.c);
  };
  const shown = () => (all() ? sites() : sites().slice(0, FIRST));
  return (
    <Show when={sites().length}>
      <nav class="launcher" aria-label="Open a site">
        <For each={shown()}>{(c) => <Tile c={c} />}</For>
      </nav>
      <Show when={sites().length > FIRST}>
        <button class="btn btn-ghost btn-sm launcher-more" onClick={() => setAll(!all())}>{all() ? "Show fewer" : `Show all ${sites().length}`}</button>
      </Show>
    </Show>
  );
}

function Tile(props: { c: DirCard }) {
  const label = () => props.c.shortName || props.c.name;
  return (
    <a class="tile" href={props.c.url} target="_blank" rel="noopener" title={`${props.c.name} · ${props.c.host}`}>
      <Switch fallback={<span class="tile-icon"><span class="tile-initial" style={{ color: props.c.theme || undefined }}>{label().slice(0, 1).toUpperCase()}</span></span>}>
        <Match when={maskableURL(props.c)}>{(src) => <span class="tile-icon" style={{ background: props.c.background || undefined }}><img src={src()} alt="" loading="lazy" /></span>}</Match>
        <Match when={iconURL(props.c)}>{(src) => <span class="tile-icon tile-padded" style={{ background: props.c.background || undefined }}><img src={src()} alt="" loading="lazy" /></span>}</Match>
        <Match when={faviconURL(props.c)}>{(src) => <span class="tile-icon tile-favicon"><img src={src()} alt="" loading="lazy" /></span>}</Match>
      </Switch>
      <span class="tile-name">{label()}</span>
      <Show when={props.c.listed}><span class="tile-listed" title="Listed in the directory" aria-label="listed" /></Show>
    </a>
  );
}
