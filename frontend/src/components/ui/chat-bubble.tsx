import * as React from "react"
import { cn } from "cn"

type ChatBubbleProps = React.ComponentProps<"article"> & {
  author: string
  timestamp?: string
  side?: "incoming" | "outgoing"
}

// A small composition primitive following the same slot and token conventions
// as the local shadcn components.  It intentionally carries no product logic:
// task comments, agent output and future chat-like extensions can all use it.
function ChatBubble({
  author,
  timestamp,
  side = "incoming",
  className,
  children,
  ...props
}: ChatBubbleProps) {
  const outgoing = side === "outgoing"
  return (
    <article
      data-slot="chat-bubble"
      data-side={side}
      className={cn(
        "flex max-w-[88%] flex-col gap-1",
        outgoing ? "ml-auto items-end" : "mr-auto items-start",
        className,
      )}
      {...props}
    >
      <div className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
        <span className="font-medium text-foreground">{author}</span>
        {timestamp && <time>{timestamp}</time>}
      </div>
      <div
        className={cn(
          "rounded-2xl px-3 py-2 text-sm leading-6 shadow-sm ring-1 ring-foreground/10",
          outgoing
            ? "rounded-br-md bg-primary text-primary-foreground ring-primary/30"
            : "rounded-bl-md bg-muted/65 text-foreground",
        )}
      >
        {children}
      </div>
    </article>
  )
}

export { ChatBubble }
