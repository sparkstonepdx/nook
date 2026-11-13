// Prints a Subduction server's Keyhive contact card (JSON), made from its key seed.
// extras/subduction-app.sh runs this and saves the card as data/subduction-card.json, where the dashboard
// and `nook-admin subduction` read it. The card is public; the seed never leaves the server.
//
//   node extras/subduction-card.mjs /srv/nook/subduction/data/subduction.key
//
// Needs @keyhive/keyhive. The card's format follows the Keyhive version: 0.4.0-alpha.1 makes the JSON
// card that subduction_cli 0.22 and @automerge/automerge-repo-keyhive 0.7 expect.
import { readFileSync } from "node:fs";
import { CiphertextStore, Keyhive, Signer } from "@keyhive/keyhive";

const file = process.argv[2];
if (!file) { console.error("usage: node subduction-card.mjs <key file>"); process.exit(1); }
const raw = readFileSync(file);
const text = raw.toString("utf8").trim();
const seed = /^[0-9a-fA-F]{64}$/.test(text) ? Buffer.from(text, "hex") : raw.length === 32 ? raw : null;
if (!seed) { console.error("the key file must hold 32 bytes, raw or as 64 hex digits"); process.exit(1); }

const kh = await Keyhive.init(Signer.memorySignerFromBytes(new Uint8Array(seed)), CiphertextStore.newInMemory(), () => {});
console.log((await kh.getExistingContactCard()).toJson());
