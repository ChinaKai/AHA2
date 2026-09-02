import {readFile, mkdir, writeFile, copyFile, rm} from "node:fs/promises";
import {resolve} from "node:path";
import {stripTypeScriptTypes} from "node:module";

const root = resolve(import.meta.dirname, "..");
const source = resolve(root, "web");
const output = resolve(source, "dist");
await rm(output, {recursive: true, force: true});
await mkdir(output, {recursive: true});

for (const name of ["api", "icons", "types", "main"]) {
  const input = await readFile(resolve(source, "src", `${name}.ts`), "utf8");
  const transformed = stripTypeScriptTypes(input, {mode: "transform", sourceMap: false});
  const outputName = name === "main" ? "app" : name;
  await writeFile(resolve(output, `${outputName}.js`), transformed);
}
await copyFile(resolve(source, "index.html"), resolve(output, "index.html"));
await copyFile(resolve(source, "src", "styles.css"), resolve(output, "styles.css"));
console.log(`Built ${output}`);
