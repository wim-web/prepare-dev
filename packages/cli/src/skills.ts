import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";

function digest(dir: string): string {
  const hash = createHash("sha256");
  const walk = (base: string): void => {
    for (const entry of fs.readdirSync(base, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      const file = path.join(base, entry.name);
      hash.update(path.relative(dir, file));
      if (entry.isDirectory()) walk(file);
      else if (entry.isFile()) hash.update(fs.readFileSync(file));
      else hash.update("symlink");
    }
  };
  walk(dir);
  return hash.digest("hex");
}

export function copySkills(source: string, destinations: string[], root: string): { copied: string[]; unchanged: string[]; conflicts: string[] } {
  const result: { copied: string[]; unchanged: string[]; conflicts: string[] } = { copied: [], unchanged: [], conflicts: [] };
  for (const destination of destinations) {
    let ancestor = destination;
    while (!fs.existsSync(ancestor) && ancestor !== path.dirname(ancestor)) ancestor = path.dirname(ancestor);
    const relative = path.relative(fs.realpathSync(root), fs.realpathSync(ancestor));
    if (relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) { result.conflicts.push(destination); continue; }
    const stat = fs.lstatSync(destination, { throwIfNoEntry: false });
    if (stat && (!stat.isDirectory() || stat.isSymbolicLink())) { result.conflicts.push(destination); continue; }
    fs.mkdirSync(destination, { recursive: true });
    for (const name of fs.readdirSync(source).sort()) {
      const from = path.join(source, name);
      if (!fs.existsSync(path.join(from, "SKILL.md"))) continue;
      const to = path.join(destination, name);
      const existing = fs.lstatSync(to, { throwIfNoEntry: false });
      if (existing) {
        if (existing.isDirectory() && !existing.isSymbolicLink() && digest(from) === digest(to)) result.unchanged.push(to);
        else result.conflicts.push(to);
      } else {
        fs.cpSync(from, to, { recursive: true, force: false, errorOnExist: true });
        result.copied.push(to);
      }
    }
  }
  return result;
}
