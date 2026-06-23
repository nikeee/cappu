// Mirror togo/internal/meta/meta.go's Version constant to package.json's
// version. Run by the npm `version` lifecycle hook (after the bump, before the
// commit/tag) so the bumped package.json and the Go constant land together in
// the single commit npm tags v<version>.
import { readFileSync, writeFileSync } from "node:fs";

const { version } = JSON.parse(readFileSync("package.json", "utf8"));
const path = "togo/internal/meta/meta.go";
const src = readFileSync(path, "utf8");

const pattern = /(Version = ")[^"]*(")/;
if (!pattern.test(src)) {
  console.error(`sync-go-version: could not find a Version = "..." line in ${path}`);
  process.exit(1);
}
writeFileSync(path, src.replace(pattern, `$1${version}$2`));
console.log(`synced ${path} to ${version}`);
