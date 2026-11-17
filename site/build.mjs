// Builds nook's website into dist-site/: the landing page (site/src), the docs (README.md) and the changelog
// (CHANGELOG.md), with the screenshots and logo from docs/. `npm run site` builds it; the site workflow deploys it.
import { cpSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { Marked } from "marked";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const out = join(root, "dist-site");
const repo = "https://github.com/sparkstonepdx/nook";

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
cpSync(join(root, "site/src"), out, { recursive: true });
for (const p of ["screens", "logo", "how-it-works.svg"]) cpSync(join(root, "docs", p), join(out, "assets", p), { recursive: true });

const slug = (s) => s.toLowerCase().replace(/<[^>]+>/g, "").replace(/[^\w\s-]/g, "").trim().replace(/\s+/g, "-");

// One markdown file as a page: headings get ids (GitHub's, so #install links keep working) and h2/h3 make the
// contents; links into the repo go to GitHub, and docs/ images to the copies in /assets/.
function page(file, title, current) {
  const toc = [];
  const seen = new Map();
  const md = new Marked({
    renderer: {
      heading({ tokens, depth }) {
        const text = this.parser.parseInline(tokens);
        let id = slug(text);
        const n = seen.get(id) ?? 0;
        seen.set(id, n + 1);
        if (n) id += `-${n}`;
        if (depth === 2 || depth === 3) toc.push({ depth, id, text });
        return `<h${depth} id="${id}">${text}</h${depth}>\n`;
      },
    },
    walkTokens(t) {
      if ((t.type === "image" || t.type === "link") && t.href && !/^(https?:|#|mailto:)/.test(t.href)) {
        t.href = t.href.startsWith("docs/") && t.type === "image" ? "/assets/" + t.href.slice(5) : `${repo}/blob/main/${t.href}`;
      }
    },
  });
  let src = readFileSync(join(root, file), "utf8");
  // the README's opening (pitch, screenshots, diagram) is the landing page; the docs start where the reference does
  if (file === "README.md" && src.includes("\n## Sites and apps")) src = "# nook docs\n" + src.slice(src.indexOf("\n## Sites and apps"));
  src = src.replace(/src="docs\//g, 'src="/assets/'); // raw <img> tags
  const body = md.parse(src);
  const items = [];
  let open = false;
  for (const h of toc) {
    if (h.depth === 2) {
      if (open) items.push("</ul></li>");
      items.push(`<li><a href="#${h.id}">${h.text}</a><ul>`);
      open = true;
    } else items.push(`<li><a href="#${h.id}">${h.text}</a></li>`);
  }
  if (open) items.push("</ul></li>");
  const nav = (href, label) => `<a href="${href}"${current === href ? ' aria-current="page"' : ""}>${label}</a>`;
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${title}</title>
  <meta name="theme-color" content="#3b4f9a">
  <link rel="icon" href="/assets/logo/icon.svg" type="image/svg+xml">
  <link rel="stylesheet" href="/style.css">
</head>
<body>
  <header class="bar"><div class="bar-in">
    <a class="brand" href="/"><img src="/assets/logo/icon.svg" alt="">nook</a>
    <nav aria-label="Site">${nav("/docs/", "Docs")}${nav("/changelog/", "Changelog")}<a href="${repo}">GitHub</a></nav>
  </div></header>
  <div class="docs">
    <details class="toc" open><summary>Contents</summary><ul>${items.join("")}</ul></details>
    <main class="prose">${body}</main>
  </div>
  <script>
    // on a phone the contents start closed; a tap on one closes them again
    const toc = document.querySelector(".toc");
    if (matchMedia("(max-width: 900px)").matches) toc.open = false;
    toc.addEventListener("click", (e) => { if (e.target.closest("a") && matchMedia("(max-width: 900px)").matches) toc.open = false; });
  </script>
</body>
</html>
`;
}

for (const [file, dir, title] of [
  ["README.md", "docs", "nook docs"],
  ["CHANGELOG.md", "changelog", "nook changelog"],
]) {
  mkdirSync(join(out, dir), { recursive: true });
  writeFileSync(join(out, dir, "index.html"), page(file, title, `/${dir}/`));
}
writeFileSync(join(out, "404.html"), readFileSync(join(out, "index.html"), "utf8"));
console.log("built", out);
