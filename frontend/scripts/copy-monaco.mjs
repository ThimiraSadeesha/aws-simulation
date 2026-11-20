// Copies the Monaco Editor AMD build into public/monaco so the editor loads from the console
// itself (works offline – no CDN). Run automatically by `predev` / `prebuild`.
import { cpSync, existsSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const src = join(root, "node_modules", "monaco-editor", "min", "vs");
const dest = join(root, "public", "monaco", "vs");
if (!existsSync(src)) {
  console.error("monaco-editor is not installed; run npm install");
  process.exit(1);
}
mkdirSync(dirname(dest), { recursive: true });
cpSync(src, dest, { recursive: true });
console.log("Monaco copied to public/monaco/vs");
