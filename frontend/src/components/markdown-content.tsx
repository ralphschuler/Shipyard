import { createElement } from "react";
import { parseMarkdown, type MarkdownInline } from "@/lib/markdown";

function Inline({ parts }: { parts: MarkdownInline[] }) {
  return <>{parts.map((part, index) => part.link ? <a key={index} href={part.link.href} target={/^https?:/i.test(part.link.href) ? "_blank" : undefined} rel={/^https?:/i.test(part.link.href) ? "noreferrer" : undefined}>{part.link.label}</a> : <span key={index}>{part.text}</span>)}</>;
}

export function MarkdownContent({ source, className = "" }: { source: string; className?: string }) {
  const blocks = parseMarkdown(source);
  if (!blocks.length) return null;
  return <div className={`markdown-content min-w-0 ${className}`}>
    {blocks.map((block, index) => {
      if (block.type === "heading") {
        return createElement(`h${block.level}`, { key: index, className: "mt-5 first:mt-0 font-semibold tracking-tight" }, block.text);
      }
      if (block.type === "paragraph") return <p key={index} className="my-3 whitespace-pre-wrap leading-7 first:mt-0 last:mb-0">{block.inline ? <Inline parts={block.inline} /> : block.text}</p>;
      if (block.type === "blockquote") return <blockquote key={index} className="my-4 border-l-2 border-primary/40 pl-4 italic text-muted-foreground whitespace-pre-wrap">{block.text}</blockquote>;
      if (block.type === "code") return <pre key={index} className="my-4 max-w-full overflow-x-auto rounded-lg bg-slate-950 p-4 font-mono text-sm leading-6 text-slate-100"><code className={block.language ? `language-${block.language}` : undefined}>{block.text}</code></pre>;
      if (block.type === "table") return <div key={index} className="my-4 max-w-full overflow-x-auto"><table className="w-full min-w-max border-collapse text-left text-sm"><thead><tr>{block.headers.map((header) => <th key={header} scope="col" className="border-b px-3 py-2 font-semibold">{header}</th>)}</tr></thead><tbody>{block.rows.map((row, rowIndex) => <tr key={rowIndex}>{row.map((cell, cellIndex) => <td key={cellIndex} className="border-b px-3 py-2 align-top">{cell}</td>)}</tr>)}</tbody></table></div>;
      const List = block.ordered ? "ol" : "ul";
      return <List key={index} className={`my-3 space-y-1 pl-6 ${block.ordered ? "list-decimal" : "list-disc"}`}>{block.items.map((item, itemIndex) => <li key={itemIndex}><Inline parts={item} /></li>)}</List>;
    })}
  </div>;
}
