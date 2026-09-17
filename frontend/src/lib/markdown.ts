export type MarkdownInline = {
  text: string;
  link?: { label: string; href: string };
};

export type MarkdownBlock =
  | { type: "heading"; level: number; text: string }
  | { type: "paragraph"; text: string; inline?: MarkdownInline[] }
  | { type: "list"; ordered: boolean; items: MarkdownInline[][] }
  | { type: "blockquote"; text: string }
  | { type: "table"; headers: string[]; rows: string[][] }
  | { type: "code"; language: string; text: string };

const unsafeScheme = /^(?:javascript|vbscript|data):/i;
const htmlTag = /<[^>]*>/g;
const linkPattern = /\[([^\]]+)\]\(([^\s)]+)(?:\s+"[^"]*")?\)/g;

function safeUrl(value: string) {
  const href = value.trim();
  if (!href || unsafeScheme.test(href)) return undefined;
  if (/^(?:https?:|mailto:)/i.test(href) || href.startsWith("/") || href.startsWith("#")) return href;
  return undefined;
}

function cleanText(value: string) {
  return value.replace(htmlTag, "");
}

function inline(value: string): MarkdownInline[] {
  const result: MarkdownInline[] = [];
  let cursor = 0;
  for (const match of value.matchAll(linkPattern)) {
    const start = match.index ?? 0;
    if (start > cursor) result.push({ text: cleanText(value.slice(cursor, start)) });
    const label = cleanText(match[1]);
    const href = safeUrl(match[2]);
    result.push(href ? { text: "", link: { label, href } } : { text: label });
    cursor = start + match[0].length;
  }
  if (cursor < value.length) result.push({ text: cleanText(value.slice(cursor)) });
  return result.filter((part) => part.text || part.link);
}

function plainOrInline(value: string): Pick<MarkdownBlock, "text" | "inline"> {
  const parts = inline(value);
  const hasLink = parts.some((part) => part.link);
  return hasLink ? { text: parts.map((part) => part.text).join(""), inline: parts } : { text: parts.map((part) => part.text).join("") };
}

function cells(line: string) {
  return line.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((cell) => cleanText(cell.trim()));
}

function isDivider(line: string) {
  return /^\|?\s*:?-{3,}:?\s*(?:\|\s*:?-{3,}:?\s*)+\|?$/.test(line);
}

export function parseMarkdown(source: string): MarkdownBlock[] {
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  const blocks: MarkdownBlock[] = [];
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (!line.trim()) { index += 1; continue; }
    const fence = line.match(/^\s*```\s*([\w-]*)\s*$/);
    if (fence) {
      index += 1;
      const code: string[] = [];
      while (index < lines.length && !/^\s*```\s*$/.test(lines[index])) code.push(lines[index++]);
      if (index < lines.length) index += 1;
      blocks.push({ type: "code", language: fence[1], text: code.join("\n") });
      continue;
    }
    const heading = line.match(/^\s*(#{1,6})\s+(.+?)\s*#*\s*$/);
    if (heading) { blocks.push({ type: "heading", level: heading[1].length, text: plainOrInline(heading[2]).text }); index += 1; continue; }
    if (line.trimStart().startsWith(">")) {
      const quote: string[] = [];
      while (index < lines.length && lines[index].trimStart().startsWith(">")) quote.push(lines[index++].trimStart().slice(1).trim());
      blocks.push({ type: "blockquote", text: cleanText(quote.join("\n")) });
      continue;
    }
    if (line.includes("|") && index + 1 < lines.length && isDivider(lines[index + 1])) {
      const headers = cells(line); index += 2;
      const rows: string[][] = [];
      while (index < lines.length && lines[index].trim() && lines[index].includes("|")) rows.push(cells(lines[index++]));
      blocks.push({ type: "table", headers, rows });
      continue;
    }
    const listItem = line.match(/^\s*([-*+] |\d+[.)] )(.+)$/);
    if (listItem) {
      const ordered = /^\d/.test(listItem[1]);
      const items: MarkdownInline[][] = [];
      while (index < lines.length) {
        const item = lines[index].match(/^\s*([-*+] |\d+[.)] )(.+)$/);
        if (!item || /^\d/.test(item[1]) !== ordered) break;
        items.push(inline(item[2])); index += 1;
      }
      blocks.push({ type: "list", ordered, items });
      continue;
    }
    const paragraph: string[] = [line]; index += 1;
    while (index < lines.length && lines[index].trim() && !/^\s*(?:```|#{1,6}\s|>|[-*+] |\d+[.)] )/.test(lines[index])) paragraph.push(lines[index++]);
    const content = plainOrInline(paragraph.join("\n"));
    blocks.push({ type: "paragraph", ...content });
  }
  return blocks;
}
