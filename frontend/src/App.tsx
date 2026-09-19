import { createContext, lazy, Suspense, useContext, useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  Activity,
  Bot,
  Boxes,
  ChevronDown,
  CircleDot,
  BrainCircuit,
  FolderGit2,
  Gauge,
  GitCompareArrows,
  LayoutDashboard,
  LoaderCircle,
  Menu,
  Moon,
  Play,
  ShieldCheck,
  ShieldAlert,
  Sun,
  Wrench,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ChatBubble } from "@/components/ui/chat-bubble";
import { MarkdownContent } from "@/components/markdown-content";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { normalizeLanguage, translate, type Language } from "@/i18n";
import { renderMarkdown, safeMarkdownURL } from "@/markdown";
import { mutation, refreshData, useAPI, type LiveChange } from "@/api/client";

const Dashboard = lazy(() => import("@/features/dashboard"));
const Memory = lazy(() => import("@/features/memory"));
type NavItem = {
  name: string;
  path: string;
  endpoint?: string;
  icon: typeof LayoutDashboard;
};
type BoardNavItem = { ID: string; Name: string };
type LocaleContextValue = {
  language: Language;
  t: (key: string) => string;
  text: (german: string, english: string) => string;
};

const LocaleContext = createContext<LocaleContextValue>({
  language: "de",
  t: (key) => key,
  text: (german) => german,
});

function useLocale() {
  return useContext(LocaleContext);
}
const nav: NavItem[] = [
  { name: "overview", path: "/", icon: LayoutDashboard },
  {
    name: "projects",
    path: "/projects",
    endpoint: "/api/v1/projects",
    icon: FolderGit2,
  },
  { name: "boards", path: "/boards", endpoint: "/api/v1/boards", icon: Boxes },
  { name: "agents", path: "/agents", endpoint: "/api/v1/agents", icon: Bot },
  {
    name: "automations",
    path: "/automations",
    endpoint: "/api/v1/automations",
    icon: Activity,
  },
  { name: "skills", path: "/skills", endpoint: "/api/v1/skills", icon: Wrench },
  { name: "runs", path: "/runs", endpoint: "/api/v1/runs", icon: Play },
  { name: "memory", path: "/memory", icon: BrainCircuit },
  {
    name: "audit",
    path: "/audit",
    endpoint: "/api/v1/audit",
    icon: ShieldCheck,
  },
  { name: "settings", path: "/settings/providers", icon: Gauge },
];
function routeFromHash() {
  return location.hash.slice(1).split("?")[0] || "/";
}

function taskTabFromHash(): "conversation" | "changes" {
  return new URLSearchParams(location.hash.split("?")[1] || "").get("tab") === "changes"
    ? "changes"
    : "conversation";
}
function titleFor(route: string, t: (key: string) => string) {
  const known = nav.find((item) => item.path === route)?.name;
  if (known) return t(known);
  if (route === "/settings") return t("settings");
  if (route === "/account") return t("account");
  // Detail views intentionally keep their parent section in the persistent
  // header; the local page then supplies the concrete board, task or run name.
  if (route === "/boards") return t("boards");
  if (route === "/projects") return t("projects");
  if (route === "/agents") return t("agents");
  if (route === "/automations") return t("automations");
  if (route === "/skills") return t("skills");
  if (route === "/runs") return t("runs");
  if (route === "/memory") return t("memory");
  if (route === "/audit") return t("audit");
  return "Shipyard";
}

export default function App() {
  const [route, setRoute] = useState(routeFromHash);
  const { data: appearance } = useAPI<any>("/api/v1/settings/appearance");
  const { data: boardsResponse } = useAPI<BoardNavItem[]>("/api/v1/boards");
  // Keep the shell usable while a mocked or older API returns an unexpected
  // envelope.  The board list endpoint normally returns an array, but a
  // malformed response must not take down every route in the application.
  const boards = Array.isArray(boardsResponse) ? boardsResponse : [];
  const [language, setLanguage] = useState<Language>(() => normalizeLanguage(localStorage.getItem("shipyard-language")));
  const t = (key: string) => translate(language, key);
  const [dark, setDark] = useState(
    localStorage.getItem("shipyard-theme") === "dark",
  );
  const [navOpen, setNavOpen] = useState(
    localStorage.getItem("shipyard-nav-open") !== "false",
  );
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [boardsOpen, setBoardsOpen] = useState(() => {
    const saved = localStorage.getItem("shipyard-boards-open");
    return saved == null ? routeFromHash().startsWith("/boards") : saved === "true";
  });
  const mobileNavRef = useRef<HTMLElement>(null);
  const mobileMenuButtonRef = useRef<HTMLButtonElement>(null);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  useEffect(() => {
    if (appearance?.Language) setLanguage(normalizeLanguage(appearance.Language));
  }, [appearance?.Language]);
  useEffect(() => {
    const changed = (event: Event) => setLanguage(normalizeLanguage((event as CustomEvent<string>).detail));
    window.addEventListener("shipyard:language-change", changed);
    return () => window.removeEventListener("shipyard:language-change", changed);
  }, []);
  useEffect(() => {
    localStorage.setItem("shipyard-language", language);
    document.documentElement.lang = language;
  }, [language]);
  useEffect(() => {
    const update = () => setRoute(routeFromHash());
    addEventListener("hashchange", update);
    addEventListener("popstate", update);
    return () => {
      removeEventListener("hashchange", update);
      removeEventListener("popstate", update);
    };
  }, []);
  useEffect(() => {
    if (!mobileNavOpen) return;
    const frame = requestAnimationFrame(() => {
      mobileNavRef.current
        ?.querySelector<HTMLElement>("[data-nav-index]")
        ?.focus();
    });
    const closeMobileNavigation = () => {
      setMobileNavOpen(false);
      requestAnimationFrame(() => mobileMenuButtonRef.current?.focus());
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") closeMobileNavigation();
    };
    const keepFocusInDrawer = (event: KeyboardEvent) => {
      if (event.key !== "Tab" || !mobileNavRef.current) return;
      const focusable = Array.from(
        mobileNavRef.current.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      ).filter((element) => element.offsetParent !== null);
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", closeOnEscape);
    window.addEventListener("keydown", keepFocusInDrawer);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("keydown", closeOnEscape);
      window.removeEventListener("keydown", keepFocusInDrawer);
    };
  }, [mobileNavOpen]);
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    localStorage.setItem("shipyard-theme", dark ? "dark" : "light");
  }, [dark]);
  useEffect(() => {
    if (!appearance?.Theme) return;
    if (appearance.Theme === "dark") {
      setDark(true);
      return;
    }
    if (appearance.Theme === "light") {
      setDark(false);
      return;
    }
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => setDark(media.matches);
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [appearance?.Theme]);
  useEffect(() => {
    const stream = new EventSource("/events");
    // Never remount the application for a database notification. Components
    // subscribe below and refresh just their remote projection, keeping forms,
    // dialogs, focus and scroll position intact.
    stream.addEventListener("change", (event) => {
      let change: LiveChange = {};
      try {
        change = JSON.parse((event as MessageEvent).data) as LiveChange;
      } catch {
        // A malformed external notification must never break the live stream.
      }
      refreshData(change);
    });
    return () => stream.close();
  }, []);
  useEffect(() => {
    localStorage.setItem("shipyard-nav-open", String(navOpen));
  }, [navOpen]);
  useEffect(() => {
    localStorage.setItem("shipyard-boards-open", String(boardsOpen));
  }, [boardsOpen]);
  useEffect(() => {
    if (route.startsWith("/boards/")) setBoardsOpen(true);
  }, [route]);
  useEffect(() => {
    const desktop = window.matchMedia("(min-width: 768px)");
    const closeMobileMenu = () => {
      if (desktop.matches) setMobileNavOpen(false);
    };
    closeMobileMenu();
    desktop.addEventListener("change", closeMobileMenu);
    return () => desktop.removeEventListener("change", closeMobileMenu);
  }, []);
  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (event.key !== "?" || target?.matches("input, textarea, select, [contenteditable=true]")) return;
      if (appearance?.ShortcutHints === false) return;
      event.preventDefault();
      setShortcutsOpen(true);
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [appearance?.ShortcutHints]);
  const navigate = (path: string) => {
    if (mobileNavOpen) {
      setMobileNavOpen(false);
      requestAnimationFrame(() => mobileMenuButtonRef.current?.focus());
    }
    if (route === path) return;
    window.history.pushState(null, "", `#${path}`);
    setRoute(path);
  };
  const toggleNavigation = () => {
    if (window.matchMedia("(min-width: 768px)").matches) {
      setNavOpen((value) => !value);
    } else {
      setMobileNavOpen((value) => !value);
    }
  };
  const navigateNav = (index: number, event: React.KeyboardEvent<HTMLElement>) => {
    let next = index;
    if ((event.key === "ArrowDown" || event.key === "ArrowRight") && index === boardsNavIndex && !boardsOpen) {
      setBoardsOpen(true);
      next = boardsNavIndex + 1;
      event.preventDefault();
      requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-nav-index="${next}"]`)?.focus());
      return;
    }
    const total = nav.length + (boardsOpen ? boards.length + 1 : 0);
    if (event.key === "ArrowDown" || event.key === "ArrowRight") next = (index + 1) % total;
    else if (event.key === "ArrowUp" || event.key === "ArrowLeft") next = (index - 1 + total) % total;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = total - 1;
    else return;
    event.preventDefault();
    document.querySelector<HTMLElement>(`[data-nav-index="${next}"]`)?.focus();
  };
  const active = nav.find((item) => item.path === route);
  const showNavLabels = navOpen || mobileNavOpen;
  const boardsActive = route === "/boards" || route.startsWith("/boards/");
  const boardsNavIndex = nav.findIndex((item) => item.name === "boards");
  const boardSubmenuSize = boardsOpen ? boards.length + 1 : 0;
  const navIndexFor = (index: number) =>
    index + (boardsOpen && index > boardsNavIndex ? boardSubmenuSize : 0);
  return (
    <LocaleContext.Provider value={{ language, t, text: (german, english) => language === "en" ? english : german }}>
    <TooltipProvider>
      <div className="min-h-dvh bg-background">
        <Button
          type="button"
          variant="outline"
          size="icon"
          aria-label={t("navigationToggle")}
          aria-controls="main-navigation"
          className="fixed left-3 top-3 z-30 md:left-4 md:top-4"
          ref={mobileMenuButtonRef}
          onClick={toggleNavigation}
        >
          <Menu className="size-4" />
        </Button>
        <aside
          id="main-navigation"
          ref={mobileNavRef}
          role={mobileNavOpen ? "dialog" : undefined}
          aria-modal={mobileNavOpen ? "true" : undefined}
          aria-labelledby={mobileNavOpen ? "shipyard-brand-name" : undefined}
          className={`shipyard-sidebar ${mobileNavOpen ? "flex w-72" : "hidden"} fixed inset-y-0 left-0 z-20 flex-col border-r md:flex ${navOpen ? "md:w-64" : "md:w-16"} ${navOpen ? "" : "is-collapsed"}`}
        >
          <div className="shipyard-brand flex h-16 items-center gap-3 border-b px-5">
            <span className="grid size-8 place-items-center rounded-lg bg-primary text-sm font-bold text-primary-foreground">
              SY
            </span>
            {showNavLabels && <span id="shipyard-brand-name"><strong className="block font-semibold">Shipyard</strong><small>Control room</small></span>}
          </div>
          <nav aria-label={t("navigation")} className="shipyard-nav flex-1 space-y-1 overflow-y-auto p-3">
            {nav.map((item, index) => {
              const Icon = item.icon;
              const selected = item.path === route || (item.name === "boards" && boardsActive);
              const navIndex = navIndexFor(index);
              return (
                <div key={item.path} className="space-y-1">
                  {item.name === "boards" ? (
            <button
                      type="button"
                      aria-expanded={boardsOpen}
                      aria-controls="board-subnavigation"
                      aria-current={selected ? "page" : undefined}
                      aria-label={t(item.name)}
                      title={t(item.name)}
                      className={`shipyard-nav-item ${selected ? "is-active" : ""}`}
                      onClick={() => setBoardsOpen((value) => !value)}
                      onKeyDown={(event) => navigateNav(navIndex, event)}
                      data-nav-index={navIndex}
                    >
                      <Icon className="size-4 shrink-0" />
                      {showNavLabels && <span className="min-w-0 flex-1 text-left">{t(item.name)}</span>}
                      {showNavLabels && <ChevronDown className={`size-4 transition-transform ${boardsOpen ? "rotate-180" : ""}`} aria-hidden="true" />}
                    </button>
                  ) : (
                    <a
                      href={`#${item.path}`}
                      onClick={(event) => { event.preventDefault(); navigate(item.path); }}
                      onKeyDown={(event) => navigateNav(navIndex, event)}
                      data-nav-index={navIndex}
                      aria-current={selected ? "page" : undefined}
                      title={t(item.name)}
                      className={`shipyard-nav-item ${selected ? "is-active" : ""}`}
                    >
                      <Icon className="size-4 shrink-0" />
                      {showNavLabels && t(item.name)}
                    </a>
                  )}
                  {item.name === "boards" && boardsOpen && (
                    <nav id="board-subnavigation" className="board-subnavigation" aria-label={t("availableBoards")}>
                      <ul>
                        <li><a href="#/boards" onClick={(event) => { event.preventDefault(); navigate("/boards"); }} onKeyDown={(event) => navigateNav(boardsNavIndex + 1, event)} data-nav-index={boardsNavIndex + 1} className={`board-nav-link ${route === "/boards" ? "is-active" : ""}`} aria-current={route === "/boards" ? "page" : undefined} aria-label={t("allBoards")} title={t("allBoards")}>
                          <span className="board-nav-glyph" aria-hidden="true">⌘</span><span>{t("allBoards")}</span>
                        </a></li>
                        {boards.map((board, boardIndex) => {
                          const boardPath = `/boards/${board.ID}`;
                          const boardSelected = route === boardPath || route.startsWith(`${boardPath}/`);
                          const boardIndexInNavigation = boardsNavIndex + boardIndex + 2;
                          return <li key={board.ID}><a href={`#${boardPath}`} onClick={(event) => { event.preventDefault(); navigate(boardPath); }} onKeyDown={(event) => navigateNav(boardIndexInNavigation, event)} data-nav-index={boardIndexInNavigation} className={`board-nav-link ${boardSelected ? "is-active" : ""}`} aria-current={boardSelected ? "page" : undefined} aria-label={board.Name} title={board.Name}><CircleDot className="size-3.5" aria-hidden="true" /><span>{board.Name}</span></a></li>;
                        })}
                        {boards.length === 0 && <li><p className="board-nav-empty">{t("noBoards")}</p></li>}
                      </ul>
                    </nav>
                  )}
                </div>
              );
            })}
          </nav>
          <div className="border-t p-3">
            <Button
              variant="ghost"
              className="w-full justify-start gap-3"
              onClick={() => setDark(!dark)}
            >
              {dark ? <Sun className="size-4" /> : <Moon className="size-4" />}
              {showNavLabels && (dark ? t("light") : t("dark"))}
            </Button>
          </div>
        </aside>
        {mobileNavOpen && (
          <button
            type="button"
            aria-label={t("navigationClose")}
            className="fixed inset-y-0 right-0 left-72 z-10 bg-foreground/20 md:hidden"
            onClick={() => {
              setMobileNavOpen(false);
              requestAnimationFrame(() => mobileMenuButtonRef.current?.focus());
            }}
          />
        )}
        <Dialog open={shortcutsOpen} onOpenChange={setShortcutsOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t("keyboardControl")}</DialogTitle>
              <DialogDescription>{language === "en" ? "The entire interface remains usable with standard focus controls." : "Die gesamte Oberfläche bleibt mit Standard-Fokussteuerung bedienbar."}</DialogDescription>
            </DialogHeader>
            <dl className="grid gap-3 text-sm">
              <div><dt className="font-medium">Tab / {language === "en" ? "Shift" : "Umschalt"} + Tab</dt><dd className="text-muted-foreground">{language === "en" ? "Move to the next or previous control." : "Zum nächsten oder vorherigen Bedienelement wechseln."}</dd></div>
              <div><dt className="font-medium">Enter / {language === "en" ? "Space" : "Leertaste"}</dt><dd className="text-muted-foreground">{language === "en" ? "Activate the focused link, button, or selection." : "Fokussierten Link, Button oder Auswahl auslösen."}</dd></div>
              <div><dt className="font-medium">↑ / ↓, {language === "en" ? "Home / End" : "Pos1 / Ende"}</dt><dd className="text-muted-foreground">{language === "en" ? "Select entries in the page navigation." : "Einträge in der Seitennavigation auswählen."}</dd></div>
              <div><dt className="font-medium">?</dt><dd className="text-muted-foreground">{language === "en" ? "Open this help." : "Diese Hilfe öffnen."}</dd></div>
              <div><dt className="font-medium">Escape</dt><dd className="text-muted-foreground">{language === "en" ? "Close the dialog." : "Dialog schließen."}</dd></div>
            </dl>
          </DialogContent>
        </Dialog>
        <main className={`mx-auto max-w-7xl px-5 py-8 pt-20 md:px-8 ${navOpen ? "md:ml-64" : "md:ml-16"}`}>
          <header className="mb-8 flex justify-between gap-4">
            <div>
              <p className="text-sm font-medium text-primary">Operations</p>
              <h1 className="mt-1 text-3xl font-semibold tracking-tight">
                {titleFor(route.split("/").slice(0, 2).join("/") || "/", t)}
              </h1>
              <p className="mt-2 text-sm text-muted-foreground">
                {route === "/" ? t("overviewDescription") : t("resourcesDescription")}
              </p>
            </div>
            <Badge variant="outline" className="h-fit gap-2 px-3 py-1.5">
              <span className="size-2 rounded-full bg-emerald-500" />
              {t("connected")}
            </Badge>
          </header>
          {route.match(/^\/boards\/[^/]+\/workflow$/) ? (
            <WorkflowEditorV2 id={route.split("/")[2]} />
          ) : route.startsWith("/boards/") ? (
            <BoardDetail key={route.split("/")[2]} id={route.split("/")[2]} />
          ) : route.startsWith("/tasks/") ? (
            <TaskDetail key={route.split("/")[2]} id={route.split("/")[2]} />
          ) : route.startsWith("/runs/") ? (
            <RunDetail id={route.split("/")[2]} />
          ) : route === "/automations/schedules" ? (
            <Schedules />
          ) : route === "/automations/webhooks" ? (
            <Webhooks />
          ) : route === "/agents/templates" ? (
            <AgentTemplates />
          ) : route === "/agents/new" ? (
            <AgentForm />
          ) : route.startsWith("/agents/") ? (
            <AgentDetail id={route.split("/")[2]} />
          ) : route === "/" ? (
            <Suspense fallback={<Loading />}><Dashboard /></Suspense>
          ) : route === "/memory" ? (
            <Suspense fallback={<Loading />}><Memory /></Suspense>
          ) : active?.endpoint ? (
            <ResourceList endpoint={active.endpoint} title={t(active.name)} />
          ) : route === "/settings" || route.startsWith("/settings/") || route === "/account" ? (
            <Settings route={route} language={language} />
          ) : (
            <NotFound />
          )}
        </main>
      </div>
    </TooltipProvider>
    </LocaleContext.Provider>
  );
}

function ResourceList({
  endpoint,
  title,
}: {
  endpoint: string;
  title: string;
}) {
  if (endpoint === "/api/v1/boards") return <Boards />;
  if (endpoint === "/api/v1/projects") return <Projects />;
  if (endpoint === "/api/v1/agents") return <Agents />;
  if (endpoint === "/api/v1/automations") return <Automations />;
  if (endpoint === "/api/v1/runs") return <Runs />;
  if (endpoint === "/api/v1/skills") return <Skills />;
  if (endpoint === "/api/v1/audit") return <Audit />;
  return <GenericResourceList endpoint={endpoint} title={title} />;
}
function GenericResourceList({
  endpoint,
  title,
}: {
  endpoint: string;
  title: string;
}) {
  const { text } = useLocale();
  const { data, error } = useAPI<Record<string, unknown>[]>(endpoint);
  if (error) return <Failure />;
  if (!data) return <Loading />;
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{data.length} {text("Einträge", "entries")}</CardDescription>
      </CardHeader>
      <CardContent>
        {data.length ? (
          <div className="divide-y">
            {data.map((item, index) => (
              <article
                key={String(item.ID ?? index)}
                className="grid gap-1 py-4 first:pt-0"
              >
                <div className="flex items-center justify-between gap-4">
                  <strong className="text-sm">
                    {String(
                      item.Name ??
                        item.TaskTitle ??
                        item.Title ??
                        item.ID ??
                        text("Eintrag", "entry"),
                    )}
                  </strong>
                  {typeof item.Status === "string" && (
                    <Badge variant="secondary">{item.Status}</Badge>
                  )}
                </div>
                <p className="max-w-3xl text-sm text-muted-foreground">
                  {String(
                    item.Description ??
                      item.Message ??
                      item.Summary ??
                      item.RepositoryURL ??
                      "",
                  )}
                </p>
              </article>
            ))}
          </div>
        ) : (
          <p className="py-12 text-center text-sm text-muted-foreground">
            {text("Noch keine Einträge vorhanden.", "No entries yet.")}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
function Boards() {
  const { language, text } = useLocale();
  const { data: items, error: loadFailed } = useAPI<Record<string, unknown>[]>("/api/v1/boards");
  const { data: templates } = useAPI<any[]>("/api/v1/board-templates");
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [template, setTemplate] = useState("software");
  const [open, setOpen] = useState(false);
  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await mutation("/api/v1/boards", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ Name: name, Template: template }),
      });
      setName("");
      setOpen(false);
      refreshData();
    } catch (err) {
      setError(String(err));
    }
  };
  const remove = async (id: string) => {
    if (
      !confirm(
        text("Board wirklich löschen? Alle darin enthaltenen Aufgaben werden entfernt.", "Delete this board? All tasks it contains will be removed."),
      )
    )
      return;
    try {
      await mutation("/boards/" + id + "/delete", { method: "POST" });
      refreshData();
    } catch (err) {
      setError(String(err));
    }
  };
  if (loadFailed) return <Failure />;
  if (!items || !templates) return <Loading />;
  const selected = templates.find((value) => value.ID === template);
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div>
          <CardTitle>{text("Boards", "Boards")}</CardTitle>
          <CardDescription>{items.length} {text("Boards", "boards")}</CardDescription>
        </div>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button>{text("Board anlegen", "Create board")}</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{text("Neues Board", "New board")}</DialogTitle>
              <DialogDescription>
                {text("Wähle eine Vorlage; der Workflow bleibt danach vollständig anpassbar.", "Choose a template; the workflow remains fully customizable afterwards.")}
              </DialogDescription>
            </DialogHeader>
            <form onSubmit={create} className="grid gap-4">
              <label className="grid gap-2 text-sm font-medium">
                {text("Name", "Name")}
                <Input
                  autoFocus
                  required
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={text("z. B. Plattform", "e.g. Platform")}
                />
              </label>
              <fieldset className="grid gap-2">
                <legend className="text-sm font-medium">
                  {text("Workflow-Vorlage", "Workflow template")}
                </legend>
                <div className="max-h-56 space-y-2 overflow-auto pr-1">
                  {templates.map((value) => (
                    <label
                      key={value.ID}
                      className={
                        template === value.ID
                          ? "block cursor-pointer rounded-lg border border-primary bg-primary/5 p-3"
                          : "block cursor-pointer rounded-lg border p-3"
                      }
                    >
                      <input
                        className="mr-2"
                        type="radio"
                        checked={template === value.ID}
                        onChange={() => setTemplate(value.ID)}
                      />
                      <strong className="text-sm">{value.Name}</strong>
                      <p className="mt-1 text-xs text-muted-foreground">
                        {value.Summary}
                      </p>
                    </label>
                  ))}
                </div>
                {selected && (
                  <p className="text-xs text-muted-foreground">
                    {selected.Detail}
                  </p>
                )}
              </fieldset>
              <DialogFooter>
                <Button type="submit">{text("Board erstellen", "Create board")}</Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        {error && <p className="mb-4 text-sm text-destructive">{error}</p>}
        {items.length ? <div className="divide-y">
          {items.map((item) => (
            <article
              key={String(item.ID)}
              className="flex items-center justify-between gap-4 py-4 first:pt-0"
            >
              <a href={"#/boards/" + String(item.ID)} className="min-w-0">
                <strong className="text-sm hover:underline">
                  {String(item.Name)}
                </strong>
                <p className="mt-1 text-xs text-muted-foreground">
                  {text("Erstellt", "Created")}{" "}
                  {new Date(String(item.CreatedAt)).toLocaleDateString(language === "en" ? "en-US" : "de-DE")}
                </p>
              </a>
              <Button
                variant="destructive"
                size="sm"
                onClick={() => remove(String(item.ID))}
              >
                {text("Löschen", "Delete")}
              </Button>
            </article>
          ))}
        </div> : <EmptyState title={text("Noch kein Board", "No boards yet")} description={text("Lege ein Board aus einer Workflow-Vorlage an, um Aufgaben und Automationen zu organisieren.", "Create a board from a workflow template to organize tasks and automations.")} />}
      </CardContent>
    </Card>
  );
}
function Projects() {
  const { language, text } = useLocale();
  const { data: items, error: loadFailed } = useAPI<any[]>("/api/v1/projects");
  const { data: boards } = useAPI<any[]>("/api/v1/boards");
  const { data: groups } = useAPI<any[]>("/api/v1/project-groups");
  const [editing, setEditing] = useState<any>();
  const [open, setOpen] = useState(false);
  const [groupsOpen, setGroupsOpen] = useState(false);
  const [editingGroup, setEditingGroup] = useState<any>();
  const [error, setError] = useState("");
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    const form = new FormData(e.target as HTMLFormElement);
    try {
      await mutation(editing?.ID ? "/projects/" + editing.ID : "/projects", {
        method: "POST",
        body: form,
      });
      setOpen(false);
      setEditing(undefined);
      refreshData();
    } catch (err) {
      setError(String(err));
    }
  };
  const remove = async (id: string) => {
    if (!confirm(text("Projekt wirklich löschen?", "Delete this project?"))) return;
    try {
      await mutation("/projects/" + id + "/delete", { method: "POST" });
      refreshData();
    } catch (err) {
      setError(String(err));
    }
  };
  const sync = async (id: string) => {
    try {
      await mutation("/projects/" + id + "/sync", { method: "POST" });
      refreshData();
    } catch (err) {
      setError(String(err));
    }
  };
  const saveGroup = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!editingGroup) return;
    try {
      await mutation(`/project-groups/${editingGroup.ID}`, {
        method: "POST",
        body: new FormData(event.target as HTMLFormElement),
      });
      setEditingGroup(undefined);
      refreshData();
    } catch (err) {
      setError(String(err));
    }
  };
  const deleteGroup = async (id: string) => {
    if (!confirm(text("Projektgruppe wirklich entfernen? Projekte und bereits gespeicherte Task-Ziele bleiben erhalten.", "Remove this project group? Projects and existing task targets will be retained."))) return;
    try {
      await mutation(`/project-groups/${id}/delete`, { method: "POST" });
      setEditingGroup(undefined);
      refreshData();
    } catch (err) {
      setError(String(err));
    }
  };
  if (loadFailed) return <Failure />;
  if (!items || !boards || !groups) return <Loading />;
  const bound = (project: any, board: any) =>
    project.Boards?.some((item: any) => item.ID === board.ID);
  const grouped = (project: any, group: any) =>
    group.Projects?.some((item: any) => item.ID === project.ID);
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div>
          <CardTitle>{text("Projekte", "Projects")}</CardTitle>
          <CardDescription>
            {text("Repositories, Gruppen und Board-Zuordnung.", "Repositories, groups, and board assignments.")}
          </CardDescription>
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="outline" onClick={() => setGroupsOpen(true)}>
            {text("Gruppen verwalten", "Manage groups")}
          </Button>
          <Button
            onClick={() => {
              setEditing(undefined);
              setOpen(true);
            }}
          >
            {text("Projekt anlegen", "Create project")}
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {error && <p className="mb-4 text-sm text-destructive">{error}</p>}
        {items.length ? <div className="divide-y">
          {items.map((project) => (
            <article key={project.ID} className="py-4 first:pt-0">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <strong className="text-sm">{project.Name}</strong>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {project.RepositoryURL || text("Kein Repository verbunden", "No repository connected")} ·{" "}
                    {project.DefaultBranch}
                  </p>
                  <p className={"mt-1 text-xs " + (project.LastSyncError ? "text-destructive" : "text-muted-foreground")}>
                    {project.LastSyncError
                      ? `${text("Sync-Fehler", "Sync error")}: ${project.LastSyncError}`
                      : project.LastSyncedAt
                        ? `${text("Zuletzt synchronisiert", "Last synchronized")}: ${new Date(project.LastSyncedAt).toLocaleString(language === "en" ? "en-US" : "de-DE")}`
                        : text("Noch nicht synchronisiert", "Not synchronized yet")}
                  </p>
                  <div className="mt-2 flex flex-wrap gap-1">
                    {project.Boards?.map((board: any) => (
                      <Badge key={board.ID} variant="outline">
                        {text("Board", "Board")}: {board.Name}
                      </Badge>
                    ))}
                    {groups.filter((group) => grouped(project, group)).map((group) => (
                      <Badge
                        key={group.ID}
                        variant="outline"
                        style={{
                          borderColor: group.Color || undefined,
                          color: group.Color || undefined,
                          backgroundColor: group.Color ? `${group.Color}18` : undefined,
                        }}
                      >
                        {group.Name}
                      </Badge>
                    ))}
                  </div>
                </div>
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => sync(project.ID)}
                  >
                    Sync
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      setEditing(project);
                      setOpen(true);
                    }}
                  >
                    {text("Bearbeiten", "Edit")}
                  </Button>
                  <Button
                    variant="destructive"
                    size="sm"
                    onClick={() => remove(project.ID)}
                  >
                    {text("Löschen", "Delete")}
                  </Button>
                </div>
              </div>
            </article>
          ))}
        </div> : <EmptyState title={text("Noch kein Projekt", "No projects yet")} description={text("Lege ein Repository an und ordne es bei Bedarf Boards und Projektgruppen zu.", "Create a repository and assign it to boards and project groups as needed.")} />}
      </CardContent>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editing ? text("Projekt bearbeiten", "Edit project") : text("Neues Projekt", "New project")}
            </DialogTitle>
            <DialogDescription>
              {text("Gruppen werden beim Speichern zugewiesen; nicht verwendete Gruppen verschwinden automatisch.", "Groups are assigned when saved; unused groups are removed automatically.")}
            </DialogDescription>
          </DialogHeader>
          <form className="grid gap-3" onSubmit={save}>
            <label className="grid gap-1 text-sm">
              {text("Name", "Name")}
              <Input name="name" required defaultValue={editing?.Name || ""} />
            </label>
            <label className="grid gap-1 text-sm">
              {text("Repository-URL", "Repository URL")}
              <Input
                name="repository_url"
                defaultValue={editing?.RepositoryURL || ""}
              />
            </label>
            <label className="grid gap-1 text-sm">
              {text("Standard-Branch", "Default branch")}
              <Input
                name="default_branch"
                defaultValue={editing?.DefaultBranch || "main"}
              />
            </label>
            <label className="grid gap-1 text-sm">
              {text("Lokaler Clone-Pfad", "Local clone path")}
              <Input
                name="local_path"
                defaultValue={editing?.LocalPath || ""}
              />
            </label>
            <fieldset className="grid gap-2">
              <legend className="text-sm font-medium">{text("Boards", "Boards")}</legend>
              {boards.map((board) => (
                <label key={board.ID} className="flex gap-2 text-sm">
                  <input
                    type="checkbox"
                    name="board_ids"
                    value={board.ID}
                    defaultChecked={editing && bound(editing, board)}
                  />
                  {board.Name}
                </label>
              ))}
            </fieldset>
            <fieldset className="grid gap-2">
              <legend className="text-sm font-medium">{text("Gruppen", "Groups")}</legend>
              {groups.map((group) => (
                <label key={group.ID} className="flex gap-2 text-sm">
                  <input
                    type="checkbox"
                    name="group_ids"
                    value={group.ID}
                    defaultChecked={editing && grouped(editing, group)}
                  />
                  {group.Name}
                </label>
              ))}
              <Input
                name="new_group"
                placeholder={text("Neue Gruppe, z. B. Inhouse APIs", "New group, e.g. Inhouse APIs")}
              />
            </fieldset>
            <DialogFooter>
              <Button type="submit">{text("Speichern", "Save")}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog open={groupsOpen} onOpenChange={(value) => {
        setGroupsOpen(value);
        if (!value) setEditingGroup(undefined);
      }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{text("Projektgruppen", "Project groups")}</DialogTitle>
            <DialogDescription>
              {text("Gruppen bündeln Projekte für die Auswahl. Eine Gruppe ohne Projekt wird automatisch entfernt.", "Groups bundle projects for selection. A group without a project is removed automatically.")}
            </DialogDescription>
          </DialogHeader>
          {editingGroup ? (
            <form className="grid gap-3" onSubmit={saveGroup}>
              <label className="grid gap-1 text-sm">
                {text("Name", "Name")}
                <Input name="name" required defaultValue={editingGroup.Name} />
              </label>
              <label className="grid gap-1 text-sm">
                {text("Beschreibung", "Description")}
                <textarea name="description" className="min-h-20 rounded-md border bg-transparent p-2" defaultValue={editingGroup.Description} />
              </label>
              <label className="grid gap-1 text-sm">
                {text("Farbe", "Color")}
                <Input name="color" type="color" defaultValue={editingGroup.Color || "#3158d4"} />
              </label>
              <fieldset className="grid gap-2">
                <legend className="text-sm font-medium">{text("Zugehörige Projekte", "Associated projects")}</legend>
                {items.map((project) => (
                  <label key={project.ID} className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      name="project_ids"
                      value={project.ID}
                      defaultChecked={grouped(project, editingGroup)}
                    />
                    {project.Name}
                  </label>
                ))}
              </fieldset>
              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setEditingGroup(undefined)}>{text("Zurück", "Back")}</Button>
                <Button type="button" variant="destructive" onClick={() => deleteGroup(editingGroup.ID)}>{text("Gruppe löschen", "Delete group")}</Button>
                <Button type="submit">{text("Gruppe speichern", "Save group")}</Button>
              </DialogFooter>
            </form>
          ) : groups.length ? (
            <div className="divide-y">
              {groups.map((group) => (
                <article key={group.ID} className="flex items-center justify-between gap-3 py-3 first:pt-0">
                  <div className="min-w-0">
                    <Badge variant="outline" style={{ borderColor: group.Color || undefined, color: group.Color || undefined, backgroundColor: group.Color ? `${group.Color}18` : undefined }}>
                      {group.Name}
                    </Badge>
                    <p className="mt-2 text-xs text-muted-foreground">
                      {group.Projects?.length || 0} {language === "en" ? ((group.Projects?.length || 0) === 1 ? "project" : "projects") : `Projekt${group.Projects?.length === 1 ? "" : "e"}`}
                      {group.Description ? ` · ${group.Description}` : ""}
                    </p>
                  </div>
                  <Button size="sm" variant="outline" onClick={() => setEditingGroup(group)}>{text("Bearbeiten", "Edit")}</Button>
                </article>
              ))}
            </div>
          ) : (
            <EmptyState title={text("Noch keine Gruppen", "No groups yet")} description={text("Lege beim Speichern eines Projekts eine neue Gruppe an.", "Create a new group while saving a project.")} />
          )}
        </DialogContent>
      </Dialog>
    </Card>
  );
}
function Agents() {
  const { text } = useLocale();
  const { data, error } = useAPI<any[]>("/api/v1/agents");
  if (error) return <Failure />;
  if (!data) return <Loading />;
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div>
          <CardTitle>Agents</CardTitle>
          <CardDescription>{text("Agenten öffnen und bearbeiten.", "Open and edit agents.")}</CardDescription>
        </div>
        <div className="flex gap-2">
          <Button asChild variant="outline"><a href="#/agents/templates">{text("Vorlagen", "Templates")}</a></Button>
          <Button asChild><a href="#/agents/new">{text("Agent anlegen", "Create agent")}</a></Button>
        </div>
      </CardHeader>
      <CardContent className="divide-y">
        {data.length ? data.map((agent) => (
          <a
            key={agent.ID}
            href={"#/agents/" + agent.ID}
            className="block py-4 first:pt-0 hover:bg-muted/40"
          >
            <div className="flex justify-between gap-3">
              <strong className="text-sm">{agent.Name}</strong>
              <Badge variant={agent.Enabled ? "secondary" : "outline"}>
                {agent.Enabled ? text("Aktiv", "Active") : text("Inaktiv", "Inactive")}
              </Badge>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              {agent.Description || text("Profil ohne festen Workspace", "Profile without a fixed workspace")}
            </p>
          </a>
        )) : <EmptyState title={text("Noch kein Agent", "No agents yet")} description={text("Lege einen Agenten mit Arbeitsanweisung und erlaubten Skills an.", "Create an agent with instructions and permitted skills.")} actionHref="#/agents/new" actionLabel={text("Agent anlegen", "Create agent")} />}
      </CardContent>
    </Card>
  );
}
function AgentDetail({ id }: { id: string }) {
  const { text } = useLocale();
  const { data, error } = useAPI<any>("/api/v1/agents/" + id);
  const { data: skills } = useAPI<any[]>("/api/v1/skills");
  const [form, setForm] = useState<any>();
  const [selected, setSelected] = useState<string[]>([]);
  const [message, setMessage] = useState("");
  useEffect(() => {
    if (data?.agent) {
      setForm(data.agent);
      setSelected((data.skills || []).map((skill: any) => skill.ID));
    }
  }, [data]);
  if (error) return <Failure />;
  if (!form || !skills) return <Loading />;
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    const body = new FormData();
    body.set("name", form.Name);
    body.set("description", form.Description || "");
    body.set("prompt_prefix", form.PromptPrefix || "");
    body.set("prompt", form.Prompt || "");
    body.set("prompt_suffix", form.PromptSuffix || "");
    body.set("max_parallel_runs", String(form.MaxParallelRuns || 1));
    body.set("enabled", String(form.Enabled));
    try {
      await mutation("/agents/" + id, { method: "POST", body });
      const assigned = new FormData();
      selected.forEach((skill) => assigned.append("skill_ids", skill));
      await mutation("/agents/" + id + "/skills", {
        method: "POST",
        body: assigned,
      });
      setMessage(text("Gespeichert.", "Saved."));
    } catch (err) {
      setMessage(String(err));
    }
  };
  const remove = async () => {
    if (!confirm(text("Agent wirklich löschen?", "Delete this agent?"))) return;
    try {
      await mutation("/agents/" + id + "/delete", { method: "POST" });
      location.hash = "/agents";
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle>{text("Agent bearbeiten", "Edit agent")}</CardTitle>
        <CardDescription>
          {text("Profil, Arbeitsanweisung und erlaubte Skills.", "Profile, instructions, and permitted skills.")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="grid gap-4" onSubmit={save}>
          <label className="grid gap-2 text-sm">
            {text("Name", "Name")}
            <Input
              value={form.Name}
              onChange={(e) => setForm({ ...form, Name: e.target.value })}
            />
          </label>
          <label className="grid gap-2 text-sm">
            {text("Beschreibung", "Description")}
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={form.Description || ""}
              onChange={(e) =>
                setForm({ ...form, Description: e.target.value })
              }
            />
          </label>
          <label className="grid gap-2 text-sm">
            {text("Arbeitsanweisung", "Instructions")}
            <textarea
              className="min-h-32 rounded-lg border bg-transparent p-2"
              value={form.Prompt || ""}
              onChange={(e) => setForm({ ...form, Prompt: e.target.value })}
            />
          </label>
          <label className="grid gap-2 text-sm">
            {text("Prompt-Prefix", "Prompt prefix")}
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={form.PromptPrefix || ""}
              onChange={(e) => setForm({ ...form, PromptPrefix: e.target.value })}
              placeholder={text("Wird vor der Arbeitsanweisung und dem Task-Kontext gesetzt.", "Placed before the instructions and task context.")}
            />
          </label>
          <label className="grid gap-2 text-sm">
            {text("Prompt-Suffix", "Prompt suffix")}
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={form.PromptSuffix || ""}
              onChange={(e) => setForm({ ...form, PromptSuffix: e.target.value })}
              placeholder={text("Definiert Abschluss, Übergabe und Rückmeldungen des Agents.", "Defines completion, handoff, and agent feedback.")}
            />
          </label>
          <label className="grid gap-2 text-sm">
            {text("Maximal parallele Runs", "Maximum parallel runs")}
            <Input
              type="number"
              min="1"
              value={form.MaxParallelRuns || 1}
              onChange={(e) =>
                setForm({ ...form, MaxParallelRuns: Number(e.target.value) })
              }
            />
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={!!form.Enabled}
              onChange={(e) => setForm({ ...form, Enabled: e.target.checked })}
            />{" "}
            {text("Agent aktiv", "Agent active")}
          </label>
          <fieldset className="grid gap-2">
            <legend className="text-sm font-medium">{text("Erlaubte Skills", "Permitted skills")}</legend>
            <div className="flex flex-wrap gap-2">
              {skills.map((skill) => (
                <button
                  type="button"
                  key={skill.ID}
                  onClick={() =>
                    setSelected(
                      selected.includes(skill.ID)
                        ? selected.filter((value) => value !== skill.ID)
                        : [...selected, skill.ID],
                    )
                  }
                  className={
                    selected.includes(skill.ID)
                      ? "rounded-full border border-primary bg-primary px-3 py-1 text-xs text-primary-foreground"
                      : "rounded-full border px-3 py-1 text-xs hover:bg-muted"
                  }
                >
                  {skill.Name}
                </button>
              ))}
            </div>
          </fieldset>
          {message && (
            <p className="text-sm text-muted-foreground">{message}</p>
          )}
          <div className="flex flex-wrap gap-2">
            <Button type="submit">{text("Änderungen speichern", "Save changes")}</Button>
            <Button type="button" variant="destructive" onClick={remove}>
              {text("Löschen", "Delete")}
            </Button>
            <Button asChild type="button" variant="outline"><a href="#/agents">{text("Zurück", "Back")}</a></Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
function AgentForm() {
  const { text } = useLocale();
  const { data: skills } = useAPI<any[]>("/api/v1/skills");
  const [name, setName] = useState("");
  const [prompt, setPrompt] = useState("");
  const [description, setDescription] = useState("");
  const [prefix, setPrefix] = useState("");
  const [suffix, setSuffix] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [message, setMessage] = useState("");
  if (!skills) return <Loading />;
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const form = new FormData();
    form.set("name", name);
    form.set("description", description);
    form.set("prompt_prefix", prefix);
    form.set("prompt", prompt);
    form.set("prompt_suffix", suffix);
    form.set("max_parallel_runs", "1");
    selected.forEach((skill) => form.append("skill_ids", skill));
    try {
      await mutation("/agents", { method: "POST", body: form });
      location.hash = "/agents";
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle>{text("Neuer Agent", "New agent")}</CardTitle>
        <CardDescription>
          {text("Der Run-Workspace wird beim Start aus dem Task-Projektziel erzeugt.", "The run workspace is created at start from the task project target.")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="grid gap-4" onSubmit={submit}>
          <label className="grid gap-2 text-sm">
            {text("Name", "Name")}
            <Input
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label className="grid gap-2 text-sm">
            {text("Beschreibung", "Description")}
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </label>
          <label className="grid gap-2 text-sm">
            {text("Arbeitsanweisung", "Instructions")}
            <textarea
              className="min-h-32 rounded-lg border bg-transparent p-2"
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
            />
          </label>
          <label className="grid gap-2 text-sm">
            {text("Prompt-Prefix", "Prompt prefix")}
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={prefix}
              onChange={(e) => setPrefix(e.target.value)}
              placeholder={text("Fester Kontext vor der Arbeitsanweisung", "Fixed context before the instructions")}
            />
          </label>
          <label className="grid gap-2 text-sm">
            {text("Prompt-Suffix", "Prompt suffix")}
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={suffix}
              onChange={(e) => setSuffix(e.target.value)}
              placeholder={text("Übergabe- und Abschlussregeln", "Handoff and completion rules")}
            />
          </label>
          <fieldset className="grid gap-2">
            <legend className="text-sm font-medium">{text("Erlaubte Skills", "Permitted skills")}</legend>
            <div className="flex flex-wrap gap-2">
              {skills.map((skill) => (
                <button
                  type="button"
                  key={skill.ID}
                  onClick={() =>
                    setSelected(
                      selected.includes(skill.ID)
                        ? selected.filter((value) => value !== skill.ID)
                        : [...selected, skill.ID],
                    )
                  }
                  className={
                    selected.includes(skill.ID)
                      ? "rounded-full border border-primary bg-primary px-3 py-1 text-xs text-primary-foreground"
                      : "rounded-full border px-3 py-1 text-xs hover:bg-muted"
                  }
                >
                  {skill.Name}
                </button>
              ))}
            </div>
          </fieldset>
          {message && <p className="text-sm text-destructive">{message}</p>}
          <Button type="submit">{text("Agent erstellen", "Create agent")}</Button>
        </form>
      </CardContent>
    </Card>
  );
}
function Settings({ route, language }: { route: string; language: Language }) {
  const { text } = useLocale();
  const tab =
    route === "/account"
      ? "account"
      : route === "/settings"
        ? "providers"
        : route.split("/").pop() || "providers";
  const tabs = [
    ["providers", "Provider"],
    ["updates", "Updates"],
    ["agent-policy", text("Agentenrichtlinien", "Agent policies")],
    ["appearance", text("Darstellung", "Appearance")],
    ["integrations", text("Integrationen", "Integrations")],
    ["account", "MCP-Tokens"],
  ];
  return (
    <>
      <div className="mb-5 flex flex-wrap gap-2 border-b pb-4">
        {tabs.map(([id, label]) => (
          <Button
            key={id}
            asChild
            variant={tab === id ? "default" : "ghost"}
            size="sm"
          >
            <a href={id === "account" ? "#/account" : "#/settings/" + id}>{label}</a>
          </Button>
        ))}
      </div>
      {tab === "updates" ? (
        <Updates language={language} />
      ) : tab === "agent-policy" ? (
        <AgentPolicy />
      ) : tab === "appearance" ? (
        <Appearance />
      ) : tab === "integrations" ? (
        <Integrations />
      ) : tab === "account" ? (
        <Account />
      ) : (
        <Providers />
      )}
    </>
  );
}
type UpdateData = {
  current: { version: string; commit: string; builtAt?: string };
  source: { provider: string; repository: string; branch: string };
  status: "up_to_date" | "update_available" | "unavailable" | "unverified" | string;
  release?: { version?: string; commit?: string; publishedAt?: string; changelog?: string; changelogSource?: string; url?: string; verified?: boolean; compatible?: boolean; migrationRequired?: boolean };
  installable?: boolean;
  reason?: string;
  checked_at?: string;
};

function isUpdateData(value: unknown): value is UpdateData {
  const isRecord = (candidate: unknown): candidate is Record<string, unknown> => Boolean(candidate && typeof candidate === "object" && !Array.isArray(candidate));
  const hasOptionalString = (record: Record<string, unknown>, key: string) => !(key in record) || typeof record[key] === "string";
  const hasOptionalBoolean = (record: Record<string, unknown>, key: string) => !(key in record) || typeof record[key] === "boolean";
  if (!isRecord(value)) return false;
  const update = value;
  const current = update.current;
  const source = update.source;
  const release = update.release;
  if (!isRecord(current) || !isRecord(source)) return false;
  if (!hasOptionalString(current, "builtAt") || !hasOptionalString(update, "reason") || !hasOptionalString(update, "checked_at")) return false;
  if ("installable" in update && typeof update.installable !== "boolean") return false;
  if (release !== undefined && !isRecord(release)) return false;
  if (release && (!hasOptionalString(release, "version") || !hasOptionalString(release, "commit") || !hasOptionalString(release, "publishedAt") || !hasOptionalString(release, "changelog") || !hasOptionalString(release, "url") || !hasOptionalBoolean(release, "verified") || !hasOptionalBoolean(release, "compatible") || !hasOptionalBoolean(release, "migrationRequired"))) return false;
  return Boolean(
    typeof current.version === "string" && typeof current.commit === "string" &&
    typeof source.provider === "string" && typeof source.repository === "string" && typeof source.branch === "string" &&
    typeof update.status === "string",
  );
}

function Updates({ language }: { language: Language }) {
  const { text } = useLocale();
  const { data, error } = useAPI<any>("/api/v1/settings/updates");
  const t = (key: string) => translate(language, key);
  const [message, setMessage] = useState("");
  const [progress, setProgress] = useState<any[]>([]);
  const [installing, setInstalling] = useState(false);
  const [showSource, setShowSource] = useState(false);
  const [checking, setChecking] = useState(false);
  const checkingRef = useRef(false);
  const [manualData, setManualData] = useState<UpdateData | undefined>();
  const [checkError, setCheckError] = useState<{ message: string; checkedAt: string }>();
  const result = (manualData || data) as UpdateData | undefined;
  if (error) return <Failure />;
  if (!result) return <Loading />;
  const displayStatus = checkError ? "unavailable" : result.status;
  const release = checkError ? {} : result.release || {};
  const releaseURL = typeof release.url === "string" ? safeMarkdownURL(release.url) : undefined;
  const reason = typeof result.reason === "string" ? result.reason : undefined;
  const available = displayStatus === "update_available" && result.installable;
  const checkFailed = Boolean(checkError) || !["up_to_date", "update_available"].includes(result.status);
  const checkedAt = checkError ? new Date(checkError.checkedAt) : result.checked_at ? new Date(result.checked_at) : undefined;
  const checkedLabel = checkedAt && !Number.isNaN(checkedAt.getTime())
    ? new Intl.DateTimeFormat(language, { dateStyle: "medium", timeStyle: "short" }).format(checkedAt)
    : t("notAvailable");
  const verifyLabel = release.verified && release.compatible ? text("Verifiziert und kompatibel", "Verified and compatible") : text("Nicht zur Installation freigegeben", "Not approved for installation");
  const checkNow = async () => {
    if (checking || checkingRef.current) return;
    checkingRef.current = true;
    setChecking(true);
    setMessage("");
    try {
      // Give React one paint to expose the busy state even when a mocked or
      // cached endpoint resolves synchronously.
      await new Promise<void>((resolve) => window.setTimeout(resolve, 50));
      const response = await fetch("/api/v1/settings/updates", { credentials: "same-origin", cache: "no-store" });
      const payload = await response.json().catch(() => null);
      if (!response.ok || !isUpdateData(payload)) throw new Error(t("updatesCheckFailed"));
      setManualData(payload);
      setCheckError(undefined);
    } catch {
      setCheckError({
        message: t("updatesCheckFailed"),
        checkedAt: new Date().toISOString(),
      });
    } finally {
      checkingRef.current = false;
      setChecking(false);
    }
  };
  const install = async () => {
    if (!available || !confirm(text("Dieses verifizierte Release installieren? Aktive Runs müssen vorher beendet sein.", "Install this verified release? Active runs must be stopped first."))) return;
    setInstalling(true); setMessage(text("Update wird geprüft und für die Wartung vorbereitet …", "The update is being checked and prepared for maintenance …"));
    try {
      const response = await mutation("/api/v1/settings/updates/install", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ confirm: true }) });
      const result = await response.json();
      setProgress(result.progress || []);
      setMessage(result.status === "succeeded" ? text("Update erfolgreich abgeschlossen.", "Update completed successfully.") : text("Update abgeschlossen.", "Update completed."));
    } catch (err) {
      setMessage(String(err));
    } finally { setInstalling(false); }
  };
  return (
    <div className="grid gap-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><GitCompareArrows className="size-5" /> {t("updatesCheckTitle")}</CardTitle>
          <CardDescription>{t("updatesCheckDescription")}</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-primary/20 bg-primary/5 p-4">
            <div>
              <p className="text-sm font-medium">{t("updatesManualTitle")}</p>
              <p className="text-sm text-muted-foreground">{t("updatesReadOnly")}</p>
            </div>
            <Button type="button" onClick={checkNow} disabled={checking} aria-busy={checking}>
              {checking ? <><LoaderCircle className="size-4 animate-spin" aria-hidden="true" /> {t("updatesChecking")}</> : t("updatesCheckNow")}
            </Button>
          </div>
          <p className="sr-only" role="status" aria-live="polite">
            {checking ? t("updatesChecking") : checkFailed ? (checkError?.message || reason || t("updatesCheckFailed")) : displayStatus === "update_available" ? t("updatesAvailable") : t("updatesUpToDate")}
          </p>
          <p className="text-sm text-muted-foreground" aria-label={t("updatesLastChecked")}>{t("updatesLastChecked")}: {checkedLabel}</p>
          <p className={checkFailed ? "rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive" : "sr-only"} role={checkFailed ? "alert" : undefined} aria-live="polite">
            {checkFailed ? (checkError?.message || reason || t("updatesCheckFailed")) : ""}
          </p>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="rounded-lg border p-4"><p className="text-xs text-muted-foreground">{t("updatesCurrentVersion")}</p><p className="mt-1 text-xl font-semibold">{String(result.current.version)}</p><p className="font-mono text-xs text-muted-foreground">{String(result.current.commit)}</p><p className="mt-3 text-sm">{text("Build", "Build")}: {typeof result.current.builtAt === "string" ? result.current.builtAt : t("notAvailable")}</p></div>
            <div className="rounded-lg border p-4"><p className="text-xs text-muted-foreground">{t("updatesStatus")}</p><p className="mt-1 text-xl font-semibold">{displayStatus === "up_to_date" ? t("updatesUpToDate") : displayStatus === "update_available" ? t("updatesAvailable") : t("updatesFailed")}</p><p className="mt-3 text-sm text-muted-foreground">{text("Quelle", "Source")}: {String(result.source.provider)} · {String(result.source.repository)}</p></div>
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle className="flex items-center gap-2"><ShieldAlert className="size-5" /> {text("Nächstes Release", "Next release")}</CardTitle><CardDescription>{verifyLabel}</CardDescription></CardHeader>
        <CardContent className="grid gap-3">
          {release.version || release.changelog ? <>
            {release.version && <div className="grid gap-3 text-sm sm:grid-cols-2"><p><span className="text-muted-foreground">{text("Version", "Version")}</span><br /><strong>{release.version}</strong></p><p><span className="text-muted-foreground">Commit</span><br /><code>{release.commit || text("nicht angegeben", "not specified")}</code></p><p><span className="text-muted-foreground">{text("Veröffentlicht", "Published")}</span><br />{release.publishedAt || text("nicht angegeben", "not specified")}</p><p><span className="text-muted-foreground">{text("Migration", "Migration")}</span><br />{release.migrationRequired ? text("Erforderlich", "Required") : text("Nicht erforderlich", "Not required")}</p></div>}
            {release.changelogSource && <p className="text-sm text-muted-foreground">{text("Quelle", "Source")}: {release.changelogSource}</p>}
            <div className="flex items-center justify-between gap-3"><h3 className="text-base font-semibold">Changelog</h3><Button variant="ghost" size="sm" onClick={() => setShowSource((value) => !value)}>{showSource ? text("Formatierte Ansicht", "Formatted view") : text("Quelltext anzeigen", "Show source")}</Button></div>
            {showSource ? <section aria-label={text("Changelog-Quelltext", "Changelog source")} className="max-h-[34rem] overflow-auto rounded-md bg-muted p-3 text-sm"><pre className="whitespace-pre-wrap break-words">{release.changelog || text("Kein Changelog angegeben.", "No changelog provided.")}</pre></section> : <section aria-label="Changelog" className="max-h-[34rem] overflow-auto rounded-md bg-muted p-4 text-sm">{renderChangelog(release.changelog)}</section>}
            <div className="flex flex-wrap items-center gap-2"><Button disabled={!available || installing} onClick={install}>{installing ? text("Update wird vorbereitet …", "Preparing update …") : text("Update installieren", "Install update")}</Button>{releaseURL && <a className="text-sm underline" href={releaseURL} target="_blank" rel="noreferrer noopener">{text("Auf GitHub ansehen", "View on GitHub")}</a>}</div>
            {progress.length > 0 && <ol className="grid gap-2 rounded-md border p-3 text-sm" aria-label={text("Update-Fortschritt", "Update progress")}>{progress.map((step, index) => <li key={`${step.phase}-${index}`} className="flex items-center justify-between gap-3"><span>{step.phase}</span><span className="text-muted-foreground">{step.status === "succeeded" ? text("Abgeschlossen", "Completed") : step.status === "failed" ? text("Fehlgeschlagen", "Failed") : text("Läuft", "Running")}</span></li>)}</ol>}
            {reason && <p className="text-sm text-muted-foreground">{reason}</p>}
          </> : <p className="text-sm text-muted-foreground">{text("Es wurde kein kompatibles Release gemeldet. Ein Installationsbutton ist deshalb nicht verfügbar.", "No compatible release was reported, so an installation button is unavailable.")}</p>}
          {message && <p className="text-sm text-destructive" role="alert" aria-live="assertive">{message}</p>}
        </CardContent>
      </Card>
    </div>
  );
}

function renderChangelog(source = "") {
  if (!source) return <p className="text-muted-foreground">Kein Changelog angegeben.</p>;
  try {
    return renderMarkdown(source);
  } catch {
    return <div role="alert"><p className="font-medium">Der Changelog konnte nicht formatiert werden.</p><pre className="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words">{source}</pre></div>;
  }
}
function Providers() {
  const { data, error } = useAPI<any[]>("/api/v1/settings/providers");
  const [message, setMessage] = useState("");
  const [drafts, setDrafts] = useState<Record<string, Record<string, unknown>>>({});
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const providers = ["codex", "openai", "claude"].map(
    (name) =>
      data.find((provider) => provider.Provider === name) || {
        Provider: name,
        Model: "",
        Command: name === "codex" ? "codex" : "",
        SecretEnv: "",
        BaseURL: "",
        Options: "{}",
        Enabled: false,
      },
  );
  const save = async (provider: any) => {
    const form = new FormData();
    form.set("model", provider.Model);
    form.set("command", provider.Command);
    form.set("secret_env", provider.SecretEnv);
    form.set("base_url", provider.BaseURL);
    form.set("options", provider.Options || "{}");
    if (provider.Enabled) form.set("enabled", "true");
    try {
      await mutation("/settings/providers/" + provider.Provider, {
        method: "POST",
        body: form,
      });
      setDrafts((current) => {
        const { [provider.Provider]: _saved, ...remaining } = current;
        return remaining;
      });
      setMessage(`${provider.Provider} gespeichert.`);
    } catch (err) {
      setMessage(String(err));
    }
  };
  const update = (provider: any, field: string, value: unknown) => {
    setDrafts((current) => ({
      ...current,
      [provider.Provider]: { ...current[provider.Provider], [field]: value },
    }));
  };
  const test = async (provider: any) => {
    try {
      const response = await mutation("/settings/providers/" + provider.Provider + "/test", { method: "POST" });
      const result = await response.json();
      setMessage(`${provider.Provider}: ${result.result || "Verbindung erfolgreich geprüft."}`);
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle>Provider</CardTitle>
        <CardDescription>
          Provider werden dynamisch konfiguriert; Secrets bleiben in den
          Umgebungsvariablen des Hosts.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        {providers.map((source) => {
          const provider = { ...source, ...drafts[source.Provider] };
          return <details key={provider.Provider} className="rounded-lg border p-4">
            <summary className="cursor-pointer font-medium capitalize">
              {provider.Provider}{" "}
              <span className="ml-2 text-xs text-muted-foreground">
                {provider.Model || "nicht konfiguriert"}
              </span>
            </summary>
            <div className="mt-4 grid gap-3">
              <label className="grid gap-1 text-sm">
                Modell
                <Input
                  value={provider.Model || ""}
                  onChange={(e) => update(provider, "Model", e.target.value)}
                />
              </label>
              <label className="grid gap-1 text-sm">
                Kommando / Adapter
                <Input
                  value={provider.Command || ""}
                  onChange={(e) => update(provider, "Command", e.target.value)}
                />
              </label>
              <label className="grid gap-1 text-sm">
                Secret-Umgebungsvariable
                <Input
                  value={provider.SecretEnv || ""}
                  onChange={(e) => update(provider, "SecretEnv", e.target.value)}
                />
              </label>
              <label className="grid gap-1 text-sm">
                Base URL
                <Input
                  value={provider.BaseURL || ""}
                  onChange={(e) => update(provider, "BaseURL", e.target.value)}
                />
              </label>
              <label className="grid gap-1 text-sm">
                Zusatzoptionen (JSON)
                <textarea
                  className="min-h-24 rounded-lg border bg-transparent p-2"
                  value={provider.Options || "{}"}
                  onChange={(e) => update(provider, "Options", e.target.value)}
                />
              </label>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={Boolean(provider.Enabled)}
                  onChange={(e) => update(provider, "Enabled", e.target.checked)}
                />{" "}
                Aktiv
              </label>
              <div className="flex flex-wrap gap-2">
                <Button className="w-fit" onClick={() => save(provider)}>
                  Provider speichern
                </Button>
                <Button className="w-fit" variant="outline" onClick={() => test(provider)}>
                  Verbindung testen
                </Button>
              </div>
            </div>
          </details>;
        })}
        {message && <p className="text-sm text-muted-foreground">{message}</p>}
      </CardContent>
    </Card>
  );
}
function AgentPolicy() {
  const { data, error } = useAPI<any>("/api/v1/settings/agent-policy");
  const [message, setMessage] = useState("");
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await mutation("/settings/agent-policy", {
        method: "POST",
        body: new FormData(e.target as HTMLFormElement),
      });
      setMessage("Richtlinien gespeichert.");
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle>Agentenrichtlinien</CardTitle>
        <CardDescription>
          Diese Abschnitte umschließen jede Arbeitsanweisung.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="grid gap-4" onSubmit={save}>
          <label className="grid gap-1 text-sm">
            Globaler Prefix
            <textarea
              name="prompt_prefix"
              className="min-h-32 rounded-lg border bg-transparent p-2"
              defaultValue={data.prefix}
            />
          </label>
          <label className="grid gap-1 text-sm">
            Globaler Suffix
            <textarea
              name="prompt_suffix"
              className="min-h-32 rounded-lg border bg-transparent p-2"
              defaultValue={data.suffix}
            />
          </label>
          <Button className="w-fit" type="submit">
            Richtlinien speichern
          </Button>
          {message && (
            <p className="text-sm text-muted-foreground">{message}</p>
          )}
        </form>
      </CardContent>
    </Card>
  );
}
function Appearance() {
  const { data, error } = useAPI<any>("/api/v1/settings/appearance");
  const [message, setMessage] = useState("");
  const [selectedLanguage, setSelectedLanguage] = useState<Language>("de");
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const currentLanguage = selectedLanguage === "de" && data.Language === "en" ? "en" : selectedLanguage;
  const appearanceText = (key: string) => translate(currentLanguage, key);
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await mutation("/settings/appearance", {
        method: "POST",
        body: new FormData(e.target as HTMLFormElement),
      });
      const nextLanguage = normalizeLanguage(new FormData(e.target as HTMLFormElement).get("language"));
      setSelectedLanguage(nextLanguage);
      window.dispatchEvent(new CustomEvent("shipyard:language-change", { detail: nextLanguage }));
      refreshData();
      setMessage(translate(nextLanguage, "saved"));
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle>{appearanceText("appearance")}</CardTitle>
        <CardDescription>
          {appearanceText("appearanceDescription")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="grid gap-5" onSubmit={save}>
          <fieldset className="grid gap-2">
            <legend className="font-medium">{appearanceText("language")}</legend>
            <label className="grid gap-1 text-sm">
              {appearanceText("languageDescription")}
              <select name="language" value={currentLanguage} onChange={(event) => {
                const next = normalizeLanguage(event.target.value);
                setSelectedLanguage(next);
                // Keep the entire React shell in sync before the preference
                // request completes. The server remains authoritative after
                // reload/login, while this event makes the switch immediate.
                window.dispatchEvent(new CustomEvent("shipyard:language-change", { detail: next }));
              }}>
                <option value="de">{appearanceText("german")}</option>
                <option value="en">{appearanceText("english")}</option>
              </select>
            </label>
          </fieldset>
          <fieldset className="grid gap-2">
            <legend className="font-medium">{appearanceText("display")}</legend>
            {["system", "light", "dark"].map((value) => (
              <label key={value} className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="theme"
                  value={value}
                  defaultChecked={data.Theme === value}
                />
                {value === "system"
                  ? appearanceText("systemTheme")
                  : value === "light"
                    ? appearanceText("light")
                    : appearanceText("dark")}
              </label>
            ))}
          </fieldset>
          <fieldset className="grid gap-2">
            <legend className="font-medium">{appearanceText("keyboard")}</legend>
            <label className="flex items-center gap-2 text-sm">
              <input
                name="shortcut_hints"
                type="checkbox"
                value="true"
                defaultChecked={data.ShortcutHints}
              />{" "}
              {appearanceText("shortcutHints")}
            </label>
          </fieldset>
          <Button className="w-fit" type="submit">
            {appearanceText("save")}
          </Button>
          {message && (
            <p className="text-sm text-muted-foreground">{message}</p>
          )}
        </form>
      </CardContent>
    </Card>
  );
}
function Integrations() {
  const { data, error } = useAPI<any[]>("/api/v1/settings/integrations");
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await mutation("/settings/integrations", {
        method: "POST",
        body: new FormData(e.target as HTMLFormElement),
      });
      setOpen(false);
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const remove = async (id: string) => {
    if (!confirm("Integration wirklich entfernen?")) return;
    try {
      await mutation("/settings/integrations/" + id + "/delete", {
        method: "POST",
      });
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <Card>
      <CardHeader className="flex flex-row justify-between gap-3">
        <div>
          <CardTitle>Integrationen</CardTitle>
          <CardDescription>
            GitHub, GitLab und Codeberg als Projektquellen vorbereiten.
          </CardDescription>
        </div>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button>Quelle hinzufügen</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Projektquelle hinzufügen</DialogTitle>
              <DialogDescription>
                Es werden noch keine Zugangsdaten abgefragt.
              </DialogDescription>
            </DialogHeader>
            <form className="grid gap-3" onSubmit={create}>
              <label className="grid gap-1 text-sm">
                Provider
                <select
                  name="provider"
                  className="h-9 rounded-md border bg-background px-2"
                >
                  <option value="github">GitHub</option>
                  <option value="gitlab">GitLab</option>
                  <option value="codeberg">Codeberg</option>
                </select>
              </label>
              <label className="grid gap-1 text-sm">
                Bezeichnung
                <Input name="label" />
              </label>
              <label className="grid gap-1 text-sm">
                Eigene Basis-URL
                <Input
                  name="base_url"
                  placeholder="https://gitlab.example.com"
                />
              </label>
              <DialogFooter>
                <Button type="submit">Quelle vorbereiten</Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent className="divide-y">
        {message && <p className="mb-3 text-sm text-destructive">{message}</p>}
        {data.length ? (
          data.map((connection) => (
            <article
              key={connection.ID}
              className="flex justify-between gap-4 py-4 first:pt-0"
            >
              <div>
                <strong className="text-sm">{connection.Label}</strong>
                <p className="mt-1 text-sm text-muted-foreground">
                  {connection.Provider} · {connection.Status}
                  {connection.BaseURL && ` · ${connection.BaseURL}`}
                </p>
              </div>
              <Button
                size="sm"
                variant="destructive"
                onClick={() => remove(connection.ID)}
              >
                Entfernen
              </Button>
            </article>
          ))
        ) : (
          <p className="py-12 text-center text-sm text-muted-foreground">
            Noch keine Projektquellen.
          </p>
        )}
      </CardContent>
    </Card>
  );
}
function Account() {
  const { data, error } = useAPI<any>("/api/v1/account");
  const [token, setToken] = useState("");
  const [message, setMessage] = useState("");
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const response = await mutation("/api/v1/account/tokens", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          Name: new FormData(e.target as HTMLFormElement).get("name"),
        }),
      });
      const result = await response.json();
      setToken(result.token);
      setMessage(
        "Token wurde erstellt. Kopiere ihn jetzt; er wird nicht erneut angezeigt.",
      );
    } catch (err) {
      setMessage(String(err));
    }
  };
  const revoke = async (id: string) => {
    if (!confirm("Token wirklich widerrufen?")) return;
    try {
      await mutation("/account/tokens/" + id + "/revoke", { method: "POST" });
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const logout = async () => {
    try {
      await mutation("/logout", { method: "POST" });
      location.href = "/login";
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>MCP-Token erstellen</CardTitle>
          <CardDescription>
            Tokens geben einem Coding-Agent Zugriff im Namen deines Kontos.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="grid gap-3" onSubmit={create}>
            <Input required name="name" placeholder="Lokaler Codex" />
            <Button className="w-fit" type="submit">
              Token erzeugen
            </Button>
          </form>
          {message && (
            <p className="mt-4 text-sm text-muted-foreground">{message}</p>
          )}
          {token && (
            <code className="mt-3 block overflow-x-auto rounded-md bg-muted p-3 text-xs">
              {token}
            </code>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Aktive Tokens</CardTitle>
          <CardDescription>
            {data.user.DisplayName} · {data.user.Email}
          </CardDescription>
        </CardHeader>
        <CardContent className="divide-y">
          {data.tokens.map((record: any) => (
            <article
              key={record.ID}
              className="flex justify-between gap-3 py-3 first:pt-0"
            >
              <div>
                <strong className="text-sm">{record.Name}</strong>
                <p className="mt-1 text-xs text-muted-foreground">
                  {record.Prefix}…
                </p>
              </div>
              <Button
                size="sm"
                variant="destructive"
                onClick={() => revoke(record.ID)}
              >
                Widerrufen
              </Button>
            </article>
          ))}
          <Button className="mt-5" variant="outline" onClick={logout}>
            Abmelden
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}
function Loading() {
  const { text } = useLocale();
  return (
    <Card>
      <CardContent className="flex min-h-56 items-center justify-center gap-3 text-sm text-muted-foreground">
        <LoaderCircle className="size-5 animate-spin" />
        {text("Lade Betriebsdaten …", "Loading operational data …")}
      </CardContent>
    </Card>
  );
}
function Failure() {
  const { text } = useLocale();
  return (
    <Card>
      <CardContent className="flex min-h-56 flex-col items-center justify-center gap-4 py-12 text-center">
        <p className="text-sm text-destructive">
          {text("Daten konnten nicht geladen werden. Bitte erneut versuchen.", "Data could not be loaded. Please try again.")}
        </p>
        <Button type="button" size="sm" variant="outline" onClick={() => refreshData()}>
          {text("Daten erneut laden", "Reload data")}
        </Button>
      </CardContent>
    </Card>
  );
}
function NotFound() {
  const { text } = useLocale();
  return (
    <Card>
      <CardContent className="flex min-h-56 flex-col items-center justify-center gap-4 py-12 text-center">
        <p className="text-sm font-medium">{text("Diese Ansicht gibt es nicht.", "This view does not exist.")}</p>
        <p className="max-w-md text-sm text-muted-foreground">
          {text("Öffne die Übersicht oder wähle einen Bereich aus der Navigation.", "Open the overview or select a section from the navigation.")}
        </p>
        <Button asChild size="sm">
          <a href="#/">{text("Zur Übersicht", "Go to overview")}</a>
        </Button>
      </CardContent>
    </Card>
  );
}
function EmptyState({
  title,
  description,
  actionHref,
  actionLabel,
}: {
  title: string;
  description: string;
  actionHref?: string;
  actionLabel?: string;
}) {
  return (
    <div className="flex min-h-40 flex-col items-center justify-center px-4 py-10 text-center">
      <p className="text-sm font-medium">{title}</p>
      <p className="mt-1 max-w-md text-sm text-muted-foreground">{description}</p>
      {actionHref && actionLabel && (
        <Button asChild className="mt-4" size="sm">
          <a href={actionHref}>{actionLabel}</a>
        </Button>
      )}
    </div>
  );
}
function AgentTemplates() {
  const [name, setName] = useState("");
  const [kind, setKind] = useState("implementation");
  const [message, setMessage] = useState("");
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const form = new FormData();
    form.set("name", name);
    form.set("kind", kind);
    try {
      await mutation("/agents/templates", { method: "POST", body: form });
      location.hash = "/agents";
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle>Agent-Vorlagen</CardTitle>
        <CardDescription>
          Starte mit einer klaren Rolle und passe sie anschließend im
          Agent-Profil an.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="grid gap-4" onSubmit={submit}>
          <label className="grid gap-1 text-sm">
            Name
            <Input
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label className="grid gap-1 text-sm">
            Vorlage
            <select
              value={kind}
              onChange={(e) => setKind(e.target.value)}
              className="h-9 rounded-md border bg-background px-2"
            >
              <option value="implementation">Implementierung</option>
              <option value="review">Code Review</option>
              <option value="docs">Dokumentation</option>
            </select>
          </label>
          {message && <p className="text-sm text-destructive">{message}</p>}
          <div className="flex gap-2">
            <Button type="submit">Agent aus Vorlage anlegen</Button>
            <Button asChild type="button" variant="outline"><a href="#/agents">Zurück</a></Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
function WorkflowEditorV2({ id }: { id: string }) {
  const { data, error } = useAPI<any>("/api/v1/boards/" + id);
  const [newOpen, setNewOpen] = useState(false);
  const [transitionOpen, setTransitionOpen] = useState(false);
  const [column, setColumn] = useState<any>();
  const [transition, setTransition] = useState<any>();
  const [message, setMessage] = useState("");
  const [positions, setPositions] = useState<Record<string, { x: number; y: number }>>({});
  const drag = useRef<{ id: string; offsetX: number; offsetY: number; moved: boolean } | undefined>(undefined);
  const suppressClick = useRef(false);
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const submit = async (path: string, e: React.FormEvent) => {
    e.preventDefault();
    try {
      await mutation(path, {
        method: "POST",
        body: new FormData(e.target as HTMLFormElement),
      });
      // A successful mutation must return the operator to the canvas. Keeping
      // the form open made touch users able to submit the same create form a
      // second time and obscured the newly created node behind the dialog.
      if (path === "/boards/" + id + "/columns") setNewOpen(false);
      if (path === "/boards/" + id + "/transitions") setTransitionOpen(false);
      if (path.startsWith("/columns/")) setColumn(undefined);
      if (path.startsWith("/transitions/")) setTransition(undefined);
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const remove = async (path: string, label: string) => {
    if (!confirm(`${label} wirklich löschen?`)) return;
    try {
      await mutation(path, { method: "POST" });
      if (path.startsWith("/columns/")) setColumn(undefined);
      if (path.startsWith("/transitions/")) setTransition(undefined);
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const name = (columnID: string) =>
    data.Columns.find((value: any) => value.ID === columnID)?.Name ||
    "Unbekannt";
  const point = (columnID: string) => {
    const value = data.Columns.find((item: any) => item.ID === columnID);
    const current = positions[columnID];
    return { x: (current?.x ?? value?.CanvasX ?? 0) + 80, y: (current?.y ?? value?.CanvasY ?? 0) + 42 };
  };
  const position = (value: any) => positions[value.ID] || { x: value.CanvasX || 0, y: value.CanvasY || 0 };
  const persistPosition = async (columnID: string, x: number, y: number) => {
    const form = new FormData();
    form.set("x", String(x));
    form.set("y", String(y));
    try {
      await mutation("/columns/" + columnID + "/position", { method: "POST", body: form });
    } catch (err) {
      setMessage(String(err));
      setPositions({});
    }
  };
  return (
    <>
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <a
          href={"#/boards/" + id}
          className="text-sm text-muted-foreground hover:text-foreground"
        >
          ← Zum Board
        </a>
        <div className="flex gap-2">
          <Dialog open={transitionOpen} onOpenChange={setTransitionOpen}>
            <DialogTrigger asChild><Button variant="outline">Transition anlegen</Button></DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Neue Transition</DialogTitle>
                <DialogDescription>Definiert einen erlaubten Wechsel zwischen zwei Spalten.</DialogDescription>
              </DialogHeader>
              <form className="grid gap-3" onSubmit={(e) => submit("/boards/" + id + "/transitions", e)}>
                <label className="grid gap-1 text-sm">Von
                  <select required name="from" className="h-9 rounded-md border bg-background px-2">
                    <option value="">Spalte wählen</option>
                    {data.Columns.map((value: any) => <option key={value.ID} value={value.ID}>{value.Name}</option>)}
                  </select>
                </label>
                <label className="grid gap-1 text-sm">Nach
                  <select required name="to" className="h-9 rounded-md border bg-background px-2">
                    <option value="">Spalte wählen</option>
                    {data.Columns.map((value: any) => <option key={value.ID} value={value.ID}>{value.Name}</option>)}
                  </select>
                </label>
                <label className="grid gap-1 text-sm">Aktionsname
                  <Input name="action_name" required placeholder="z. B. Zur Review geben" />
                </label>
                <DialogFooter><Button type="submit">Transition speichern</Button></DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
          <Dialog open={newOpen} onOpenChange={setNewOpen}>
            <DialogTrigger asChild>
              <Button>Spalte anlegen</Button>
            </DialogTrigger>
            <DialogContent>
            <DialogHeader>
              <DialogTitle>Neue Workflow-Spalte</DialogTitle>
            </DialogHeader>
            <form
              className="grid gap-3"
              onSubmit={(e) => submit("/boards/" + id + "/columns", e)}
            >
              <label className="grid gap-1 text-sm">
                Name
                <Input name="name" required />
              </label>
              <label className="grid gap-1 text-sm">
                Spaltentyp
                <select
                  name="column_type"
                  className="h-9 rounded-md border bg-background px-2"
                >
                  <option value="standard">Standard</option>
                  <option value="inbox">Inbox</option>
                  <option value="needs_action">Needs action</option>
                  <option value="done">Done</option>
                </select>
              </label>
              <DialogFooter>
                <Button type="submit">Spalte erstellen</Button>
              </DialogFooter>
            </form>
            </DialogContent>
          </Dialog>
        </div>
      </div>
      {message && <p className="mb-3 text-sm text-destructive">{message}</p>}
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_21rem]">
        <Card>
          <CardHeader>
            <CardTitle>Flow-Canvas</CardTitle>
            <CardDescription>
              Die Koordinaten werden pro Spalte gespeichert. Klicke eine Spalte,
              um Name, Typ oder Position zu ändern.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div className="overflow-auto rounded-xl border">
              <div
                className="relative h-[32rem] min-w-[68rem] bg-[linear-gradient(to_right,hsl(var(--border))_1px,transparent_1px),linear-gradient(to_bottom,hsl(var(--border))_1px,transparent_1px)] bg-[size:24px_24px]"
                aria-label="Workflow-Canvas"
              >
                <svg className="pointer-events-none absolute inset-0 h-full w-full" aria-hidden="true">
                  <defs>
                    <marker id="workflow-arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                      <path d="M 0 0 L 10 5 L 0 10 z" className="fill-primary" />
                    </marker>
                  </defs>
                  {data.Transitions.map((edge: any) => {
                    const from = point(edge.FromColumnID);
                    const to = point(edge.ToColumnID);
                    const middle = Math.round((from.x + to.x) / 2);
                    return <path key={edge.ID} d={`M ${from.x} ${from.y} H ${middle} V ${to.y} H ${to.x}`} fill="none" className="stroke-primary" strokeWidth="2" markerEnd="url(#workflow-arrow)" opacity="0.75" />;
                  })}
                </svg>
                {data.Columns.map((value: any) => {
                  const current = position(value);
                  return (
                  <button
                    type="button"
                    key={value.ID}
                    onClick={() => {
                      if (!suppressClick.current) setColumn(value);
                    }}
                    onPointerDown={(event) => {
                      if (event.button !== 0) return;
                      const canvas = event.currentTarget.parentElement?.getBoundingClientRect();
                      if (!canvas) return;
                      event.currentTarget.setPointerCapture(event.pointerId);
                      drag.current = {
                        id: value.ID,
                        offsetX: event.clientX - canvas.left - current.x,
                        offsetY: event.clientY - canvas.top - current.y,
                        moved: false,
                      };
                    }}
                    onPointerMove={(event) => {
                      const active = drag.current;
                      const canvas = event.currentTarget.parentElement?.getBoundingClientRect();
                      if (!active || active.id !== value.ID || !canvas) return;
                      const x = Math.max(0, Math.min(920, Math.round(event.clientX - canvas.left - active.offsetX)));
                      const y = Math.max(0, Math.min(430, Math.round(event.clientY - canvas.top - active.offsetY)));
                      if (Math.abs(x - current.x) + Math.abs(y - current.y) > 3) active.moved = true;
                      setPositions((old) => ({ ...old, [value.ID]: { x, y } }));
                    }}
                    onPointerUp={() => {
                      const active = drag.current;
                      drag.current = undefined;
                      if (!active || active.id !== value.ID || !active.moved) return;
                      const final = positions[value.ID] || current;
                      suppressClick.current = true;
                      window.setTimeout(() => { suppressClick.current = false; }, 0);
                      void persistPosition(value.ID, final.x, final.y);
                    }}
                    style={{ left: current.x, top: current.y, touchAction: "none" }}
                    className="absolute w-40 cursor-grab rounded-xl border bg-card p-3 text-left shadow-sm transition hover:border-primary active:cursor-grabbing focus-visible:outline-primary"
                  >
                    <div className="flex justify-between gap-2">
                      <strong className="text-sm">{value.Name}</strong>
                      <Badge variant="outline">{value.Type}</Badge>
                    </div>
                    <p className="mt-3 text-xs text-muted-foreground">{current.x}, {current.y}</p>
                  </button>
                  );
                })}
              </div>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Transitionen</CardTitle>
            <CardDescription>
              {data.Transitions.length} erlaubte Wechsel.
            </CardDescription>
          </CardHeader>
          <CardContent className="divide-y">
            {data.Transitions.map((value: any) => (
              <button
                type="button"
                key={value.ID}
                onClick={() => setTransition(value)}
                className="flex w-full items-center justify-between gap-2 py-3 text-left first:pt-0"
              >
                <span className="text-sm">
                  <strong>{value.ActionName || "Wechsel"}</strong>
                  <br />
                  <span className="text-muted-foreground">
                    {name(value.FromColumnID)} → {name(value.ToColumnID)}
                  </span>
                </span>
                <span className="text-xs text-primary">Bearbeiten</span>
              </button>
            ))}
          </CardContent>
        </Card>
      </div>
      <Dialog
        open={!!column}
        onOpenChange={(open) => !open && setColumn(undefined)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Spalte bearbeiten</DialogTitle>
          </DialogHeader>
          {column && (
            <form
              className="grid gap-3"
              onSubmit={(e) => submit("/columns/" + column.ID, e)}
            >
              <label className="grid gap-1 text-sm">
                Name
                <Input name="name" required defaultValue={column.Name} />
              </label>
              <label className="grid gap-1 text-sm">
                Typ
                <select
                  name="column_type"
                  defaultValue={column.Type}
                  className="h-9 rounded-md border bg-background px-2"
                >
                  <option value="standard">Standard</option>
                  <option value="inbox">Inbox</option>
                  <option value="needs_action">Needs action</option>
                  <option value="done">Done</option>
                </select>
              </label>
              <div className="grid grid-cols-2 gap-3">
                <label className="grid gap-1 text-sm">
                  Canvas X
                  <Input name="x" type="number" defaultValue={column.CanvasX} />
                </label>
                <label className="grid gap-1 text-sm">
                  Canvas Y
                  <Input name="y" type="number" defaultValue={column.CanvasY} />
                </label>
              </div>
              <DialogFooter>
                <Button type="submit">Speichern</Button>
                <Button
                  type="button"
                  variant="destructive"
                  onClick={() =>
                    remove("/columns/" + column.ID + "/delete", "Spalte")
                  }
                >
                  Löschen
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!transition}
        onOpenChange={(open) => !open && setTransition(undefined)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Transition bearbeiten</DialogTitle>
          </DialogHeader>
          {transition && (
            <form
              className="grid gap-3"
              onSubmit={(e) => submit("/transitions/" + transition.ID, e)}
            >
              <label className="grid gap-1 text-sm">
                Von
                <select
                  name="from"
                  defaultValue={transition.FromColumnID}
                  className="h-9 rounded-md border bg-background px-2"
                >
                  {data.Columns.map((value: any) => (
                    <option key={value.ID} value={value.ID}>
                      {value.Name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="grid gap-1 text-sm">
                Nach
                <select
                  name="to"
                  defaultValue={transition.ToColumnID}
                  className="h-9 rounded-md border bg-background px-2"
                >
                  {data.Columns.map((value: any) => (
                    <option key={value.ID} value={value.ID}>
                      {value.Name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="grid gap-1 text-sm">
                Aktionsname
                <Input
                  name="action_name"
                  defaultValue={transition.ActionName}
                />
              </label>
              <DialogFooter>
                <Button type="submit">Speichern</Button>
                <Button
                  type="button"
                  variant="destructive"
                  onClick={() =>
                    remove(
                      "/transitions/" + transition.ID + "/delete",
                      "Transition",
                    )
                  }
                >
                  Löschen
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

export function WorkflowEditor({ id }: { id: string }) {
  const { data, error } = useAPI<any>("/api/v1/boards/" + id);
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const reload = () => refreshData();
  const addColumn = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await mutation("/boards/" + id + "/columns", {
        method: "POST",
        body: new FormData(e.target as HTMLFormElement),
      });
      reload();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const addTransition = async (from: string, to: string) => {
    const name = prompt("Aktionsname für den Wechsel:");
    if (name === null) return;
    const form = new FormData();
    form.set("from", from);
    form.set("to", to);
    form.set("action_name", name);
    try {
      await mutation("/boards/" + id + "/transitions", {
        method: "POST",
        body: form,
      });
      reload();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const deleteTransition = async (transition: any) => {
    if (!confirm("Transition wirklich löschen?")) return;
    try {
      await mutation("/transitions/" + transition.ID + "/delete", {
        method: "POST",
      });
      reload();
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <>
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <a
          href={"#/boards/" + id}
          className="text-sm text-muted-foreground hover:text-foreground"
        >
          ← Zum Board
        </a>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button>Spalte anlegen</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Neue Workflow-Spalte</DialogTitle>
              <DialogDescription>
                Spezielle Spaltentypen darf es pro Board nur einmal geben.
              </DialogDescription>
            </DialogHeader>
            <form className="grid gap-3" onSubmit={addColumn}>
              <label className="grid gap-1 text-sm">
                Name
                <Input required name="name" />
              </label>
              <label className="grid gap-1 text-sm">
                Typ
                <select
                  name="column_type"
                  className="h-9 rounded-md border bg-background px-3"
                >
                  <option value="standard">Standard</option>
                  <option value="inbox">Inbox</option>
                  <option value="needs_action">Needs action</option>
                  <option value="done">Done</option>
                </select>
              </label>
              <DialogFooter>
                <Button type="submit">Spalte speichern</Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </div>
      {message && <p className="mb-3 text-sm text-destructive">{message}</p>}
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_19rem]">
        <Card>
          <CardHeader>
            <CardTitle>Workflow-Editor</CardTitle>
            <CardDescription>
              Wähle an einem Knoten „Übergang von hier“ und dann das Ziel.
              Bestehende Wechsel können entfernt werden.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              {data.Columns.map((column: any) => (
                <article
                  key={column.ID}
                  className="rounded-xl border bg-card p-4"
                >
                  <div className="flex items-start justify-between gap-2">
                    <div>
                      <strong>{column.Name}</strong>
                      <p className="mt-1 text-xs text-muted-foreground">
                        {column.Type || "standard"}
                      </p>
                    </div>
                    <Badge variant="outline">
                      {
                        data.Tasks.filter(
                          (task: any) => task.ColumnID === column.ID,
                        ).length
                      }
                    </Badge>
                  </div>
                  <details className="mt-4">
                    <summary className="cursor-pointer text-sm text-primary">
                      Übergang von hier
                    </summary>
                    <div className="mt-3 grid gap-2">
                      {data.Columns.filter(
                        (candidate: any) => candidate.ID !== column.ID,
                      ).map((candidate: any) => (
                        <Button
                          key={candidate.ID}
                          type="button"
                          size="sm"
                          variant="outline"
                          onClick={() => addTransition(column.ID, candidate.ID)}
                        >
                          → {candidate.Name}
                        </Button>
                      ))}
                    </div>
                  </details>
                </article>
              ))}
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Transitionen</CardTitle>
            <CardDescription>
              {data.Transitions.length} definierte Wechsel.
            </CardDescription>
          </CardHeader>
          <CardContent className="divide-y">
            {data.Transitions.map((transition: any) => {
              const from =
                data.Columns.find(
                  (column: any) => column.ID === transition.FromColumnID,
                )?.Name || "Unbekannt";
              const to =
                data.Columns.find(
                  (column: any) => column.ID === transition.ToColumnID,
                )?.Name || "Unbekannt";
              return (
                <article
                  key={transition.ID}
                  className="flex items-center justify-between gap-2 py-3 first:pt-0"
                >
                  <p className="text-sm">
                    <strong>{transition.ActionName || "Wechsel"}</strong>
                    <br />
                    <span className="text-muted-foreground">
                      {from} → {to}
                    </span>
                  </p>
                  <Button
                    size="sm"
                    variant="destructive"
                    onClick={() => deleteTransition(transition)}
                  >
                    Löschen
                  </Button>
                </article>
              );
            })}
          </CardContent>
        </Card>
      </div>
    </>
  );
}
type BoardFilters = { search: string; column: string; priority: string; label: string; project: string };

function emptyBoardFilters(): BoardFilters {
  return { search: "", column: "", priority: "", label: "", project: "" };
}

function readBoardFilters(boardID: string): BoardFilters {
  try {
    const value = JSON.parse(sessionStorage.getItem(`shipyard-board-filters:${boardID}`) || "null");
    return { ...emptyBoardFilters(), ...(value && typeof value === "object" ? value : {}) };
  } catch {
    return emptyBoardFilters();
  }
}

function normalized(value: unknown): string {
  return String(value || "").toLocaleLowerCase();
}

function priorityLabel(priority: string): string {
  return ({ urgent: "Dringend", high: "Hoch", normal: "Normal", low: "Niedrig" } as Record<string, string>)[priority] || priority;
}

function matchesBoardFilters(task: any, boardID: string, columns: any[], filters: BoardFilters, search: string): boolean {
  const query = normalized(search);
  const textMatches = !query || normalized(`${task.Title} ${task.Description}`).includes(query);
  const labelMatches = !filters.label || task.Labels?.some((label: any) => label.ID === filters.label);
  const projectMatches = !filters.project || task.TargetProjects?.some((project: any) => project.ID === filters.project);
  return task.BoardID === boardID && textMatches && (!filters.column || task.ColumnID === filters.column) && (!filters.priority || task.Priority === filters.priority) && labelMatches && projectMatches && columns.some((column: any) => column.ID === task.ColumnID);
}

function BoardDetail({ id }: { id: string }) {
  const TOUCH_LONG_PRESS_DELAY = 425;
  const TOUCH_MOVE_TOLERANCE = 10;
  const { data, error } = useAPI<any>("/api/v1/boards/" + id);
  const [open, setOpen] = useState(false);
  const [labelsOpen, setLabelsOpen] = useState(false);
  const [settings, setSettings] = useState(false);
  const [title, setTitle] = useState("");
  const [message, setMessage] = useState("");
  const [filters, setFilters] = useState(() => readBoardFilters(id));
  const [searchInput, setSearchInput] = useState(filters.search);
  const [debouncedSearch, setDebouncedSearch] = useState(filters.search);
  const filterBoardID = useRef(id);
  const boardChanged = filterBoardID.current !== id;
  const [draggedTask, setDraggedTask] = useState("");
  const touchDrag = useRef<{ taskID: string; startX: number; startY: number; active: boolean; timer: number } | undefined>(undefined);
  const suppressTaskClick = useRef(false);
  const cancelTouchDrag = () => {
    const pending = touchDrag.current;
    if (pending) window.clearTimeout(pending.timer);
    touchDrag.current = undefined;
    setDraggedTask("");
  };
  useEffect(() => {
    const next = readBoardFilters(id);
    filterBoardID.current = id;
    setFilters(next);
    setSearchInput(next.search);
    setDebouncedSearch(next.search);
  }, [id]);
  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedSearch(searchInput.trim());
      setFilters((current) => ({ ...current, search: searchInput.trim() }));
    }, 180);
    return () => window.clearTimeout(timer);
  }, [searchInput]);
  useEffect(() => {
    const cancel = () => cancelTouchDrag();
    window.addEventListener("pointercancel", cancel);
    window.addEventListener("blur", cancel);
    document.addEventListener("visibilitychange", cancel);
    return () => {
      window.removeEventListener("pointercancel", cancel);
      window.removeEventListener("blur", cancel);
      document.removeEventListener("visibilitychange", cancel);
      cancelTouchDrag();
    };
  }, [id]);
  useEffect(() => {
    if (open || labelsOpen || settings) cancelTouchDrag();
  }, [open, labelsOpen, settings]);
  useEffect(() => {
    // During a board transition, the render still contains the previous
    // board's filters. Wait for the board-change effect to hydrate the new
    // state before writing anything under the new key.
    if (boardChanged) return;
    sessionStorage.setItem(`shipyard-board-filters:${id}`, JSON.stringify(filters));
  }, [boardChanged, filters, id]);
  const refresh = () => refreshData();
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const board = data.Board || data;
  const visibleTasks = data.Tasks.filter((task: any) => matchesBoardFilters(task, board.ID, data.Columns, filters, debouncedSearch));
  const activeFilters = [
    ["search", filters.search ? `Suche: ${filters.search}` : ""],
    ["column", data.Columns.find((column: any) => column.ID === filters.column)?.Name || ""],
    ["priority", filters.priority ? `Priorität: ${priorityLabel(filters.priority)}` : ""],
    ["label", data.Labels?.find((label: any) => label.ID === filters.label)?.Name || ""],
    ["project", filters.project ? `Projekt: ${data.Projects?.find((project: any) => project.ID === filters.project)?.Name || filters.project}` : ""],
  ].filter(([, label]) => label) as [keyof BoardFilters, string][];
  const updateFilter = (key: keyof BoardFilters, value: string) => setFilters((current) => ({ ...current, [key]: value }));
  const resetFilters = () => {
    setSearchInput("");
    setDebouncedSearch("");
    setFilters(emptyBoardFilters());
  };
  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    const form = new FormData(e.target as HTMLFormElement);
    try {
      await mutation("/boards/" + id + "/tasks", {
        method: "POST",
        headers: { Accept: "application/json" },
        body: form,
      });
      setOpen(false);
      refresh();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const update = async (e: React.FormEvent) => {
    e.preventDefault();
    const form = new FormData(e.target as HTMLFormElement);
    try {
      await mutation("/boards/" + id, { method: "POST", body: form });
      refresh();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const remove = async () => {
    if (!confirm("Board inklusive Aufgaben wirklich löschen?")) return;
    try {
      await mutation("/boards/" + id + "/delete", { method: "POST" });
      location.hash = "/boards";
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const moveTask = async (taskID: string, targetColumnID: string) => {
    const form = new FormData();
    form.set("target_column_id", targetColumnID);
    try {
      await mutation("/tasks/" + taskID + "/move", { method: "POST", body: form });
      refreshData();
    } catch (err) {
      setMessage(String(err));
    } finally {
      setDraggedTask("");
    }
  };
  return (
    <>
      <h1 className="mb-4 text-2xl font-semibold">{board.Name}</h1>
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <a
          className="text-sm text-muted-foreground hover:text-foreground"
          href="#/boards"
        >
          ← Alle Boards
        </a>
        <div className="flex gap-2">
          <Button asChild variant="outline"><a href={"#/boards/" + id + "/workflow"}>Workflow</a></Button>
          <Button variant="outline" onClick={() => setLabelsOpen(true)}>
            Tags
          </Button>
          <Button variant="outline" onClick={() => setSettings(true)}>
            Board verwalten
          </Button>
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button>Aufgabe anlegen</Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Neue Aufgabe</DialogTitle>
                <DialogDescription>
                  Die Aufgabe startet in der Inbox dieses Boards.
                </DialogDescription>
              </DialogHeader>
              <form className="grid gap-4" onSubmit={create}>
                <label className="grid gap-2 text-sm font-medium">
                  Titel
                  <Input
                    name="title"
                    required
                    autoFocus
                    value={title}
                    onChange={(e) => setTitle(e.target.value)}
                  />
                </label>
                <div className="grid gap-3 sm:grid-cols-3">
                  <label className="grid gap-1 text-sm">
                    Priorität
                    <select
                      name="priority"
                      defaultValue="normal"
                      className="h-9 rounded-md border bg-background px-2"
                    >
                      <option value="low">Niedrig</option>
                      <option value="normal">Normal</option>
                      <option value="high">Hoch</option>
                      <option value="urgent">Dringend</option>
                    </select>
                  </label>
                  <label className="grid gap-1 text-sm">
                    Start
                    <Input name="start_date" type="date" />
                  </label>
                  <label className="grid gap-1 text-sm">
                    Fällig
                    <Input name="due_date" type="date" />
                  </label>
                </div>
                {data.Labels?.length > 0 && (
                  <fieldset className="flex flex-wrap gap-x-3 gap-y-2">
                    <legend className="mb-1 text-sm font-medium">Tags</legend>
                    {data.Labels.map((label: any) => (
                      <label key={label.ID} className="flex items-center gap-1 text-sm">
                        <input name="label_ids" type="checkbox" value={label.ID} />
                        {label.Name}
                      </label>
                    ))}
                  </fieldset>
                )}
                <details className="rounded-lg border p-3">
                  <summary className="cursor-pointer text-sm font-medium">
                    Zielbereiche (optional)
                  </summary>
                  <div className="mt-3 grid gap-3">
                    {data.Projects?.length > 0 && (
                      <fieldset className="grid gap-1">
                        <legend className="text-sm font-medium">Projekte</legend>
                        {data.Projects.map((project: any) => (
                          <label key={project.ID} className="flex items-center gap-2 text-sm">
                            <input name="target_project_ids" type="checkbox" value={project.ID} />
                            {project.Name}
                          </label>
                        ))}
                      </fieldset>
                    )}
                    {data.Groups?.length > 0 && (
                      <fieldset className="grid gap-1">
                        <legend className="text-sm font-medium">Projektgruppen</legend>
                        {data.Groups.map((group: any) => (
                          <label key={group.ID} className="flex items-center gap-2 text-sm">
                            <input name="target_group_ids" type="checkbox" value={group.ID} />
                            {group.Name}
                          </label>
                        ))}
                      </fieldset>
                    )}
                  </div>
                </details>
                <DescriptionEditor initialValue="" />
                <DialogFooter>
                  <Button type="submit">Aufgabe speichern</Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        </div>
      </div>
      {message && <p className="mb-3 text-sm text-destructive">{message}</p>}
      {data.Tasks.length > 0 && <section className="mb-5 rounded-xl border bg-card p-4" aria-label="Board-Filter">
        <div className="flex flex-wrap items-end gap-3">
          <label className="min-w-56 flex-1 text-sm font-medium">
            Aufgaben suchen
            <Input
              className="mt-1"
              type="search"
              role="searchbox"
              aria-label="Aufgaben suchen"
              placeholder="Titel oder Beschreibung"
              value={searchInput}
              onChange={(event) => setSearchInput(event.target.value)}
            />
          </label>
          <label className="grid gap-1 text-sm font-medium">
            Spalte
            <select aria-label="Spalte" value={filters.column} onChange={(event) => updateFilter("column", event.target.value)} className="h-9 rounded-md border bg-background px-2">
              <option value="">Alle Spalten</option>
              {data.Columns.map((column: any) => <option key={column.ID} value={column.ID}>{column.Name}</option>)}
            </select>
          </label>
          <label className="grid gap-1 text-sm font-medium">
            Priorität
            <select aria-label="Priorität" value={filters.priority} onChange={(event) => updateFilter("priority", event.target.value)} className="h-9 rounded-md border bg-background px-2">
              <option value="">Alle Prioritäten</option>
              <option value="urgent">Dringend</option><option value="high">Hoch</option><option value="normal">Normal</option><option value="low">Niedrig</option>
            </select>
          </label>
          <label className="grid gap-1 text-sm font-medium">
            Tag
            <select aria-label="Tag" value={filters.label} onChange={(event) => updateFilter("label", event.target.value)} className="h-9 rounded-md border bg-background px-2">
              <option value="">Alle Tags</option>
              {data.Labels?.map((label: any) => <option key={label.ID} value={label.ID}>{label.Name}</option>)}
            </select>
          </label>
          <label className="grid gap-1 text-sm font-medium">
            Projekt
            <select aria-label="Projekt" value={filters.project} onChange={(event) => updateFilter("project", event.target.value)} className="h-9 rounded-md border bg-background px-2">
              <option value="">Alle Projekte</option>
              {data.Projects?.map((project: any) => <option key={project.ID} value={project.ID}>{project.Name}</option>)}
            </select>
          </label>
          <Button type="button" variant="outline" onClick={resetFilters}>Alle Filter zurücksetzen</Button>
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-2 text-sm" aria-live="polite">
          <strong>{visibleTasks.length} {visibleTasks.length === 1 ? "Aufgabe" : "Aufgaben"} gefunden</strong>
          {activeFilters.map(([key, label]) => <button key={key} type="button" className="rounded-full border px-2 py-1 text-xs hover:border-primary" onClick={() => { updateFilter(key, ""); if (key === "search") setSearchInput(""); }} aria-label={`${label} zurücksetzen`}>{label} ×</button>)}
        </div>
      </section>}
      <div className="flex gap-4 overflow-x-auto pb-4">
        {data.Columns.map((column: any) => (
          <section
            key={column.ID}
            data-board-column={column.ID}
            onDragOver={(event) => event.preventDefault()}
            onDrop={(event) => {
              event.preventDefault();
              const taskID = event.dataTransfer.getData("text/taskboard-task") || draggedTask;
              if (taskID) void moveTask(taskID, column.ID);
            }}
            className="w-72 shrink-0 rounded-xl border bg-muted/40 p-3 transition-colors has-[a.dragging]:ring-2 has-[a.dragging]:ring-primary"
          >
            <div className="mb-3 flex justify-between">
              <strong className="text-sm">{column.Name}</strong>
              <Badge variant="secondary">{visibleTasks.filter((task: any) => task.ColumnID === column.ID).length}</Badge>
            </div>
            <div className="space-y-2">
              {visibleTasks.filter((task: any) => task.ColumnID === column.ID).map((task: any) => (
                <a
                  key={task.ID}
                  href={"#/tasks/" + task.ID}
                  draggable
                  onDragStart={(event) => {
                    event.dataTransfer.effectAllowed = "move";
                    event.dataTransfer.setData("text/taskboard-task", task.ID);
                    setDraggedTask(task.ID);
                  }}
                  onDragEnd={() => setDraggedTask("")}
                  onClick={(event) => {
                    if (!suppressTaskClick.current) return;
                    event.preventDefault();
                  }}
                  onPointerDown={(event) => {
                    if (event.pointerType !== "touch") return;
                    if (!event.isPrimary) {
                      cancelTouchDrag();
                      return;
                    }
                    cancelTouchDrag();
                    const pending = { taskID: task.ID, startX: event.clientX, startY: event.clientY, active: false, timer: 0 };
                    const cardElement = event.currentTarget;
                    pending.timer = window.setTimeout(() => {
                      if (touchDrag.current !== pending) return;
                      pending.active = true;
                      setDraggedTask(task.ID);
                      cardElement.setPointerCapture?.(event.pointerId);
                      if (navigator.vibrate) navigator.vibrate(12);
                    }, TOUCH_LONG_PRESS_DELAY);
                    touchDrag.current = pending;
                  }}
                  onPointerMove={(event) => {
                    const active = touchDrag.current;
                    if (event.pointerType !== "touch" || !active || active.taskID !== task.ID) return;
                    if (!active.active && Math.hypot(event.clientX - active.startX, event.clientY - active.startY) > TOUCH_MOVE_TOLERANCE) {
                      cancelTouchDrag();
                    }
                  }}
                  onPointerUp={(event) => {
                    const active = touchDrag.current;
                    if (active) window.clearTimeout(active.timer);
                    touchDrag.current = undefined;
                    if (event.pointerType !== "touch" || !active?.active) return;
                    event.preventDefault();
                    suppressTaskClick.current = true;
                    window.setTimeout(() => { suppressTaskClick.current = false; }, 0);
                    const target = document.elementFromPoint(event.clientX, event.clientY)?.closest<HTMLElement>("[data-board-column]");
                    const targetColumnID = target?.dataset.boardColumn;
                    if (targetColumnID) void moveTask(task.ID, targetColumnID);
                    else setDraggedTask("");
                  }}
                  onPointerCancel={() => cancelTouchDrag()}
                  data-touch-dragging={draggedTask === task.ID ? "true" : undefined}
                  className={"block rounded-lg border bg-card p-3 text-sm shadow-sm transition hover:border-primary " + (draggedTask === task.ID ? "dragging opacity-60 ring-2 ring-primary" : "")}
                >
                  <strong>{task.Title}</strong>
                  {task.Description && (
                    <p className="mt-2 line-clamp-3 text-xs text-muted-foreground">
                      {task.Description}
                    </p>
                  )}
                  <p className="mt-3 text-xs text-muted-foreground">
                    {task.Priority}
                  </p>
                  {task.Labels?.length > 0 && (
                    <span className="mt-2 flex flex-wrap gap-1">
                      {task.Labels.map((label: any) => (
                        <Badge key={label.ID} variant="outline">{label.Name}</Badge>
                      ))}
                    </span>
                  )}
                </a>
              ))}
              {visibleTasks.filter((task: any) => task.ColumnID === column.ID).length === 0 && <p className="py-4 text-center text-xs text-muted-foreground">Keine passenden Aufgaben</p>}
            </div>
          </section>
        ))}
      </div>
      {visibleTasks.length === 0 && <p className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">Keine Aufgaben entsprechen den aktiven Filtern. Setze die Filter zurück oder suche nach einem anderen Begriff.</p>}
      <Dialog open={settings} onOpenChange={setSettings}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Board verwalten</DialogTitle>
            <DialogDescription>
              Name und Lebenszyklus dieses Boards.
            </DialogDescription>
          </DialogHeader>
          <form className="grid gap-4" onSubmit={update}>
            <label className="grid gap-2 text-sm">
              Name
              <Input name="name" required defaultValue={board.Name} />
            </label>
            <DialogFooter>
              <Button type="submit">Speichern</Button>
              <Button type="button" variant="destructive" onClick={remove}>
                Board löschen
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog open={labelsOpen} onOpenChange={setLabelsOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Board-Tags</DialogTitle>
            <DialogDescription>
              Tags lassen sich Aufgaben, Automationen und Agenten-Workflows zuordnen.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-wrap gap-2">
            {data.Labels?.length ? data.Labels.map((label: any) => (
              <Badge key={label.ID} variant="outline">{label.Name}</Badge>
            )) : <p className="text-sm text-muted-foreground">Noch keine Tags.</p>}
          </div>
          <form
            className="mt-3 grid gap-3"
            onSubmit={async (e) => {
              e.preventDefault();
              try {
                await mutation("/boards/" + id + "/labels", {
                  method: "POST",
                  body: new FormData(e.target as HTMLFormElement),
                });
                refresh();
              } catch (err) {
                setMessage(String(err));
              }
            }}
          >
            <label className="grid gap-1 text-sm">
              Neuer Tag
              <Input name="name" required placeholder="z. B. Frontend" />
            </label>
            <label className="grid gap-1 text-sm">
              Farbe
              <Input name="color" type="color" defaultValue="#176f8a" />
            </label>
            <DialogFooter><Button type="submit">Tag anlegen</Button></DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}


type DiffFile = { path: string; additions: number; deletions: number; lines: string[] };

function parseDiff(raw: string): DiffFile[] {
  const files: DiffFile[] = [];
  let current: DiffFile | undefined;
  for (const line of raw.split("\n")) {
    const header = line.match(/^diff --git a\/(.+) b\/(.+)$/);
    if (header) {
      current = { path: header[2], additions: 0, deletions: 0, lines: [] };
      files.push(current);
      continue;
    }
    if (!current || line.startsWith("--- ") || line.startsWith("+++ ") || line.startsWith("@@ ") || line.startsWith("index ") || line.startsWith("new file") || line.startsWith("old mode") || line.startsWith("new mode")) continue;
    if (line.startsWith("+") && !line.startsWith("+++")) current.additions += 1;
    if (line.startsWith("-") && !line.startsWith("---")) current.deletions += 1;
    current.lines.push(line);
  }
  return files;
}

function changeState(change: any) {
  if (!change) return "Kein Delivery-Run vorhanden.";
  if (["queued", "running"].includes(change.Status)) return "Der Delivery-Run läuft noch. Änderungen können erst nach Abschluss geprüft werden.";
  if (change.Status !== "succeeded") return "Der Delivery-Run ist fehlgeschlagen; seine Änderungen sind nicht übernehmbar.";
  if (change.GateStatus !== "passed") return "Das Qualitäts-Gate ist nicht bestanden; die Änderungen bleiben geschützt.";
  if (!change.DiffSummary) return "Dieser Run enthält keine übernehmbaren Änderungen.";
  if (change.AppliedAt) return "Diese Änderungen wurden bereits übernommen.";
  return "";
}

function DiffReview({ loading, files }: { loading: boolean; files: DiffFile[] }) {
  if (loading) return <Card><CardContent className="py-10 text-center text-sm text-muted-foreground">Diff wird geladen …</CardContent></Card>;
  if (!files.length) return <Card><CardContent className="py-10 text-center text-sm text-muted-foreground">Der Diff ist leer oder nicht mehr verfügbar.</CardContent></Card>;
  return <div className="grid gap-4 lg:grid-cols-[15rem_minmax(0,1fr)]">
    <Card className="h-fit"><CardHeader><CardTitle className="text-base">Dateien</CardTitle></CardHeader><CardContent className="p-2"><nav aria-label="Geänderte Dateien" className="grid gap-1">{files.map((file) => <a key={file.path} href={`#change-${file.path}`} className="rounded-md px-3 py-2 text-left text-sm hover:bg-muted"><span className="block truncate font-medium">{file.path}</span><span className="text-xs text-muted-foreground"><span className="text-emerald-700 dark:text-emerald-400">+{file.additions}</span> <span className="text-red-700 dark:text-red-400">−{file.deletions}</span></span></a>)}</nav></CardContent></Card>
    <div className="min-w-0 space-y-3">{files.map((file, index) => <details key={file.path} id={`change-${file.path}`} open={index === 0} className="overflow-hidden rounded-lg border"><summary className="cursor-pointer list-inside bg-muted px-4 py-3 text-sm font-medium"><span>{file.path}</span><span className="ml-3 text-xs font-normal text-muted-foreground">{file.additions + file.deletions} Änderungen</span></summary><div className="overflow-auto bg-muted/40 font-mono text-xs leading-6">{file.lines.map((line, lineIndex) => <div key={lineIndex} className={`min-w-max px-4 ${line.startsWith("+") ? "bg-emerald-500/15 text-emerald-900 dark:text-emerald-200" : line.startsWith("-") ? "bg-red-500/15 text-red-900 dark:text-red-200" : "text-muted-foreground"}`}><span aria-hidden="true" className="mr-3 inline-block w-3 select-none text-center">{line[0] || " "}</span>{line.slice(1)}</div>)}</div></details>)}</div>
  </div>;
}

function ChangesTab({ changes, onMessage }: { changes: any[]; onMessage: (message: string) => void }) {
  const [rawDiff, setRawDiff] = useState("");
  const [loading, setLoading] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [applyError, setApplyError] = useState("");
  const candidate = changes.find((change) => change.Status === "succeeded" && change.GateStatus === "passed" && change.DiffSummary && !change.AppliedAt);
  const latest = changes[0];
  const selected = candidate || latest;
  const candidateID = candidate?.ID;
  const files = parseDiff(rawDiff);
  const additions = files.reduce((sum, file) => sum + file.additions, 0);
  const deletions = files.reduce((sum, file) => sum + file.deletions, 0);

  useEffect(() => {
    let cancelled = false;
    setRawDiff("");
    if (!candidateID) return;
    setLoading(true);
    fetch(`/runs/${candidateID}/diff`, { credentials: "same-origin" })
      .then((response) => response.ok ? response.text() : Promise.reject(new Error("Diff konnte nicht geladen werden.")))
      .then((value) => { if (!cancelled) setRawDiff(value); })
      .catch((error) => { if (!cancelled) onMessage(error.message); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [candidateID, onMessage]);

  const apply = async () => {
    if (!candidate) return;
    setBusy(true);
    setApplyError("");
    try {
      await mutation(`/runs/${candidate.ID}/apply`, { method: "POST" });
      setConfirmOpen(false);
      refreshData();
    } catch (error) {
      setApplyError(String(error));
      setBusy(false);
    }
  };

  if (!changes.length) return <Card><CardContent className="py-12 text-center"><p className="font-medium">Noch kein Delivery-Run vorhanden.</p><p className="mt-2 text-sm text-muted-foreground">Sobald ein Agent Änderungen erstellt, erscheinen sie hier.</p></CardContent></Card>;
  return <div className="grid gap-4">
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div><CardTitle>Änderungen</CardTitle><CardDescription>{selected ? `Run ${selected.ID.slice(0, 8)} · ${selected.GateStatus === "passed" ? "Gate bestanden" : selected.Status}` : "Kein übernehmbarer Run"}</CardDescription></div>
        <Button disabled={!candidate || loading || !rawDiff} onClick={() => setConfirmOpen(true)}>Änderungen übernehmen</Button>
      </CardHeader>
      <CardContent>
        <p className={`text-sm ${candidate ? "text-muted-foreground" : "text-amber-700 dark:text-amber-300"}`} role="status">{candidate ? "Dieser Diff ist geprüft und kann in das zugewiesene Repository übernommen werden." : changeState(latest)}</p>
        {applyError && <p className="mt-3 text-sm text-destructive" role="alert">Übernahme fehlgeschlagen: {applyError}</p>}
        {candidate && <div className="mt-5 flex flex-wrap gap-3 border-t pt-4 text-sm"><span><strong>{files.length || "–"}</strong> Dateien</span><span className="text-emerald-700 dark:text-emerald-400">+{additions} hinzugefügt</span><span className="text-red-700 dark:text-red-400">−{deletions} gelöscht</span></div>}
      </CardContent>
    </Card>
    {candidate && <DiffReview loading={loading} files={files} />}
    <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}><DialogContent><DialogHeader><DialogTitle>Änderungen übernehmen?</DialogTitle><DialogDescription>Die geprüften Änderungen werden in das zugewiesene Repository integriert. Danach wechselt der Task nach Review.</DialogDescription></DialogHeader><div className="rounded-md bg-muted p-3 text-sm"><p><strong>{files.length}</strong> Dateien · <span className="text-emerald-700 dark:text-emerald-400">+{additions}</span> / <span className="text-red-700 dark:text-red-400">−{deletions}</span></p><ul className="mt-2 max-h-32 list-disc overflow-auto pl-5">{files.map((file) => <li key={file.path}>{file.path}</li>)}</ul></div><DialogFooter><Button variant="outline" onClick={() => setConfirmOpen(false)}>Abbrechen</Button><Button disabled={busy} onClick={apply}>Bestätigen und übernehmen</Button></DialogFooter></DialogContent></Dialog>
  </div>;
}

function TaskDetail({ id }: { id: string }) {
  const { data, error } = useAPI<any>("/api/v1/tasks/" + id);
  const [activeTab, setActiveTab] = useState<"conversation" | "changes">(taskTabFromHash);
  const [comment, setComment] = useState("");
  const [showOlderComments, setShowOlderComments] = useState(false);
  const commentScrollAnchor = useRef<{ index: number; top: number } | null>(null);
  useLayoutEffect(() => {
    const anchor = commentScrollAnchor.current;
    if (!anchor) return;
    const element = document.querySelector<HTMLElement>(
      `[data-testid="task-comment"][data-comment-index="${anchor.index}"]`,
    );
    if (element) {
      window.scrollTo({ top: window.scrollY + element.getBoundingClientRect().top - anchor.top, behavior: "auto" });
    }
    commentScrollAnchor.current = null;
  }, [showOlderComments]);
  const [edit, setEdit] = useState(false);
  const [targets, setTargets] = useState(false);
  const [handoff, setHandoff] = useState(false);
  const [decision, setDecision] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    const updateTab = () => setActiveTab(taskTabFromHash());
    addEventListener("hashchange", updateTab);
    addEventListener("popstate", updateTab);
    return () => {
      removeEventListener("hashchange", updateTab);
      removeEventListener("popstate", updateTab);
    };
  }, []);
  const refresh = () => refreshData();
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const task = data.Task;
  const allComments = data.Comments || [];
  const hasOlderComments = allComments.length > 6;
  const visibleComments = showOlderComments || !hasOlderComments
    ? allComments
    : allComments.slice(-6);
  const latestCommentStart = Math.max(0, allComments.length - 3);
  const toggleOlderComments = () => {
    if (hasOlderComments) {
      const anchorIndex = allComments.length - 6;
      const element = document.querySelector<HTMLElement>(
        `[data-testid="task-comment"][data-comment-index="${anchorIndex}"]`,
      );
      if (element) commentScrollAnchor.current = { index: anchorIndex, top: element.getBoundingClientRect().top };
    }
    setShowOlderComments((visible) => !visible);
  };
  const request = async (url: string, body: FormData) => {
    try {
      await mutation(url, { method: "POST", body });
      refresh();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const commentSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const form = new FormData();
    form.set("body", comment);
    await request("/tasks/" + id + "/comments", form);
    setComment("");
  };
  const move = async (target: string) => {
    const form = new FormData();
    form.set("target_column_id", target);
    await request("/tasks/" + id + "/move", form);
  };
  const answer = async (interaction: any, e: React.FormEvent) => {
    e.preventDefault();
    await request(
      "/interactions/" + interaction.ID + "/answer",
      new FormData(e.target as HTMLFormElement),
    );
  };
  const start = async (agentID: string) => {
    const form = new FormData();
    form.set("agent_id", agentID);
    await request("/tasks/" + id + "/runs", form);
  };
  const remove = async () => {
    if (!confirm("Task wirklich löschen?")) return;
    try {
      await mutation("/tasks/" + id + "/delete", { method: "POST" });
      location.hash = "/boards/" + task.BoardID;
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <>
      <div className={activeTab === "conversation" ? "grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]" : "grid gap-6"}>
        <section className="min-w-0">
          <Tabs
            value={activeTab}
            onValueChange={(value) => {
              const nextTab = value as "conversation" | "changes";
              setActiveTab(nextTab);
              history.pushState(null, "", `#${routeFromHash()}?tab=${nextTab}`);
            }}
            className="mb-6"
          >
            <TabsList variant="line" aria-label="Task-Ansichten">
              <TabsTrigger value="conversation">Conversation</TabsTrigger>
              <TabsTrigger value="changes">Changes</TabsTrigger>
            </TabsList>
          </Tabs>
          <div className="flex justify-between gap-3">
            <a
              className="text-sm text-muted-foreground hover:text-foreground"
              href={"#/boards/" + task.BoardID}
            >
              ← Zum Board
            </a>
            <Button size="sm" variant="outline" onClick={() => setEdit(true)}>
              Bearbeiten
            </Button>
          </div>
          <h2 className="mt-5 text-2xl font-semibold">{task.Title}</h2>
          {task.Description ? <MarkdownContent source={task.Description} className="mt-3 text-sm" /> : <p className="mt-3 text-sm text-muted-foreground">Keine Beschreibung.</p>}
          {message && (
            <p className="mt-3 text-sm text-destructive">{message}</p>
          )}
          <div className={activeTab === "conversation" ? "block" : "hidden"}>
          <Card>
            <CardHeader>
              <CardTitle>Kommentare & Entscheidungen</CardTitle>
            </CardHeader>
            <CardContent>
              {data.Interactions?.map((interaction: any) => (
                <section
                  key={interaction.ID}
                  className="mb-5 rounded-lg border border-amber-500/40 bg-amber-500/5 p-4"
                >
                  <p className="text-xs font-semibold uppercase tracking-wide text-amber-700 dark:text-amber-400">
                    Entscheidung benötigt
                  </p>
                  <h3 className="mt-1 font-medium">{interaction.Title}</h3>
                  {interaction.Body && (
                    <MarkdownContent source={interaction.Body} className="mt-2 text-sm text-muted-foreground" />
                  )}
                  <form
                    className="mt-3 grid gap-3"
                    onSubmit={(e) => answer(interaction, e)}
                  >
                    {interaction.Fields.map((field: any) => (
                      <label key={field.ID} className="grid gap-1 text-sm">
                        {field.Label}
                        {field.Type === "textarea" ? (
                          <textarea
                            name={field.ID}
                            required={field.Required}
                            className="min-h-20 rounded-md border bg-transparent p-2"
                          />
                        ) : field.Type === "select" ? (
                          <select
                            name={field.ID}
                            required={field.Required}
                            className="h-9 rounded-md border bg-background px-2"
                          >
                            <option value="">Bitte wählen …</option>
                            {field.Options.map((option: any) => (
                              <option key={option.Value} value={option.Value}>
                                {option.Label}
                              </option>
                            ))}
                          </select>
                        ) : field.Type === "buttons" ? (
                          <span className="flex flex-wrap gap-2">
                            {field.Options.map((option: any) => (
                              <Button
                                key={option.Value}
                                type="submit"
                                name={field.ID}
                                value={option.Value}
                                size="sm"
                              >
                                {option.Label}
                              </Button>
                            ))}
                          </span>
                        ) : (
                          <Input name={field.ID} required={field.Required} />
                        )}
                      </label>
                    ))}
                    <label className="grid gap-1 text-sm">
                      Eigene oder ergänzende Antwort
                      <textarea
                        name="freeform_answer"
                        className="min-h-20 rounded-md border bg-transparent p-2"
                        placeholder="Überschreibt oder ergänzt die Auswahl."
                      />
                    </label>
                    <label className="grid gap-1 text-sm">
                      Danach weitergeben
                      <select
                        name="next_column_id"
                        className="h-9 rounded-md border bg-background px-2"
                      >
                        <option value="">In Blocked bleiben</option>
                        {data.Allowed.map((transition: any) => (
                          <option
                            key={transition.ID}
                            value={transition.ToColumnID}
                          >
                            {transition.ActionName ||
                              data.Columns[transition.ToColumnID]}
                          </option>
                        ))}
                      </select>
                    </label>
                    <Button className="w-fit" type="submit">
                      Antwort speichern
                    </Button>
                  </form>
                </section>
              ))}
              <div
                id="task-comments-list"
                className="space-y-3"
                aria-label="Task-Kommentare"
                aria-live="polite"
              >
                {visibleComments.map((entry: any, visibleIndex: number) => {
                  const automated = /agent|taskboard|system|codex|qa/i.test(entry.Author || "");
                  const originalIndex = showOlderComments || !hasOlderComments
                    ? visibleIndex
                    : allComments.length - visibleComments.length + visibleIndex;
                  const prominent = originalIndex >= latestCommentStart;
                  const body = String(entry.Body || "");
                      // The latest three comments remain fully readable; older
                      // long comments can stay compact and expand on demand.
                      const canExpand = !prominent && body.length > 280;
                  return (
                    <ChatBubble
                      key={entry.ID}
                      data-testid="task-comment"
                      data-comment-index={originalIndex}
                      data-prominent={prominent}
                      className={prominent ? "task-comment--prominent" : "task-comment--compact"}
                      author={entry.Author || "Unbekannt"}
                      timestamp={entry.CreatedAt ? new Date(entry.CreatedAt).toLocaleString("de-DE") : undefined}
                      side={automated ? "incoming" : "outgoing"}
                    >
                      {entry.Status && <span className="mb-1 block text-xs font-medium opacity-75">{entry.Status}</span>}
                      {canExpand ? (
                        <details>
                          <summary className="cursor-pointer whitespace-pre-wrap leading-6 marker:text-current/60">
                            {body.slice(0, 280).trimEnd()} …
                          </summary>
                          <MarkdownContent source={body} className="mt-2 text-sm" />
                        </details>
                      ) : (
                        <MarkdownContent source={body} className="text-sm" />
                      )}
                    </ChatBubble>
                  );
                })}
              </div>
              {hasOlderComments && (
                <Button
                  type="button"
                  variant="outline"
                  className="mt-4"
                  aria-expanded={showOlderComments}
                  aria-controls="task-comments-list"
                  onClick={toggleOlderComments}
                >
                  {showOlderComments ? "Ältere Kommentare ausblenden" : "Ältere Kommentare anzeigen"}
                </Button>
              )}
              <form className="mt-5 grid gap-3" onSubmit={commentSubmit}>
                <textarea
                  required
                  className="min-h-24 rounded-lg border bg-transparent p-2 text-sm"
                  placeholder="Kommentar hinzufügen"
                  value={comment}
                  onChange={(e) => setComment(e.target.value)}
                />
                <Button className="justify-self-start" type="submit">
                  Kommentieren
                </Button>
              </form>
            </CardContent>
          </Card>
          </div>
          <div className={activeTab === "changes" ? "block" : "hidden"}>
              <ChangesTab changes={data.Changes || []} onMessage={setMessage} />
          </div>
          <div className={activeTab === "conversation" ? "block" : "hidden"}>
          <Card className="mt-4">
            <CardHeader>
              <CardTitle>Verlauf</CardTitle>
              <CardDescription>Nachvollziehbare Wechsel im Workflow.</CardDescription>
            </CardHeader>
            <CardContent className="divide-y">
              {data.History?.length ? data.History.map((entry: any, index: number) => (
                <div key={`${entry.OccurredAt}-${index}`} className="py-3 first:pt-0">
                  <p className="text-sm"><strong>{entry.FromName}</strong> → <strong>{entry.ToName}</strong></p>
                  <p className="mt-1 text-xs text-muted-foreground">{entry.Source} · {new Date(entry.OccurredAt).toLocaleString("de-DE")}</p>
                </div>
              )) : <p className="py-4 text-sm text-muted-foreground">Noch keine Workflow-Wechsel.</p>}
            </CardContent>
          </Card>
          </div>
        </section>
        {activeTab === "conversation" && <aside className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>Nächster Schritt</CardTitle>
              <CardDescription>
                Nur erlaubte Workflow-Übergänge.
              </CardDescription>
            </CardHeader>
            <CardContent className="grid gap-2">
              {data.Allowed.map((transition: any) => (
                <Button
                  key={transition.ID}
                  variant="outline"
                  onClick={() => move(transition.ToColumnID)}
                >
                  {transition.ActionName || data.Columns[transition.ToColumnID]}
                </Button>
              ))}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Koordination</CardTitle>
              <CardDescription>Kontext an einen Agenten übergeben oder eine menschliche Entscheidung einholen.</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-2">
              <Button size="sm" variant="outline" onClick={() => setHandoff(true)}>
                An Agent übergeben
              </Button>
              <Button size="sm" variant="outline" onClick={() => setDecision(true)}>
                Entscheidung anfordern
              </Button>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Details</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2 text-sm">
              <p>
                <span className="text-muted-foreground">Priorität: </span>
                {task.Priority}
              </p>
              <p>
                <span className="text-muted-foreground">Start: </span>
                {task.StartDate
                  ? new Date(task.StartDate).toLocaleDateString("de-DE")
                  : "–"}
              </p>
              <p>
                <span className="text-muted-foreground">Fällig: </span>
                {task.DueDate
                  ? new Date(task.DueDate).toLocaleDateString("de-DE")
                  : "–"}
              </p>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Zielbereiche</CardTitle>
            </CardHeader>
            <CardContent>
              <Button
                size="sm"
                variant="outline"
                onClick={() => setTargets(true)}
              >
                Ziele bearbeiten
              </Button>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Agent starten</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-2">
              {data.Agents.filter((agent: any) => agent.Enabled).map(
                (agent: any) => (
                  <Button
                    key={agent.ID}
                    variant="outline"
                    onClick={() => start(agent.ID)}
                  >
                    {agent.Name}
                  </Button>
                ),
              )}
              {data.Runs.map((run: any) => (
                <a
                  key={run.ID}
                  className="text-sm text-primary hover:underline"
                  href={"#/runs/" + run.ID}
                >
                  {run.Status} · Run öffnen
                </a>
              ))}
            </CardContent>
          </Card>
        </aside>}
      </div>
      <Dialog open={edit} onOpenChange={setEdit}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Aufgabe bearbeiten</DialogTitle>
          </DialogHeader>
          <form
            className="grid gap-3"
            onSubmit={async (e) => {
              e.preventDefault();
              await request(
                "/tasks/" + id,
                new FormData(e.target as HTMLFormElement),
              );
              setEdit(false);
            }}
          >
            <label className="grid gap-1 text-sm">
              Titel
              <Input name="title" required defaultValue={task.Title} />
            </label>
            <DescriptionEditor initialValue={task.Description || ""} />
            <label className="grid gap-1 text-sm">
              Priorität
              <select
                name="priority"
                defaultValue={task.Priority}
                className="h-9 rounded-md border bg-background px-2"
              >
                <option value="low">Niedrig</option>
                <option value="normal">Normal</option>
                <option value="high">Hoch</option>
                <option value="urgent">Dringend</option>
              </select>
            </label>
            <label className="grid gap-1 text-sm">
              Startdatum
              <Input
                type="date"
                name="start_date"
                defaultValue={task.StartDate?.slice(0, 10)}
              />
            </label>
            <label className="grid gap-1 text-sm">
              Enddatum
              <Input
                type="date"
                name="due_date"
                defaultValue={task.DueDate?.slice(0, 10)}
              />
            </label>
            <fieldset className="flex flex-wrap gap-2">
              <legend className="mb-2 text-sm">Tags</legend>
              {data.BoardLabels.map((label: any) => (
                <label
                  key={label.ID}
                  className="flex items-center gap-1 text-sm"
                >
                  <input
                    type="checkbox"
                    name="label_ids"
                    value={label.ID}
                    defaultChecked={task.Labels?.some(
                      (value: any) => value.ID === label.ID,
                    )}
                  />
                  {label.Name}
                </label>
              ))}
            </fieldset>
            <DialogFooter>
              <Button type="submit">Speichern</Button>
              <Button type="button" variant="destructive" onClick={remove}>
                Löschen
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog open={targets} onOpenChange={setTargets}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Zielbereiche</DialogTitle>
            <DialogDescription>
              Gruppen werden beim Run-Start in ihre Repositories aufgelöst.
            </DialogDescription>
          </DialogHeader>
          <form
            className="grid gap-3"
            onSubmit={async (e) => {
              e.preventDefault();
              await request(
                "/tasks/" + id + "/targets",
                new FormData(e.target as HTMLFormElement),
              );
              setTargets(false);
            }}
          >
            <fieldset className="grid gap-1">
              <legend className="text-sm font-medium">Projekte</legend>
              {data.Projects.map((project: any) => (
                <label key={project.ID} className="flex gap-2 text-sm">
                  <input
                    type="checkbox"
                    name="target_project_ids"
                    value={project.ID}
                    defaultChecked={data.TargetProjects.some(
                      (value: any) => value.ID === project.ID,
                    )}
                  />
                  {project.Name}
                </label>
              ))}
            </fieldset>
            <fieldset className="grid gap-1">
              <legend className="text-sm font-medium">Gruppen</legend>
              {data.Groups.map((group: any) => (
                <label key={group.ID} className="flex gap-2 text-sm">
                  <input
                    type="checkbox"
                    name="target_group_ids"
                    value={group.ID}
                    defaultChecked={data.TargetGroups.some(
                      (value: any) => value.ID === group.ID,
                    )}
                  />
                  {group.Name}
                </label>
              ))}
            </fieldset>
            <DialogFooter>
              <Button type="submit">Ziele speichern</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog open={handoff} onOpenChange={setHandoff}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Task an Agent übergeben</DialogTitle>
            <DialogDescription>Der Hinweis wird als Kommentar gespeichert und Teil des nächsten Agent-Kontexts.</DialogDescription>
          </DialogHeader>
          <form
            className="grid gap-3"
            onSubmit={async (e) => {
              e.preventDefault();
              await request("/tasks/" + id + "/handoff", new FormData(e.target as HTMLFormElement));
              setHandoff(false);
            }}
          >
            <label className="grid gap-1 text-sm">Agent
              <select required name="agent_id" className="h-9 rounded-md border bg-background px-2">
                <option value="">Agent wählen</option>
                {data.Agents.filter((agent: any) => agent.Enabled).map((agent: any) => (
                  <option key={agent.ID} value={agent.ID}>{agent.Name}</option>
                ))}
              </select>
            </label>
            <label className="grid gap-1 text-sm">Übergabehinweis
              <textarea required name="note" className="min-h-28 rounded-md border bg-transparent p-2" placeholder="Was ist geprüft, was ist der nächste Schritt?" />
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" name="start" value="true" defaultChecked />
              Folge-Run sofort starten
            </label>
            <DialogFooter><Button type="submit">Übergabe speichern</Button></DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog open={decision} onOpenChange={setDecision}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Entscheidung anfordern</DialogTitle>
            <DialogDescription>Der Task wird nach „Needs action“ verschoben und auf dem Dashboard hervorgehoben.</DialogDescription>
          </DialogHeader>
          <form
            className="grid gap-3"
            onSubmit={async (e) => {
              e.preventDefault();
              await request("/tasks/" + id + "/needs-decision", new FormData(e.target as HTMLFormElement));
              setDecision(false);
            }}
          >
            <label className="grid gap-1 text-sm">Benötigte Entscheidung
              <textarea required name="question" className="min-h-28 rounded-md border bg-transparent p-2" placeholder="Welche Entscheidung oder Information wird benötigt?" />
            </label>
            <DialogFooter><Button type="submit">Entscheidung anfordern</Button></DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}

function DescriptionEditor({ initialValue }: { initialValue: string }) {
  const [value, setValue] = useState(initialValue);
  const [preview, setPreview] = useState(false);
  return <div className="grid gap-2">
    <div className="flex items-center justify-between gap-3">
      <label htmlFor="task-description" className="text-sm">Beschreibung</label>
      <div className="flex gap-1" role="tablist" aria-label="Beschreibung bearbeiten">
        <Button type="button" size="sm" variant={preview ? "ghost" : "secondary"} onClick={() => setPreview(false)} role="tab" aria-selected={!preview}>Markdown</Button>
        <Button type="button" size="sm" variant={preview ? "secondary" : "ghost"} onClick={() => setPreview(true)} role="tab" aria-selected={preview}>Vorschau</Button>
      </div>
    </div>
    {preview ? <div className="min-h-24 rounded-md border bg-muted/30 p-3" role="tabpanel" aria-label="Markdown-Vorschau"><MarkdownContent source={value} /></div> : <textarea id="task-description" name="description" className="min-h-32 rounded-md border bg-transparent p-2" value={value} onChange={(event) => setValue(event.target.value)} aria-label="Beschreibung als Markdown" />}
    {preview && <textarea className="sr-only" tabIndex={-1} aria-hidden="true" name="description" value={value} readOnly />}
  </div>;
}

function Automations() {
  const { data: rules, error } = useAPI<any[]>("/api/v1/automations");
  const { data: boards } = useAPI<any[]>("/api/v1/boards");
  const { data: agents } = useAPI<any[]>("/api/v1/agents");
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<any>();
  const [boardID, setBoardID] = useState("");
  const [columns, setColumns] = useState<any[]>([]);
  const [labels, setLabels] = useState<any[]>([]);
  const [message, setMessage] = useState("");
  const [targetColumnID, setTargetColumnID] = useState("");
  const [preview, setPreview] = useState<any>();
  useEffect(() => {
    if (!boardID) return;
    fetch("/api/v1/boards/" + boardID, { credentials: "same-origin" })
      .then((r) => r.json())
      .then((page) => {
        setColumns(page.Columns || []);
        setLabels(page.Labels || []);
      })
      .catch(() => {
        setColumns([]);
        setLabels([]);
      });
  }, [boardID]);
  if (error) return <Failure />;
  if (!rules || !boards || !agents) return <Loading />;
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    const form = new FormData(e.target as HTMLFormElement);
    try {
      await mutation(editing ? "/automations/" + editing.ID : "/automations", {
        method: "POST",
        body: form,
      });
      setOpen(false);
      setEditing(undefined);
      setBoardID("");
      setTargetColumnID("");
      setPreview(undefined);
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const enabled = async (rule: any) => {
    const form = new FormData();
    form.set("enabled", String(!rule.Enabled));
    try {
      await mutation("/automations/" + rule.ID + "/enabled", {
        method: "POST",
        body: form,
      });
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const remove = async (id: string) => {
    if (!confirm("Automation wirklich löschen?")) return;
    try {
      await mutation("/automations/" + id + "/delete", { method: "POST" });
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const edit = (rule: any) => {
    setEditing(rule);
    setBoardID(rule.BoardID || "");
    setTargetColumnID(rule.TargetColumnID || "");
    setPreview(undefined);
    setOpen(true);
  };
  const previewTasks = async () => {
    if (!boardID) {
      setMessage("Wähle zuerst ein Board aus.");
      return;
    }
    try {
      const response = await fetch(
        "/automations/preview?board_id=" + encodeURIComponent(boardID) + "&target_column_id=" + encodeURIComponent(targetColumnID),
        { credentials: "same-origin" },
      );
      if (!response.ok) throw new Error(await response.text());
      setPreview(await response.json());
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <>
      <AutomationTabs active="rules" />
      <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div>
          <CardTitle>Automationen</CardTitle>
          <CardDescription>
            Starte Agents durch Ereignisse im Task-Lebenszyklus.
          </CardDescription>
        </div>
        <Dialog
          open={open}
          onOpenChange={(value) => {
            setOpen(value);
            if (!value) {
              setEditing(undefined);
              setBoardID("");
              setTargetColumnID("");
              setPreview(undefined);
            }
          }}
        >
          <DialogTrigger asChild>
            <Button
              onClick={() => {
                setEditing(undefined);
                setBoardID("");
                setTargetColumnID("");
                setPreview(undefined);
              }}
            >
              Regel anlegen
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                {editing ? "Automation bearbeiten" : "Neue Automation"}
              </DialogTitle>
              <DialogDescription>
                Sie startet den ausgewählten Agenten, sobald ein Task das
                Ereignis auslöst.
              </DialogDescription>
            </DialogHeader>
            <form
              key={editing?.ID || "new"}
              className="grid gap-3"
              onSubmit={save}
            >
              <label className="grid gap-1 text-sm">
                Name
                <Input name="name" required defaultValue={editing?.Name || ""} />
              </label>
              <label className="grid gap-1 text-sm">
                Board
                <select
                  required
                  name="board_id"
                  className="h-9 rounded-md border bg-background px-3"
                  value={boardID}
                  onChange={(e) => setBoardID(e.target.value)}
                >
                  <option value="">Board wählen</option>
                  {boards.map((board) => (
                    <option key={board.ID} value={board.ID}>
                      {board.Name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="grid gap-1 text-sm">
                Auslöser
                <select
                  name="trigger_type"
                  defaultValue={editing?.TriggerType || "task.entered_column"}
                  className="h-9 rounded-md border bg-background px-3"
                >
                  <option value="task.entered_column">
                    Task in Spalte verschoben
                  </option>
                  <option value="task.created">Task angelegt</option>
                  <option value="task.due_soon">Fälligkeit nähert sich</option>
                </select>
              </label>
              <label className="grid gap-1 text-sm">
                Zielspalte
                <select
                  name="target_column_id"
                  value={targetColumnID}
                  onChange={(e) => setTargetColumnID(e.target.value)}
                  className="h-9 rounded-md border bg-background px-3"
                >
                  <option value="">Beliebige Spalte</option>
                  {columns.map((column) => (
                    <option key={column.ID} value={column.ID}>
                      {column.Name}
                    </option>
                  ))}
                </select>
              </label>
              <div className="rounded-lg border bg-muted/30 p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <p className="text-sm font-medium">Vorschau</p>
                  <Button type="button" size="sm" variant="outline" onClick={previewTasks}>
                    Passende Tasks prüfen
                  </Button>
                </div>
                {preview && (
                  <div className="mt-3 text-sm text-muted-foreground">
                    <p>{preview.tasks?.length || 0}{preview.limited ? "+" : ""} offene Task(s) würden aktuell passen.</p>
                    {preview.tasks?.length > 0 && (
                      <ul className="mt-2 list-disc space-y-1 pl-5">
                        {preview.tasks.map((task: any) => <li key={task.ID}>{task.ColumnName}: {task.Title}</li>)}
                      </ul>
                    )}
                  </div>
                )}
              </div>
              <label className="grid gap-1 text-sm">
                Agent
                <select
                  required
                  name="agent_id"
                  defaultValue={editing?.AgentID || ""}
                  className="h-9 rounded-md border bg-background px-3"
                >
                  <option value="">Agent wählen</option>
                  {agents
                    .filter((agent) => agent.Enabled)
                    .map((agent) => (
                      <option key={agent.ID} value={agent.ID}>
                        {agent.Name}
                      </option>
                    ))}
                </select>
              </label>
              <label className="grid gap-1 text-sm">
                Bei Erfolg verschieben
                <select
                  name="success_column_id"
                  defaultValue={editing?.SuccessColumnID || ""}
                  className="h-9 rounded-md border bg-background px-3"
                >
                  <option value="">Nicht verschieben</option>
                  {columns.map((column) => (
                    <option key={column.ID} value={column.ID}>
                      {column.Name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="grid gap-1 text-sm">
                Bei Fehler verschieben
                <select
                  name="failure_column_id"
                  defaultValue={editing?.FailureColumnID || ""}
                  className="h-9 rounded-md border bg-background px-3"
                >
                  <option value="">Nicht verschieben</option>
                  {columns.map((column) => (
                    <option key={column.ID} value={column.ID}>
                      {column.Name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="grid gap-1 text-sm">
                Erforderlicher Tag
                <select
                  name="label_id"
                  defaultValue={editing?.LabelID || ""}
                  className="h-9 rounded-md border bg-background px-3"
                >
                  <option value="">Jeder Tag</option>
                  {labels.map((label) => (
                    <option key={label.ID} value={label.ID}>
                      {label.Name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="grid gap-1 text-sm">
                Übergabe bei Erfolg
                <select
                  name="require_delivery_approval"
                  defaultValue={
                    editing ? String(editing.RequireDeliveryApproval) : "true"
                  }
                  className="h-9 rounded-md border bg-background px-3"
                >
                  <option value="true">Erst nach Diff-Übernahme</option>
                  <option value="false">Sofort weitergeben</option>
                </select>
              </label>
              <div className="grid grid-cols-3 gap-3">
                <label className="grid gap-1 text-sm">
                  Intervall (min)
                  <Input
                    name="schedule_every_minutes"
                    type="number"
                    min="1"
                    defaultValue={editing?.ScheduleEveryMinutes || ""}
                  />
                </label>
                <label className="grid gap-1 text-sm">
                  Fällig in (h)
                  <Input
                    name="due_within_hours"
                    type="number"
                    min="1"
                    defaultValue={editing?.DueWithinHours || ""}
                  />
                </label>
                <label className="grid gap-1 text-sm">
                  Cooldown (min)
                  <Input
                    name="cooldown_minutes"
                    type="number"
                    min="0"
                    defaultValue={editing?.CooldownMinutes || 0}
                  />
                </label>
              </div>
              <DialogFooter>
                <Button type="submit">Regel speichern</Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        {message && <p className="mb-4 text-sm text-destructive">{message}</p>}
        <div className="divide-y">
          {rules.length ? (
            rules.map((rule) => (
              <article key={rule.ID} className="py-4 first:pt-0">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <strong className="text-sm">{rule.Name}</strong>
                    <p className="mt-1 text-sm text-muted-foreground">
                      {rule.TriggerType} · Agent{" "}
                      {agents.find((agent) => agent.ID === rule.AgentID)
                        ?.Name || "nicht verfügbar"}
                    </p>
                  </div>
                  <Badge variant={rule.Enabled ? "secondary" : "outline"}>
                    {rule.Enabled ? "Aktiv" : "Pausiert"}
                  </Badge>
                </div>
                <div className="mt-3 flex gap-2">
                  <Button size="sm" variant="outline" onClick={() => edit(rule)}>
                    Bearbeiten
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => enabled(rule)}
                  >
                    {rule.Enabled ? "Pausieren" : "Aktivieren"}
                  </Button>
                  <Button
                    size="sm"
                    variant="destructive"
                    onClick={() => remove(rule.ID)}
                  >
                    Löschen
                  </Button>
                </div>
              </article>
            ))
          ) : (
            <p className="py-12 text-center text-sm text-muted-foreground">
              Noch keine Regeln vorhanden.
            </p>
          )}
        </div>
      </CardContent>
      </Card>
    </>
  );
}
function Schedules() {
  const { data, error } = useAPI<any[]>("/api/v1/schedules");
  const { data: boards } = useAPI<any[]>("/api/v1/boards");
  const { data: agents } = useAPI<any[]>("/api/v1/agents");
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  if (error) return <Failure />;
  if (!data || !boards || !agents) return <Loading />;
  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await mutation("/schedules", {
        method: "POST",
        body: new FormData(e.target as HTMLFormElement),
      });
      setOpen(false);
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const remove = async (id: string) => {
    if (!confirm("Zeitregel wirklich löschen?")) return;
    try {
      await mutation("/schedules/" + id + "/delete", { method: "POST" });
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <>
      <AutomationTabs active="schedules" />
      <Card>
        <CardHeader className="flex flex-row justify-between gap-3">
          <div>
            <CardTitle>Zeitregeln</CardTitle>
            <CardDescription>
              Prüfe fällige Aufgaben in einem verlässlichen Intervall.
            </CardDescription>
          </div>
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button>Zeitregel anlegen</Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Zeitregel anlegen</DialogTitle>
              </DialogHeader>
              <form className="grid gap-3" onSubmit={create}>
                <label className="grid gap-1 text-sm">
                  Name
                  <Input name="name" required />
                </label>
                <label className="grid gap-1 text-sm">
                  Board
                  <select
                    name="board_id"
                    className="h-9 rounded-md border bg-background px-2"
                  >
                    <option value="">Alle Boards</option>
                    {boards.map((board) => (
                      <option key={board.ID} value={board.ID}>
                        {board.Name}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="grid gap-1 text-sm">
                  Fällig innerhalb Stunden
                  <Input
                    name="within"
                    type="number"
                    min="1"
                    defaultValue="24"
                  />
                </label>
                <label className="grid gap-1 text-sm">
                  Prüfintervall Minuten
                  <Input name="every" type="number" min="1" defaultValue="60" />
                </label>
                <label className="grid gap-1 text-sm">
                  Agent
                  <select
                    name="agent_id"
                    required
                    className="h-9 rounded-md border bg-background px-2"
                  >
                    {agents
                      .filter((agent) => agent.Enabled)
                      .map((agent) => (
                        <option key={agent.ID} value={agent.ID}>
                          {agent.Name}
                        </option>
                      ))}
                  </select>
                </label>
                <DialogFooter>
                  <Button type="submit">Zeitregel speichern</Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        </CardHeader>
        <CardContent className="divide-y">
          {message && (
            <p className="mb-3 text-sm text-destructive">{message}</p>
          )}
          {data.length ? (
            data.map((rule) => (
              <article
                key={rule.ID}
                className="flex justify-between gap-4 py-4 first:pt-0"
              >
                <div>
                  <strong className="text-sm">{rule.Name}</strong>
                  <p className="mt-1 text-sm text-muted-foreground">
                    Alle {rule.EveryMinutes} Minuten · fällig in{" "}
                    {rule.DueWithinHours} Stunden
                  </p>
                </div>
                <Button
                  size="sm"
                  variant="destructive"
                  onClick={() => remove(rule.ID)}
                >
                  Löschen
                </Button>
              </article>
            ))
          ) : (
            <p className="py-12 text-center text-sm text-muted-foreground">
              Noch keine Zeitregeln.
            </p>
          )}
        </CardContent>
      </Card>
    </>
  );
}
function Webhooks() {
  const { data, error } = useAPI<any[]>("/api/v1/webhooks");
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await mutation("/webhooks", {
        method: "POST",
        body: new FormData(e.target as HTMLFormElement),
      });
      setOpen(false);
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const enabled = async (hook: any) => {
    const form = new FormData();
    form.set("enabled", String(!hook.Enabled));
    try {
      await mutation("/webhooks/" + hook.ID + "/enabled", {
        method: "POST",
        body: form,
      });
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const remove = async (id: string) => {
    if (!confirm("Webhook wirklich löschen?")) return;
    try {
      await mutation("/webhooks/" + id + "/delete", { method: "POST" });
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <>
      <AutomationTabs active="webhooks" />
      <Card>
        <CardHeader className="flex flex-row justify-between gap-3">
          <div>
            <CardTitle>Webhooks</CardTitle>
            <CardDescription>
              Informiere externe Systeme über Agent-Run-Ergebnisse.
            </CardDescription>
          </div>
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button>Webhook hinzufügen</Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Webhook hinzufügen</DialogTitle>
              </DialogHeader>
              <form className="grid gap-3" onSubmit={create}>
                <label className="grid gap-1 text-sm">
                  Name
                  <Input name="name" required />
                </label>
                <label className="grid gap-1 text-sm">
                  Ziel-URL
                  <Input name="url" type="url" required />
                </label>
                <label className="grid gap-1 text-sm">
                  Ereignisse
                  <Input
                    name="events"
                    defaultValue="run.succeeded,run.failed,run.cancelled"
                  />
                </label>
                <DialogFooter>
                  <Button type="submit">Webhook speichern</Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        </CardHeader>
        <CardContent className="divide-y">
          {message && (
            <p className="mb-3 text-sm text-destructive">{message}</p>
          )}
          {data.length ? (
            data.map((hook) => (
              <article
                key={hook.ID}
                className="flex justify-between gap-4 py-4 first:pt-0"
              >
                <div>
                  <strong className="text-sm">{hook.Name}</strong>
                  <p className="mt-1 text-sm text-muted-foreground">
                    {hook.URL} · {hook.Events}
                  </p>
                </div>
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => enabled(hook)}
                  >
                    {hook.Enabled ? "Pausieren" : "Aktivieren"}
                  </Button>
                  <Button
                    size="sm"
                    variant="destructive"
                    onClick={() => remove(hook.ID)}
                  >
                    Löschen
                  </Button>
                </div>
              </article>
            ))
          ) : (
            <p className="py-12 text-center text-sm text-muted-foreground">
              Noch keine Webhooks.
            </p>
          )}
        </CardContent>
      </Card>
    </>
  );
}
function AutomationTabs({
  active,
}: {
  active: "rules" | "schedules" | "webhooks";
}) {
  return (
    <div className="mb-5 flex flex-wrap gap-2 border-b pb-4">
      <Button asChild size="sm" variant={active === "rules" ? "default" : "ghost"}>
        <a href="#/automations">Ereignisregeln</a>
      </Button>
      <Button asChild size="sm" variant={active === "schedules" ? "default" : "ghost"}>
        <a href="#/automations/schedules">Zeitregeln</a>
      </Button>
      <Button asChild size="sm" variant={active === "webhooks" ? "default" : "ghost"}>
        <a href="#/automations/webhooks">Webhooks</a>
      </Button>
    </div>
  );
}
function Runs() {
  const { data, error } = useAPI<any>("/api/v1/runs");
  const [older, setOlder] = useState<any[]>([]);
  const [next, setNext] = useState<string | undefined>();
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const items = [...(data.items || []), ...older];
  const cursor = next ?? data.next;
  const loadOlder = async () => {
    if (!cursor) return;
    const response = await fetch("/api/v1/runs?before=" + encodeURIComponent(cursor), { credentials: "same-origin" });
    if (!response.ok) return;
    const page = await response.json();
    setOlder((entries) => [...entries, ...(page.items || [])]);
    setNext(page.next || "");
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle>Runs</CardTitle>
        <CardDescription>Agentenläufe, Status und Ergebnis.</CardDescription>
      </CardHeader>
      <CardContent className="divide-y">
        {items.length ? items.map((run: any) => (
          <a
            key={run.ID}
            href={"#/runs/" + run.ID}
            className="block py-4 first:pt-0 hover:bg-muted/40"
          >
            <div className="flex justify-between gap-4">
              <strong className="text-sm">{run.TaskTitle || "Aufgabe"}</strong>
              <Badge
                variant={run.Status === "failed" ? "destructive" : "secondary"}
              >
                {run.Status}
              </Badge>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              {run.AgentName || "Agent"} ·{" "}
              {run.Summary || run.ErrorMessage || "Kein Ergebnistext"}
            </p>
            <dl className="mt-3 grid grid-cols-3 gap-2 text-xs text-muted-foreground sm:max-w-xl">
              <div>
                <dt>Gestartet</dt>
                <dd className="mt-0.5 text-foreground">{formatDateTime(run.StartedAt || run.CreatedAt)}</dd>
              </div>
              <div>
                <dt>Beendet</dt>
                <dd className="mt-0.5 text-foreground">{formatDateTime(run.FinishedAt)}</dd>
              </div>
              <div>
                <dt>Laufzeit</dt>
                <dd className="mt-0.5 text-foreground">{formatDuration(run.DurationSeconds, run.Status)}</dd>
              </div>
            </dl>
          </a>
        )) : <EmptyState title="Noch keine Runs" description="Starte einen Agenten an einer Aufgabe. Hier erscheinen Laufzeit, Ergebnis und das vollständige Protokoll." />}
        {cursor && (
          <Button className="mt-4" size="sm" variant="outline" onClick={loadOlder}>
            Ältere Runs laden
          </Button>
        )}
      </CardContent>
    </Card>
  );
}
function formatDateTime(value?: string) {
  if (!value) return "–";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "–" : date.toLocaleString("de-DE");
}
function formatDuration(seconds: unknown, status?: string) {
  const value = Number(seconds);
  if (!Number.isFinite(value) || value < 0) return status === "running" ? "läuft …" : "–";
  if (value < 60) return `${Math.round(value)} s`;
  return `${Math.floor(value / 60)} min ${Math.round(value % 60)} s`;
}
function Skills() {
  const { data: installed, error } = useAPI<any[]>("/api/v1/skills");
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<any[]>([]);
  const [message, setMessage] = useState("");
  const search = async (e: React.FormEvent) => {
    e.preventDefault();
    setMessage("");
    const r = await fetch(
      "/api/v1/skills/search?q=" + encodeURIComponent(query),
      { credentials: "same-origin" },
    );
    setResults(r.ok ? await r.json() : []);
  };
  const install = async (skill: any) => {
    setMessage("Installiere " + skill.Name + " …");
    try {
      await mutation("/api/v1/skills/install", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ Source: skill.Source, Slug: skill.Slug }),
      });
      setMessage("Skill installiert.");
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const uninstall = async (id: string) => {
    if (!confirm("Skill wirklich deinstallieren?")) return;
    try {
      await mutation("/skills/" + id + "/uninstall", { method: "POST" });
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  if (error) return <Failure />;
  if (!installed) return <Loading />;
  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
      <Card>
        <CardHeader>
          <CardTitle>Skills entdecken</CardTitle>
          <CardDescription>
            Durchsuche den globalen skills.sh-Katalog.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="flex gap-2" onSubmit={search}>
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="z. B. postgres, testing, frontend"
            />
            <Button type="submit">Suchen</Button>
          </form>
          {message && (
            <p className="mt-3 text-sm text-muted-foreground">{message}</p>
          )}
          <div className="mt-5 divide-y">
            {results.map((skill) => (
              <article
                key={skill.Source + "/" + skill.Slug}
                className="flex justify-between gap-4 py-4"
              >
                <div>
                  <strong className="text-sm">{skill.Name}</strong>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {skill.Source} · {skill.Installs} Installationen
                  </p>
                </div>
                <Button size="sm" onClick={() => install(skill)}>
                  Installieren
                </Button>
              </article>
            ))}
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Installiert</CardTitle>
          <CardDescription>Für Agents verfügbar.</CardDescription>
        </CardHeader>
        <CardContent className="divide-y">
          {installed.length ? installed.map((skill) => (
            <article
              key={skill.ID}
              className="flex justify-between gap-2 py-3 first:pt-0"
            >
              <div>
                <strong className="text-sm">{skill.Name}</strong>
                <p className="mt-1 text-xs text-muted-foreground">
                  {skill.Description}
                </p>
              </div>
              <Button
                variant="destructive"
                size="sm"
                onClick={() => uninstall(skill.ID)}
              >
                Entfernen
              </Button>
            </article>
          )) : <EmptyState title="Noch keine Skills installiert" description="Suche links im skills.sh-Katalog und installiere nur die Fähigkeiten, die deine Agents verwenden dürfen." />}
        </CardContent>
      </Card>
    </div>
  );
}
function Audit() {
  const { data, error } = useAPI<any>("/api/v1/audit");
  const [older, setOlder] = useState<any[]>([]);
  const [next, setNext] = useState<string | undefined>();
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const items = [...(data.items || []), ...older];
  const cursor = next ?? data.next;
  const loadOlder = async () => {
    if (!cursor) return;
    const response = await fetch("/api/v1/audit?before=" + encodeURIComponent(cursor), { credentials: "same-origin" });
    if (!response.ok) return;
    const page = await response.json();
    setOlder((entries) => [...entries, ...(page.items || [])]);
    setNext(page.next || "");
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle>Audit-Protokoll</CardTitle>
        <CardDescription>
          Nachvollziehbare Aktionen aus Control Panel und MCP.
        </CardDescription>
      </CardHeader>
      <CardContent className="divide-y">
        {items.length ? items.map((event: any) => (
          <article key={event.ID} className="py-4 first:pt-0">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <strong className="text-sm">
                {event.Kind.replaceAll("_", " ")}
              </strong>
              <time className="text-xs text-muted-foreground">
                {new Date(event.CreatedAt).toLocaleString("de-DE")}
              </time>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              {event.Actor || "System"} ·{" "}
              {event.TokenName ? `Token: ${event.TokenName}` : "Control Panel"}{" "}
              · {event.ResourceType || "Ressource"}
            </p>
            <details className="mt-2 text-xs text-muted-foreground">
              <summary className="cursor-pointer select-none hover:text-foreground">
                Technische Details
              </summary>
              <dl className="mt-2 grid gap-1 rounded-md border bg-muted/30 p-3">
                <div><dt className="inline font-medium text-foreground">Ressource: </dt><dd className="inline">{event.ResourceType || "–"}</dd></div>
                <div><dt className="inline font-medium text-foreground">ID: </dt><dd className="inline break-all font-mono">{event.ResourceID || "–"}</dd></div>
                {event.Metadata && <div><dt className="font-medium text-foreground">Metadaten</dt><dd className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap font-mono">{event.Metadata}</dd></div>}
              </dl>
            </details>
          </article>
        )) : <EmptyState title="Noch keine Audit-Einträge" description="Aktionen im Control Panel und über MCP werden hier nachvollziehbar protokolliert." />}
        {cursor && (
          <Button className="mt-4" size="sm" variant="outline" onClick={loadOlder}>
            Ältere Audit-Einträge laden
          </Button>
        )}
      </CardContent>
    </Card>
  );
}
function RunConsole({ runID }: { runID: string }) {
  // The initial request deliberately fetches a bounded tail. Subsequent live
  // notifications request only entries after the final known sequence, so a
  // noisy terminal never causes the entire page or console history to reload.
  const [data, setData] = useState<any>();
  const [error, setError] = useState(false);
  const [olderLogs, setOlderLogs] = useState<any[]>([]);
  const [olderAvailable, setOlderAvailable] = useState<boolean | undefined>();
  const [message, setMessage] = useState("");
  const [newLogsAvailable, setNewLogsAvailable] = useState(false);
  const logRef = useRef<HTMLPreElement>(null);
  const followLogs = useRef(true);
  const latestSequence = useRef<number | undefined>(undefined);
  const olderScrollHeight = useRef<number | undefined>(undefined);
  const logs = data?.entries || [];
  const visibleLogs = [...olderLogs, ...logs];
  const canLoadOlder = olderAvailable ?? Boolean(data?.truncated);

  useEffect(() => {
    let stopped = false;
    let pending: number | undefined;
    let controller: AbortController | undefined;
    const load = async (incremental: boolean) => {
      controller?.abort();
      controller = new AbortController();
      const latest = latestSequence.current;
      const suffix = incremental && latest ? `?after=${encodeURIComponent(latest)}` : "";
      try {
        const response = await fetch(`/api/v1/runs/${runID}/logs${suffix}`, {
          credentials: "same-origin",
          signal: controller.signal,
        });
        if (!response.ok) throw new Error(await response.text());
        const next = await response.json();
        if (stopped) return;
        setData((current: any) => {
          if (!incremental || !current) {
            latestSequence.current = next.entries?.[next.entries.length - 1]?.Sequence || 0;
            return next;
          }
          const existing = current.entries || [];
          const additions = (next.entries || []).filter((entry: any) =>
            !existing.some((known: any) => known.Sequence === entry.Sequence),
          );
          // Preserve the stable console DOM if a notification has no fresh
          // line (for example a run-status update).
          if (!additions.length) return { ...current, status: next.status };
          const merged = [...existing, ...additions];
          // Keep a bounded live tail. Older entries remain explicitly
          // available through the paging action rather than growing the DOM
          // forever during a long-running agent session.
          const entries = merged.slice(-250);
          latestSequence.current = entries[entries.length - 1]?.Sequence || latestSequence.current;
          return { ...current, entries, truncated: current.truncated || merged.length > entries.length, status: next.status };
        });
        setError(false);
        // The API intentionally bounds every response. Catch up immediately
        // when a particularly chatty process produced more than one page,
        // without refreshing any surrounding resource.
        if (incremental && next.hasMore) {
          window.setTimeout(() => void load(true), 0);
        }
      } catch (reason: any) {
        if (!stopped && reason?.name !== "AbortError" && !data) setError(true);
      }
    };
    setData(undefined);
    setError(false);
    latestSequence.current = undefined;
    void load(false);
    const refresh = (event: Event) => {
      const change = (event as CustomEvent<LiveChange>).detail ?? {};
      // Run-log events include their owning run. Ignore output from every
      // other agent; a globally open SSE connection must not fan one terminal
      // line out into console refreshes for unrelated detail views.
      if (change.table !== "agent_run_logs" || change.run_id !== runID) return;
      window.clearTimeout(pending);
      pending = window.setTimeout(() => void load(true), 350);
    };
    window.addEventListener("taskboard:data-change", refresh);
    return () => {
      stopped = true;
      controller?.abort();
      window.clearTimeout(pending);
      window.removeEventListener("taskboard:data-change", refresh);
    };
  }, [runID]);

  const isNearEnd = () => {
    const log = logRef.current;
    return !!log && log.scrollHeight - log.scrollTop - log.clientHeight <= 48;
  };
  const scrollToLatest = () => {
    const log = logRef.current;
    if (!log) return;
    followLogs.current = true;
    setNewLogsAvailable(false);
    log.scrollTo({ top: log.scrollHeight, behavior: "auto" });
  };

  useEffect(() => {
    if (!data) return;
    const latest = Number(data.entries?.at(-1)?.Sequence ?? 0);
    const log = logRef.current;
    const shouldFollow = followLogs.current || !log || latestSequence.current === undefined;
    const receivedNewLogs = latestSequence.current !== undefined && latest > latestSequence.current;

    latestSequence.current = latest;
    if (receivedNewLogs && !shouldFollow) setNewLogsAvailable(true);
    if (shouldFollow) {
      requestAnimationFrame(() => {
        const current = logRef.current;
        if (current) current.scrollTop = current.scrollHeight;
      });
    }
  }, [data]);

  useEffect(() => {
    const log = logRef.current;
    const previousHeight = olderScrollHeight.current;
    if (!log || previousHeight === undefined) return;
    log.scrollTop += log.scrollHeight - previousHeight;
    olderScrollHeight.current = undefined;
  }, [olderLogs]);

  const handleLogScroll = () => {
    const nearEnd = isNearEnd();
    followLogs.current = nearEnd;
    if (nearEnd) setNewLogsAvailable(false);
  };
  const loadOlderLogs = async () => {
    const before = visibleLogs[0]?.Sequence;
    if (!before) return;
    const log = logRef.current;
    olderScrollHeight.current = log?.scrollHeight ?? 0;
    try {
      const response = await fetch(`/api/v1/runs/${runID}/logs?before=${encodeURIComponent(before)}`, { credentials: "same-origin" });
      if (!response.ok) throw new Error(await response.text());
      const page = await response.json();
      setOlderLogs((entries) => [...(page.entries || []), ...entries]);
      setOlderAvailable(Boolean(page.truncated));
    } catch (err) {
      olderScrollHeight.current = undefined;
      setMessage(String(err));
    }
  };
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Protokoll</CardTitle>
        <CardDescription>Aktuelle und historische Ausgaben dieses Runs.</CardDescription>
      </CardHeader>
      <CardContent>
        {error ? (
          <p className="text-sm text-destructive">Protokoll konnte nicht geladen werden.</p>
        ) : (
          <div className="relative">
            <pre
              ref={logRef}
              onScroll={handleLogScroll}
              aria-label="Run-Protokoll"
              className="max-h-[34rem] overflow-auto whitespace-pre-wrap rounded-lg bg-muted p-3 text-xs"
            >
            {visibleLogs.map((log: any) => `[${log.Sequence}] ${log.Level}: ${log.Message}`).join("\n") || "Noch keine Protokolleinträge."}
            </pre>
            {newLogsAvailable && (
              <Button
                className="absolute bottom-3 left-1/2 -translate-x-1/2 shadow-md"
                size="sm"
                onClick={scrollToLatest}
              >
                Neue Einträge anzeigen
              </Button>
            )}
          </div>
        )}
        {message && <p className="mt-3 text-sm text-destructive">{message}</p>}
        {canLoadOlder && visibleLogs.length > 0 && (
          <Button className="mt-3" size="sm" variant="outline" onClick={loadOlderLogs}>
            Ältere Ausgabe laden
          </Button>
        )}
      </CardContent>
    </Card>
  );
}

function RunDetail({ id }: { id: string }) {
  const { data, error } = useAPI<any>("/api/v1/runs/" + id);
  const [busy, setBusy] = useState(false);
  const [diff, setDiff] = useState("");
  const [trace, setTrace] = useState<any>();
  const [feedback, setFeedback] = useState("");
  const [message, setMessage] = useState("");
  const [confirmApply, setConfirmApply] = useState(false);
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const terminal = !["running", "queued"].includes(data.run.Status);
  const money = (microusd: number) => new Intl.NumberFormat("de-DE", { style: "currency", currency: "USD" }).format(microusd / 1e6);
  const action = async (path: string, body?: FormData) => {
    setBusy(true);
    try {
      await mutation("/runs/" + id + path, { method: "POST", body });
      refreshData();
    } catch (err) {
      setMessage(String(err));
    } finally {
      setBusy(false);
    }
  };
  const showDiff = async () => {
    try {
      const response = await fetch("/runs/" + id + "/diff", {
        credentials: "same-origin",
      });
      if (!response.ok) throw new Error(await response.text());
      setDiff(await response.text());
    } catch (err) {
      setMessage(String(err));
    }
  };
  const showTrace = async () => {
    try {
      const response = await fetch("/runs/" + id + "/trace", { credentials: "same-origin" });
      if (!response.ok) throw new Error(await response.text());
      setTrace(await response.json());
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
      <section>
        <a
          className="text-sm text-muted-foreground hover:text-foreground"
          href={"#/tasks/" + data.task.ID}
        >
          ← Zur Aufgabe
        </a>
        <h2 className="mt-5 text-2xl font-semibold">{data.task.Title}</h2>
        <p className="mt-2 text-sm text-muted-foreground">
          Run {data.run.ID} · {data.run.Status}
        </p>
        {message && <p className="mt-3 text-sm text-destructive">{message}</p>}
        <section className="mt-6 grid gap-6 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>Auslieferung</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2 text-sm">
              <p>
                <span className="text-muted-foreground">Qualitäts-Gate: </span>
                {data.delivery.GateStatus || "pending"}
              </p>
              <p>
                <span className="text-muted-foreground">Laufzeit: </span>
                {data.delivery.DurationSeconds || 0} s
              </p>
              <p>
                <span className="text-muted-foreground">Tokens: </span>
                {data.delivery.TokenUsage || 0}
              </p>
              <p>
                <span className="text-muted-foreground">Kosten (geschätzt): </span>
                {new Intl.NumberFormat("de-DE", { style: "currency", currency: "USD" }).format((data.delivery.EstimatedCostMicrousd || 0) / 1_000_000)}
              </p>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Diff & Qualitäts-Gate</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              <p>{data.delivery.DiffSummary || "Keine geänderten Dateien."}</p>
              <pre className="max-h-32 overflow-auto whitespace-pre-wrap rounded-md bg-muted p-2 text-xs">
                {data.delivery.GateOutput || "Noch keine Gate-Ausgabe."}
              </pre>
              {data.delivery.DiffSummary && (
                <Button size="sm" variant="outline" onClick={showDiff}>
                  Vollständigen Diff anzeigen
                </Button>
              )}
            </CardContent>
          </Card>
        </section>
        <Card className="mt-6">
          <CardHeader>
            <CardTitle>Usage & Kosten</CardTitle>
            <CardDescription>{data.usage?.Provider || "unbekannter Provider"} · {data.usage?.Model || "unbekanntes Modell"}</CardDescription>
          </CardHeader>
          <CardContent className="grid gap-2 text-sm sm:grid-cols-2">
            {([['Input', data.usage?.InputTokens], ['Output', data.usage?.OutputTokens], ['Cache-Input', data.usage?.CachedInputTokens], ['Cache-Schreiben', data.usage?.CacheWriteTokens], ['Reasoning', data.usage?.ReasoningTokens], ['Gesamt', data.usage?.TotalTokens]] as [string, number | null | undefined][]).map(([label, value]) => <p key={label}><span className="text-muted-foreground">{label}: </span>{value == null ? 'unbekannt' : value.toLocaleString('de-DE')}</p>)}
            <p><span className="text-muted-foreground">Kostenquelle: </span>{data.usage?.CostSource === 'reported' ? 'Provider gemeldet' : data.usage?.CostSource === 'estimated' ? 'Geschätzt' : data.usage?.CostSource === 'included' ? 'Inklusive' : 'Unbekannt'}</p>
            <p><span className="text-muted-foreground">Kosten: </span>{data.usage?.CalculatedCostMicrousd == null ? 'nicht bestimmbar' : money(data.usage.CalculatedCostMicrousd)}</p>
            <p className="sm:col-span-2 text-xs text-muted-foreground">Status: {data.usage?.Status || 'unknown'} · Preisversion: {data.usage?.PriceVersion || 'keine'}{data.usage?.CostCalculatedAt ? ` · ${new Date(data.usage.CostCalculatedAt).toLocaleString('de-DE')}` : ''}</p>
          </CardContent>
        </Card>
        {diff && (
          <Card className="mt-6">
            <CardHeader>
              <CardTitle>Vollständiger Diff</CardTitle>
            </CardHeader>
            <CardContent>
              <pre className="max-h-[30rem] overflow-auto whitespace-pre rounded-lg bg-muted p-3 text-xs">
                {diff}
              </pre>
            </CardContent>
          </Card>
        )}
        <RunConsole runID={id} />
        <Button className="mt-3" size="sm" variant="outline" onClick={showTrace}>
          Run-Trace anzeigen
        </Button>
        {trace && (
          <Card className="mt-4">
            <CardHeader>
              <CardTitle>Run-Trace</CardTitle>
              <CardDescription>Batch, Event und einzelne Verarbeitungsschritte.</CardDescription>
            </CardHeader>
            <CardContent className="divide-y">
              {trace.Items?.length ? trace.Items.map((item: any, index: number) => (
                <div key={`${item.At}-${index}`} className="py-3 first:pt-0">
                  <p className="text-sm font-medium">{item.Kind}</p>
                  <p className="mt-1 whitespace-pre-wrap text-sm text-muted-foreground">{item.Detail}</p>
                  <p className="mt-1 text-xs text-muted-foreground">{item.At ? new Date(item.At).toLocaleString("de-DE") : ""}</p>
                </div>
              )) : <p className="py-3 text-sm text-muted-foreground">Keine zusätzlichen Trace-Einträge vorhanden.</p>}
            </CardContent>
          </Card>
        )}
      </section>
      <aside className="space-y-4">
        <Card>
          <CardHeader>
            <CardTitle>Steuerung</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-2">
            {!terminal ? (
              <Button
                disabled={busy}
                variant="destructive"
                onClick={() => action("/cancel")}
              >
                Run abbrechen
              </Button>
            ) : (
              <Button disabled={busy} onClick={() => action("/restart")}>
                Mit aktuellen Settings neu starten
              </Button>
            )}
            {terminal &&
              !data.delivery.AppliedAt &&
              data.delivery.DiffSummary &&
              data.delivery.GateStatus === "passed" && (
                <>
                  <Button disabled={busy} onClick={() => setConfirmApply(true)}>
                    Änderungen übernehmen
                  </Button>
                  <Button
                    disabled={busy}
                    variant="outline"
                    onClick={() => {
                      if (confirm("Isolierte Änderungen wirklich verwerfen?"))
                        void action("/discard");
                    }}
                  >
                    Änderungen verwerfen
                  </Button>
                </>
              )}
            {terminal &&
              !data.delivery.AppliedAt &&
              !data.delivery.DiffSummary && (
                <Button
                  disabled={busy}
                  variant="outline"
                  onClick={() => {
                    if (confirm("Worktree dieses Runs bereinigen?"))
                      void action("/discard");
                  }}
                >
                  Worktree bereinigen
                </Button>
              )}
          </CardContent>
        </Card>
        <Dialog open={confirmApply} onOpenChange={setConfirmApply}>
          <DialogContent>
            <DialogHeader><DialogTitle>Änderungen übernehmen?</DialogTitle><DialogDescription>Die geprüften Änderungen werden in das zugewiesene Repository integriert und der Task anschließend zur Review weitergegeben.</DialogDescription></DialogHeader>
            <DialogFooter><Button variant="outline" onClick={() => setConfirmApply(false)}>Abbrechen</Button><Button disabled={busy} onClick={() => { setConfirmApply(false); void action("/apply"); }}>Bestätigen und übernehmen</Button></DialogFooter>
          </DialogContent>
        </Dialog>
        {terminal && !data.delivery.AppliedAt && (
          <Card>
            <CardHeader>
              <CardTitle>Änderung zurückweisen</CardTitle>
              <CardDescription>
                Das Feedback landet als Kommentar an der Aufgabe.
              </CardDescription>
            </CardHeader>
            <CardContent className="grid gap-3">
              <textarea
                className="min-h-24 rounded-md border bg-transparent p-2 text-sm"
                placeholder="Was soll beim nächsten Versuch anders sein?"
                value={feedback}
                onChange={(event) => setFeedback(event.target.value)}
              />
              <Button
                disabled={busy || !feedback.trim()}
                variant="outline"
                onClick={() => {
                  const form = new FormData();
                  form.set("feedback", feedback);
                  void action("/reject", form);
                }}
              >
                Mit Feedback zurückweisen
              </Button>
            </CardContent>
          </Card>
        )}
      </aside>
    </div>
  );
}
