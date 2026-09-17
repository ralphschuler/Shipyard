import type { ElementType, ReactNode } from "react";

const unsafeScheme = /^(?:javascript|data|vbscript|file|blob):/i;

export function safeMarkdownURL(value: string): string | undefined {
  const candidate = value.trim();
  if (!candidate || unsafeScheme.test(candidate)) return undefined;
  if (/^(?:https?:|mailto:|#|\/)/i.test(candidate)) return candidate;
  return undefined;
}

function inline(value: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  const token = /(`[^`]*`|!?\[[^\]]*\]\([^)]*\)|\*\*[^*]+\*\*|__[^_]+__|\*[^*]+\*|_[^_]+_)/g;
  let cursor = 0;
  let match: RegExpExecArray | null;
  while ((match = token.exec(value))) {
    if (match.index > cursor) nodes.push(value.slice(cursor, match.index));
    const part = match[0];
    if (part.startsWith("`")) nodes.push(<code key={match.index}>{part.slice(1, -1)}</code>);
    else if (part.startsWith("[") || part.startsWith("![")) {
      const image = part.startsWith("!");
      const link = part.slice(image ? 2 : 1).match(/^(.*)\]\(([^)]*)\)$/);
      const href = link && safeMarkdownURL(link[2]);
      if (image || !href) nodes.push(link?.[1] || part);
      else nodes.push(<a key={match.index} href={href} target="_blank" rel="noreferrer noopener">{inline(link[1])}</a>);
    } else if (part.startsWith("**") || part.startsWith("__")) nodes.push(<strong key={match.index}>{inline(part.slice(2, -2))}</strong>);
    else nodes.push(<em key={match.index}>{inline(part.slice(1, -1))}</em>);
    cursor = match.index + part.length;
  }
  if (cursor < value.length) nodes.push(value.slice(cursor));
  return nodes;
}

function table(lines: string[], key: number): ReactNode {
  const cells = (line: string) => line.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((cell) => cell.trim());
  const head = cells(lines[0]);
  const rows = lines.slice(2).map(cells);
  return <div className="overflow-x-auto" key={key}><table className="w-full text-left text-sm"><thead><tr>{head.map((cell, index) => <th className="border-b px-2 py-2" key={index}>{inline(cell)}</th>)}</tr></thead><tbody>{rows.map((row, rowIndex) => <tr key={rowIndex}>{head.map((_, index) => <td className="border-b px-2 py-2 last:border-0" key={index}>{inline(row[index] || "")}</td>)}</tr>)}</tbody></table></div>;
}

export function renderMarkdown(source: string): ReactNode {
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  const blocks: ReactNode[] = [];
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (!line.trim()) { index++; continue; }
    if (/^```/.test(line.trim())) {
      const code: string[] = [];
      index++;
      while (index < lines.length && !/^```/.test(lines[index].trim())) code.push(lines[index++]);
      if (index < lines.length) index++;
      blocks.push(<pre className="max-w-full overflow-x-auto rounded-md bg-muted p-3 text-xs" key={blocks.length}><code>{code.join("\n")}</code></pre>);
      continue;
    }
    const heading = line.match(/^(#{1,6})\s+(.+?)\s*#*$/);
    if (heading) { const Tag = `h${heading[1].length}` as ElementType; blocks.push(<Tag key={blocks.length}>{inline(heading[2])}</Tag>); index++; continue; }
    if (index + 1 < lines.length && /^\s*\|?.+\|.+\|?\s*$/.test(line) && /^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)+\|?\s*$/.test(lines[index + 1])) {
      const tableLines = [line, lines[index + 1]]; index += 2;
      while (index < lines.length && lines[index].includes("|")) tableLines.push(lines[index++]);
      blocks.push(table(tableLines, blocks.length)); continue;
    }
    const list = line.match(/^\s*([-*+]|\d+[.)])\s+(.+)$/);
    if (list) {
      const ordered = /^\d/.test(list[1]); const items: ReactNode[] = [];
      while (index < lines.length) { const item = lines[index].match(/^\s*([-*+]|\d+[.)])\s+(.+)$/); if (!item || (ordered !== /^\d/.test(item[1]))) break; items.push(<li key={items.length}>{inline(item[2])}</li>); index++; }
      const Tag = ordered ? "ol" : "ul"; blocks.push(<Tag className="my-2 list-inside space-y-1" key={blocks.length}>{items}</Tag>); continue;
    }
    if (/^>\s?/.test(line)) { blocks.push(<blockquote className="border-l-2 pl-3 text-muted-foreground" key={blocks.length}>{inline(line.replace(/^>\s?/, ""))}</blockquote>); index++; continue; }
    const paragraph: string[] = [line]; index++;
    while (index < lines.length && lines[index].trim() && !/^(#{1,6})\s|^```|^\s*([-*+]|\d+[.)])\s+|^>\s?/.test(lines[index])) paragraph.push(lines[index++]);
    blocks.push(<p key={blocks.length}>{inline(paragraph.join(" "))}</p>);
  }
  return <div className="changelog-markdown space-y-3 break-words">{blocks}</div>;
}
