import { parse as parseYaml, stringify as stringifyYaml } from "yaml";

export interface MarkdownDoc<F = Record<string, unknown>> {
  frontmatter: F;
  body: string;
}

const FM_RE = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/;

export function parseMarkdown<F = Record<string, unknown>>(text: string): MarkdownDoc<F> {
  const m = FM_RE.exec(text);
  if (!m) {
    return { frontmatter: {} as F, body: text };
  }
  const fm = (parseYaml(m[1]!) ?? {}) as F;
  return { frontmatter: fm, body: m[2] ?? "" };
}

export function serializeMarkdown(doc: MarkdownDoc<Record<string, unknown>>): string {
  const yaml = stringifyYaml(doc.frontmatter, { lineWidth: 0 }).trimEnd();
  const body = doc.body.startsWith("\n") ? doc.body : `\n${doc.body}`;
  const bodyEnd = body.endsWith("\n") ? body : `${body}\n`;
  return `---\n${yaml}\n---\n${bodyEnd}`;
}
