import * as React from "react"
import { cn } from "cn"
import { Dialog as DialogPrimitive } from "radix-ui"

import { Button } from "@/components/ui/button"
import { XIcon } from "lucide-react"

let lastDialogTrigger: HTMLElement | null = null

function Dialog({
  onOpenChange,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Root>) {
  return (
    <DialogPrimitive.Root
      data-slot="dialog"
      onOpenChange={(open) => {
        if (open && document.activeElement instanceof HTMLElement) {
          lastDialogTrigger = document.activeElement
        }
        onOpenChange?.(open)
      }}
      {...props}
    />
  )
}

function DialogTrigger({
  onClick,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Trigger>) {
  return (
    <DialogPrimitive.Trigger
      data-slot="dialog-trigger"
      onClick={(event) => {
        lastDialogTrigger = event.currentTarget
        onClick?.(event)
      }}
      {...props}
    />
  )
}

function DialogPortal({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Portal>) {
  return <DialogPrimitive.Portal data-slot="dialog-portal" {...props} />
}

function DialogClose({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Close>) {
  return <DialogPrimitive.Close data-slot="dialog-close" {...props} />
}

function DialogOverlay({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 isolate z-50 bg-black/70 duration-100 supports-backdrop-filter:backdrop-blur-xs data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0",
        className
      )}
      {...props}
    />
  )
}

function DialogContent({
  className,
  children,
  showCloseButton = true,
  onOpenAutoFocus,
  onCloseAutoFocus,
  onEscapeKeyDown,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content> & {
  showCloseButton?: boolean
}) {
  const dialogChildren = React.Children.toArray(children)
  const slotOf = (child: React.ReactNode) =>
    React.isValidElement<{ "data-slot"?: string }>(child)
      ? child.props["data-slot"]
      : undefined
  const header = dialogChildren.filter(
    (child) => slotOf(child) === "dialog-header"
  )
  const footer = dialogChildren.filter(
    (child) => slotOf(child) === "dialog-footer"
  )
  const body = dialogChildren.filter(
    (child) => !["dialog-header", "dialog-footer"].includes(slotOf(child) ?? "")
  )
  const focusDialog: NonNullable<
    React.ComponentProps<typeof DialogPrimitive.Content>["onOpenAutoFocus"]
  > = (event) => {
    onOpenAutoFocus?.(event)
    if (event.defaultPrevented) return
    event.preventDefault()
    const dialog = event.currentTarget as HTMLElement
    if (document.activeElement instanceof HTMLElement && !dialog.contains(document.activeElement)) {
      lastDialogTrigger = document.activeElement
    }
    const target =
      dialog.querySelector<HTMLElement>("[autofocus]") ??
      dialog.querySelector<HTMLElement>(
        "[data-slot=dialog-body] button:not([data-slot=dialog-close]), [data-slot=dialog-body] input, [data-slot=dialog-body] select, [data-slot=dialog-body] textarea, [data-slot=dialog-body] [tabindex]:not([tabindex='-1'])"
      ) ??
      dialog.querySelector<HTMLElement>("[data-slot=dialog-close]")
    target?.focus()
  }
  const handleEscapeKeyDown: NonNullable<
    React.ComponentProps<typeof DialogPrimitive.Content>["onEscapeKeyDown"]
  > = (event) => {
    onEscapeKeyDown?.(event)
    if (event.defaultPrevented) return
    const dialog = event.currentTarget as HTMLElement | null
    if (!showCloseButton && !dialog?.querySelector("[data-slot=dialog-close]")) {
      event.preventDefault()
    }
  }
  const restoreDialogFocus: NonNullable<
    React.ComponentProps<typeof DialogPrimitive.Content>["onCloseAutoFocus"]
  > = (event) => {
    onCloseAutoFocus?.(event)
    if (event.defaultPrevented) return
    const trigger = lastDialogTrigger
    if (!trigger?.isConnected) return
    event.preventDefault()
    requestAnimationFrame(() => trigger.focus())
    lastDialogTrigger = null
  }

  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        className={cn(
          "fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 flex-col gap-4 overflow-hidden rounded-xl bg-popover p-4 text-sm text-popover-foreground ring-1 ring-foreground/10 duration-100 outline-none data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          className
        )}
        onOpenAutoFocus={focusDialog}
        onCloseAutoFocus={restoreDialogFocus}
        onEscapeKeyDown={handleEscapeKeyDown}
        {...props}
      >
        {header}
        <div
          data-slot="dialog-body"
          className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain pr-1"
        >
          {body}
        </div>
        {footer}
        {showCloseButton && (
          <DialogPrimitive.Close data-slot="dialog-close" asChild>
            <Button
              variant="ghost"
              className="absolute top-2 right-2"
              size="icon-sm"
            >
              <XIcon
              />
              <span className="sr-only">Close</span>
            </Button>
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Content>
    </DialogPortal>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex shrink-0 flex-col gap-2", className)}
      {...props}
    />
  )
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  showCloseButton?: boolean
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "-mx-4 -mb-4 flex shrink-0 flex-col-reverse gap-2 rounded-b-xl border-t bg-muted/50 p-4 sm:flex-row sm:justify-end",
        className
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close data-slot="dialog-close" asChild>
          <Button variant="outline">Close</Button>
        </DialogPrimitive.Close>
      )}
    </div>
  )
}

function DialogTitle({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn(
        "font-heading text-base leading-none font-medium",
        className
      )}
      {...props}
    />
  )
}

function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={cn(
        "text-sm text-muted-foreground *:[a]:underline *:[a]:underline-offset-3 *:[a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
}
