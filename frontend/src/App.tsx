import { lazy, Suspense, useEffect, useRef, useState } from "react";
import {
  Activity,
  Bot,
  Boxes,
  FolderGit2,
  Gauge,
  LayoutDashboard,
  LoaderCircle,
  Menu,
  Moon,
  Play,
  ShieldCheck,
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

const Dashboard = lazy(() => import("@/features/dashboard"));
type NavItem = {
  name: string;
  path: string;
  endpoint?: string;
  icon: typeof LayoutDashboard;
};
const nav: NavItem[] = [
  { name: "Übersicht", path: "/", icon: LayoutDashboard },
  {
    name: "Projekte",
    path: "/projects",
    endpoint: "/api/v1/projects",
    icon: FolderGit2,
  },
  { name: "Boards", path: "/boards", endpoint: "/api/v1/boards", icon: Boxes },
  { name: "Agents", path: "/agents", endpoint: "/api/v1/agents", icon: Bot },
  {
    name: "Automationen",
    path: "/automations",
    endpoint: "/api/v1/automations",
    icon: Activity,
  },
  { name: "Skills", path: "/skills", endpoint: "/api/v1/skills", icon: Wrench },
  { name: "Runs", path: "/runs", endpoint: "/api/v1/runs", icon: Play },
  {
    name: "Audit",
    path: "/audit",
    endpoint: "/api/v1/audit",
    icon: ShieldCheck,
  },
  { name: "Einstellungen", path: "/settings/providers", icon: Gauge },
];
function routeFromHash() {
  return location.hash.slice(1) || "/";
}
function titleFor(route: string) {
  const known = nav.find((item) => item.path === route)?.name;
  if (known) return known;
  if (route === "/settings") return "Einstellungen";
  if (route === "/account") return "Konto & Zugriff";
  // Detail views intentionally keep their parent section in the persistent
  // header; the local page then supplies the concrete board, task or run name.
  if (route === "/boards") return "Boards";
  if (route === "/projects") return "Projekte";
  if (route === "/agents") return "Agents";
  if (route === "/automations") return "Automationen";
  if (route === "/skills") return "Skills";
  if (route === "/runs") return "Runs";
  if (route === "/audit") return "Audit";
  return "Shipyard";
}
type LiveChange = {
  table?: string;
  action?: string;
  id?: string;
};

function refreshData(change: LiveChange = {}) {
  window.dispatchEvent(
    new CustomEvent<LiveChange>("taskboard:data-change", { detail: change }),
  );
}

// A Postgres notification identifies the table that changed.  Keeping that
// information until the query hook means a running agent does not make every
// visible screen refetch (and therefore look like it is loading) for each log
// line it writes.
function endpointUsesChange(endpoint: string, change: LiveChange) {
  const table = change.table;
  if (!table) return true; // Explicit local refreshes intentionally refresh.
  const matches = (...tables: string[]) => tables.includes(table);
  if (endpoint.includes("/settings/appearance")) return matches("workspace_preferences");
  if (endpoint.includes("/settings/providers")) return matches("provider_settings");
  if (endpoint.includes("/settings/agent-policy")) return matches("agent_prompt_policy");
  if (endpoint.includes("/settings/integrations")) return matches("integration_connections");
  if (endpoint.includes("/account")) return matches("api_tokens", "users");
  // Templates are bundled configuration, not board records. They only need a
  // refresh after a local explicit action, which is handled by the table-less
  // refresh above.
  if (endpoint.includes("/board-templates")) return false;
  if (endpoint.includes("/skills")) return matches("skill_sources", "skills", "installed_skills", "agent_skills");
  if (endpoint.includes("/agents")) return matches("agents", "agent_skills", "installed_skills", "skills");
  if (endpoint.includes("/automations")) return matches("automation_rules", "automation_events", "agents", "workflow_columns", "labels", "boards");
  if (endpoint.includes("/schedules")) return matches("schedules", "agents", "boards");
  if (endpoint.includes("/webhooks")) return matches("webhook_subscriptions", "webhook_deliveries");
  if (endpoint.includes("/projects") || endpoint.includes("/project-groups")) return matches("projects", "board_projects", "project_groups", "project_group_members", "project_sources", "integration_connections");
  // Run logs are high-frequency terminal output.  Only the dedicated console
  // projection subscribes to them; list and task views already receive the
  // meaningful state change through agent_runs and must not redraw for every
  // line written by an agent.
  if (/^\/api\/v1\/runs\/[^/?]+\/logs(?:\?|$)/.test(endpoint)) return matches("agent_run_logs", "agent_runs");
  if (/^\/api\/v1\/runs\/[^/?]+(?:\?|$)/.test(endpoint)) return matches("agent_runs", "agent_run_batches", "workflow_runs", "workflow_steps", "task_repository_targets", "task_comments", "tasks");
  if (endpoint.includes("/runs")) return matches("agent_runs", "agent_run_batches", "workflow_runs", "workflow_steps", "task_repository_targets", "task_comments", "tasks");
  if (endpoint.includes("/tasks/")) return matches("tasks", "task_comments", "task_labels", "labels", "task_target_projects", "task_target_groups", "task_repository_targets", "agent_interactions", "task_decisions", "task_transitions", "agent_runs", "workflow_columns", "transitions");
  if (endpoint.includes("/boards/")) return matches("boards", "board_projects", "workflow_columns", "transitions", "tasks", "labels", "task_labels", "task_target_projects", "task_target_groups", "task_comments", "agent_runs");
  if (endpoint.includes("/boards")) return matches("boards", "tasks", "workflow_columns");
  if (endpoint.includes("/dashboard")) return matches("boards", "tasks", "agent_runs", "agent_run_batches", "notifications", "automation_events", "agent_interactions", "task_decisions");
  if (endpoint.includes("/audit")) return matches("audit_events");
  return true;
}

export default function App() {
  const [route, setRoute] = useState(routeFromHash);
  const { data: appearance } = useAPI<any>("/api/v1/settings/appearance");
  const [dark, setDark] = useState(
    localStorage.getItem("shipyard-theme") === "dark",
  );
  const [navOpen, setNavOpen] = useState(
    localStorage.getItem("shipyard-nav-open") !== "false",
  );
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const mobileNavRef = useRef<HTMLElement>(null);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
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
        ?.querySelector<HTMLAnchorElement>("[data-nav-index]")
        ?.focus();
    });
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMobileNavOpen(false);
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("keydown", closeOnEscape);
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
    setMobileNavOpen(false);
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
  const navigateNav = (index: number, event: React.KeyboardEvent<HTMLAnchorElement>) => {
    let next = index;
    if (event.key === "ArrowDown" || event.key === "ArrowRight") next = (index + 1) % nav.length;
    else if (event.key === "ArrowUp" || event.key === "ArrowLeft") next = (index - 1 + nav.length) % nav.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = nav.length - 1;
    else return;
    event.preventDefault();
    document.querySelector<HTMLAnchorElement>(`[data-nav-index="${next}"]`)?.focus();
  };
  const active = nav.find((item) => item.path === route);
  const showNavLabels = navOpen || mobileNavOpen;
  return (
    <TooltipProvider>
      <div className="min-h-dvh bg-background">
        <Button
          type="button"
          variant="outline"
          size="icon"
          aria-label="Navigation öffnen oder schließen"
          aria-controls="main-navigation"
          className="fixed left-3 top-3 z-30 md:left-4 md:top-4"
          onClick={toggleNavigation}
        >
          <Menu className="size-4" />
        </Button>
        <aside
          id="main-navigation"
          ref={mobileNavRef}
          className={`${mobileNavOpen ? "flex w-64" : "hidden"} fixed inset-y-0 left-0 z-20 flex-col border-r bg-card md:flex ${navOpen ? "md:w-64" : "md:w-16"}`}
        >
          <div className="flex h-16 items-center gap-3 border-b px-5">
            <span className="grid size-8 place-items-center rounded-lg bg-primary text-sm font-bold text-primary-foreground">
              SY
            </span>
            {showNavLabels && <span className="font-semibold">Shipyard</span>}
          </div>
          <nav className="flex-1 space-y-1 overflow-y-auto p-3">
            {nav.map((item, index) => {
              const Icon = item.icon;
              const selected = item.path === route;
              return (
                <a
                  key={item.path}
                  href={"#/".concat(item.path.slice(1))}
                  onClick={(event) => {
                    event.preventDefault();
                    navigate(item.path);
                  }}
                  onKeyDown={(event) => navigateNav(index, event)}
                  data-nav-index={index}
                  aria-current={selected ? "page" : undefined}
                  aria-label={item.name}
                  title={item.name}
                  className={
                    selected
                      ? "flex h-9 items-center gap-3 rounded-md bg-accent px-3 text-sm font-medium text-accent-foreground"
                      : "flex h-9 items-center gap-3 rounded-md px-3 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
                  }
                >
                  <Icon className="size-4" />
                  {showNavLabels && item.name}
                </a>
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
              {showNavLabels && (dark ? "Helles Design" : "Dunkles Design")}
            </Button>
          </div>
        </aside>
        {mobileNavOpen && (
          <button
            type="button"
            aria-label="Navigation schließen"
            className="fixed inset-0 z-10 bg-foreground/20 md:hidden"
            onClick={() => setMobileNavOpen(false)}
          />
        )}
        <Dialog open={shortcutsOpen} onOpenChange={setShortcutsOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Tastatursteuerung</DialogTitle>
              <DialogDescription>Die gesamte Oberfläche bleibt mit Standard-Fokussteuerung bedienbar.</DialogDescription>
            </DialogHeader>
            <dl className="grid gap-3 text-sm">
              <div><dt className="font-medium">Tab / Umschalt + Tab</dt><dd className="text-muted-foreground">Zum nächsten oder vorherigen Bedienelement wechseln.</dd></div>
              <div><dt className="font-medium">Enter / Leertaste</dt><dd className="text-muted-foreground">Fokussierten Link, Button oder Auswahl auslösen.</dd></div>
              <div><dt className="font-medium">↑ / ↓, Pos1 / Ende</dt><dd className="text-muted-foreground">Einträge in der Seitennavigation auswählen.</dd></div>
              <div><dt className="font-medium">?</dt><dd className="text-muted-foreground">Diese Hilfe öffnen.</dd></div>
              <div><dt className="font-medium">Escape</dt><dd className="text-muted-foreground">Dialog schließen.</dd></div>
            </dl>
          </DialogContent>
        </Dialog>
        <main className={`mx-auto max-w-7xl px-5 py-8 pt-20 md:px-8 ${navOpen ? "md:ml-64" : "md:ml-16"}`}>
          <header className="mb-8 flex justify-between gap-4">
            <div>
              <p className="text-sm font-medium text-primary">Operations</p>
              <h1 className="mt-1 text-3xl font-semibold tracking-tight">
                {titleFor(route.split("/").slice(0, 2).join("/") || "/")}
              </h1>
              <p className="mt-2 text-sm text-muted-foreground">
                {route === "/"
                  ? "Arbeitsfluss, Agentenläufe und Entscheidungen an einem Ort."
                  : "Verwalte die Ressourcen und Vorgänge deines Agenten-Systems."}
              </p>
            </div>
            <Badge variant="outline" className="h-fit gap-2 px-3 py-1.5">
              <span className="size-2 rounded-full bg-emerald-500" />
              System verbunden
            </Badge>
          </header>
          {route.match(/^\/boards\/[^/]+\/workflow$/) ? (
            <WorkflowEditorV2 id={route.split("/")[2]} />
          ) : route.startsWith("/boards/") ? (
            <BoardDetail id={route.split("/")[2]} />
          ) : route.startsWith("/tasks/") ? (
            <TaskDetail id={route.split("/")[2]} />
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
          ) : active?.endpoint ? (
            <ResourceList endpoint={active.endpoint} title={active.name} />
          ) : (
            <Settings route={route} />
          )}
        </main>
      </div>
    </TooltipProvider>
  );
}

function useAPI<T>(endpoint: string) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState(false);
  useEffect(() => {
    let stopped = false;
    let timer: number | undefined;
    let controller: AbortController | undefined;
    let hasData = false;
    const load = () => {
      controller?.abort();
      controller = new AbortController();
      fetch(endpoint, {
        credentials: "same-origin",
        signal: controller.signal,
      })
        .then((r) => (r.ok ? r.json() : Promise.reject()))
        .then((value) => {
          if (!stopped) {
            hasData = true;
            setData(value);
            setError(false);
          }
        })
        .catch((reason) => {
          // A newer live event supersedes an older request.  More
          // importantly, preserve an already usable view during a transient
          // background failure instead of replacing it with an error screen.
          if (!stopped && reason?.name !== "AbortError" && !hasData) {
            setError(true);
          }
        });
    };
    setData(undefined);
    setError(false);
    load();
    const refresh = (event: Event) => {
      const change = (event as CustomEvent<LiveChange>).detail ?? {};
      if (!endpointUsesChange(endpoint, change)) return;
      window.clearTimeout(timer);
      timer = window.setTimeout(load, 250);
    };
    window.addEventListener("taskboard:data-change", refresh);
    return () => {
      stopped = true;
      controller?.abort();
      window.clearTimeout(timer);
      window.removeEventListener("taskboard:data-change", refresh);
    };
  }, [endpoint]);
  return { data, error };
}
function csrf() {
  return (
    document.cookie
      .split("; ")
      .find((v) => v.startsWith("taskboard_csrf="))
      ?.split("=")
      .slice(1)
      .join("") ?? ""
  );
}
async function mutation(url: string, init: RequestInit) {
  const response = await fetch(url, {
    ...init,
    credentials: "same-origin",
    headers: { ...init.headers, "X-CSRF-Token": csrf() },
  });
  if (!response.ok) throw new Error(await response.text());
  // The legacy form handlers use PRG for success.  A validation failure on a
  // few of those handlers is historically rendered as an HTML page with 200.
  // A fetch would otherwise close the new dialog and falsely report success.
  if (
    !response.redirected &&
    response.headers.get("content-type")?.includes("text/html")
  ) {
    const page = new DOMParser().parseFromString(
      await response.text(),
      "text/html",
    );
    throw new Error(
      page.querySelector("[role=alert], .error")?.textContent?.trim() ??
        "Änderung konnte nicht gespeichert werden.",
    );
  }
  return response;
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
  const { data, error } = useAPI<Record<string, unknown>[]>(endpoint);
  if (error) return <Failure />;
  if (!data) return <Loading />;
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{data.length} Einträge</CardDescription>
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
                        "Eintrag",
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
                      item.WorkspacePath ??
                      "",
                  )}
                </p>
              </article>
            ))}
          </div>
        ) : (
          <p className="py-12 text-center text-sm text-muted-foreground">
            Noch keine Einträge vorhanden.
          </p>
        )}
      </CardContent>
    </Card>
  );
}
function Boards() {
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
        "Board wirklich löschen? Alle darin enthaltenen Aufgaben werden entfernt.",
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
          <CardTitle>Boards</CardTitle>
          <CardDescription>{items.length} Boards</CardDescription>
        </div>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button>Board anlegen</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Neues Board</DialogTitle>
              <DialogDescription>
                Wähle eine Vorlage; der Workflow bleibt danach vollständig
                anpassbar.
              </DialogDescription>
            </DialogHeader>
            <form onSubmit={create} className="grid gap-4">
              <label className="grid gap-2 text-sm font-medium">
                Name
                <Input
                  autoFocus
                  required
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="z. B. Plattform"
                />
              </label>
              <fieldset className="grid gap-2">
                <legend className="text-sm font-medium">
                  Workflow-Vorlage
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
                <Button type="submit">Board erstellen</Button>
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
                  Erstellt{" "}
                  {new Date(String(item.CreatedAt)).toLocaleDateString("de-DE")}
                </p>
              </a>
              <Button
                variant="destructive"
                size="sm"
                onClick={() => remove(String(item.ID))}
              >
                Löschen
              </Button>
            </article>
          ))}
        </div> : <EmptyState title="Noch kein Board" description="Lege ein Board aus einer Workflow-Vorlage an, um Aufgaben und Automationen zu organisieren." />}
      </CardContent>
    </Card>
  );
}
function Projects() {
  const { data: items, error: loadFailed } = useAPI<any[]>("/api/v1/projects");
  const { data: boards } = useAPI<any[]>("/api/v1/boards");
  const { data: groups } = useAPI<any[]>("/api/v1/project-groups");
  const [editing, setEditing] = useState<any>();
  const [open, setOpen] = useState(false);
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
    if (!confirm("Projekt wirklich löschen?")) return;
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
          <CardTitle>Projekte</CardTitle>
          <CardDescription>
            Repositories, Gruppen und Board-Zuordnung.
          </CardDescription>
        </div>
        <Button
          onClick={() => {
            setEditing(undefined);
            setOpen(true);
          }}
        >
          Projekt anlegen
        </Button>
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
                    {project.RepositoryURL || "Kein Repository verbunden"} ·{" "}
                    {project.DefaultBranch}
                  </p>
                  <p className={"mt-1 text-xs " + (project.LastSyncError ? "text-destructive" : "text-muted-foreground")}>
                    {project.LastSyncError
                      ? `Sync-Fehler: ${project.LastSyncError}`
                      : project.LastSyncedAt
                        ? `Zuletzt synchronisiert: ${new Date(project.LastSyncedAt).toLocaleString("de-DE")}`
                        : "Noch nicht synchronisiert"}
                  </p>
                  <div className="mt-2 flex flex-wrap gap-1">
                    {project.Boards?.map((board: any) => (
                      <Badge key={board.ID} variant="outline">
                        Board: {board.Name}
                      </Badge>
                    ))}
                    {groups.filter((group) => grouped(project, group)).map((group) => (
                      <Badge key={group.ID} variant="secondary">
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
                    Bearbeiten
                  </Button>
                  <Button
                    variant="destructive"
                    size="sm"
                    onClick={() => remove(project.ID)}
                  >
                    Löschen
                  </Button>
                </div>
              </div>
            </article>
          ))}
        </div> : <EmptyState title="Noch kein Projekt" description="Lege ein Repository an und ordne es bei Bedarf Boards und Projektgruppen zu." />}
      </CardContent>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editing ? "Projekt bearbeiten" : "Neues Projekt"}
            </DialogTitle>
            <DialogDescription>
              Gruppen werden beim Speichern zugewiesen; nicht verwendete Gruppen
              verschwinden automatisch.
            </DialogDescription>
          </DialogHeader>
          <form className="grid gap-3" onSubmit={save}>
            <label className="grid gap-1 text-sm">
              Name
              <Input name="name" required defaultValue={editing?.Name || ""} />
            </label>
            <label className="grid gap-1 text-sm">
              Repository-URL
              <Input
                name="repository_url"
                defaultValue={editing?.RepositoryURL || ""}
              />
            </label>
            <label className="grid gap-1 text-sm">
              Standard-Branch
              <Input
                name="default_branch"
                defaultValue={editing?.DefaultBranch || "main"}
              />
            </label>
            <label className="grid gap-1 text-sm">
              Lokaler Clone-Pfad
              <Input
                name="local_path"
                defaultValue={editing?.LocalPath || ""}
              />
            </label>
            <fieldset className="grid gap-2">
              <legend className="text-sm font-medium">Boards</legend>
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
              <legend className="text-sm font-medium">Gruppen</legend>
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
                placeholder="Neue Gruppe, z. B. Inhouse APIs"
              />
            </fieldset>
            <DialogFooter>
              <Button type="submit">Speichern</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
function Agents() {
  const { data, error } = useAPI<any[]>("/api/v1/agents");
  if (error) return <Failure />;
  if (!data) return <Loading />;
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div>
          <CardTitle>Agents</CardTitle>
          <CardDescription>Agenten öffnen und bearbeiten.</CardDescription>
        </div>
        <div className="flex gap-2">
          <Button asChild variant="outline"><a href="#/agents/templates">Vorlagen</a></Button>
          <Button asChild><a href="#/agents/new">Agent anlegen</a></Button>
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
                {agent.Enabled ? "Aktiv" : "Inaktiv"}
              </Badge>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              {agent.Description || agent.WorkspacePath}
            </p>
          </a>
        )) : <EmptyState title="Noch kein Agent" description="Lege einen Agenten mit Workspace, Arbeitsanweisung und erlaubten Skills an." actionHref="#/agents/new" actionLabel="Agent anlegen" />}
      </CardContent>
    </Card>
  );
}
function AgentDetail({ id }: { id: string }) {
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
    body.set("workspace_path", form.WorkspacePath);
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
      setMessage("Gespeichert.");
    } catch (err) {
      setMessage(String(err));
    }
  };
  const remove = async () => {
    if (!confirm("Agent wirklich löschen?")) return;
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
        <CardTitle>Agent bearbeiten</CardTitle>
        <CardDescription>
          Profil, Arbeitsanweisung und erlaubte Skills.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="grid gap-4" onSubmit={save}>
          <label className="grid gap-2 text-sm">
            Name
            <Input
              value={form.Name}
              onChange={(e) => setForm({ ...form, Name: e.target.value })}
            />
          </label>
          <label className="grid gap-2 text-sm">
            Beschreibung
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={form.Description || ""}
              onChange={(e) =>
                setForm({ ...form, Description: e.target.value })
              }
            />
          </label>
          <label className="grid gap-2 text-sm">
            Workspace
            <Input
              value={form.WorkspacePath}
              onChange={(e) =>
                setForm({ ...form, WorkspacePath: e.target.value })
              }
            />
          </label>
          <label className="grid gap-2 text-sm">
            Arbeitsanweisung
            <textarea
              className="min-h-32 rounded-lg border bg-transparent p-2"
              value={form.Prompt || ""}
              onChange={(e) => setForm({ ...form, Prompt: e.target.value })}
            />
          </label>
          <label className="grid gap-2 text-sm">
            Prompt-Prefix
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={form.PromptPrefix || ""}
              onChange={(e) => setForm({ ...form, PromptPrefix: e.target.value })}
              placeholder="Wird vor der Arbeitsanweisung und dem Task-Kontext gesetzt."
            />
          </label>
          <label className="grid gap-2 text-sm">
            Prompt-Suffix
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={form.PromptSuffix || ""}
              onChange={(e) => setForm({ ...form, PromptSuffix: e.target.value })}
              placeholder="Definiert Abschluss, Übergabe und Rückmeldungen des Agents."
            />
          </label>
          <label className="grid gap-2 text-sm">
            Maximal parallele Runs
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
            Agent aktiv
          </label>
          <fieldset className="grid gap-2">
            <legend className="text-sm font-medium">Erlaubte Skills</legend>
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
            <Button type="submit">Änderungen speichern</Button>
            <Button type="button" variant="destructive" onClick={remove}>
              Löschen
            </Button>
            <Button asChild type="button" variant="outline"><a href="#/agents">Zurück</a></Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
function AgentForm() {
  const { data: skills } = useAPI<any[]>("/api/v1/skills");
  const [name, setName] = useState("");
  const [workspace, setWorkspace] = useState("");
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
    form.set("workspace_path", workspace);
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
        <CardTitle>Neuer Agent</CardTitle>
        <CardDescription>
          Der Workspace muss ein zugängliches lokales Git-Repository sein.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="grid gap-4" onSubmit={submit}>
          <label className="grid gap-2 text-sm">
            Name
            <Input
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label className="grid gap-2 text-sm">
            Workspace
            <Input
              required
              value={workspace}
              onChange={(e) => setWorkspace(e.target.value)}
              placeholder="/home/agent/projekt"
            />
          </label>
          <label className="grid gap-2 text-sm">
            Beschreibung
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </label>
          <label className="grid gap-2 text-sm">
            Arbeitsanweisung
            <textarea
              className="min-h-32 rounded-lg border bg-transparent p-2"
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
            />
          </label>
          <label className="grid gap-2 text-sm">
            Prompt-Prefix
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={prefix}
              onChange={(e) => setPrefix(e.target.value)}
              placeholder="Fester Kontext vor der Arbeitsanweisung"
            />
          </label>
          <label className="grid gap-2 text-sm">
            Prompt-Suffix
            <textarea
              className="min-h-20 rounded-lg border bg-transparent p-2"
              value={suffix}
              onChange={(e) => setSuffix(e.target.value)}
              placeholder="Übergabe- und Abschlussregeln"
            />
          </label>
          <fieldset className="grid gap-2">
            <legend className="text-sm font-medium">Erlaubte Skills</legend>
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
          <Button type="submit">Agent erstellen</Button>
        </form>
      </CardContent>
    </Card>
  );
}
function Settings({ route }: { route: string }) {
  const tab =
    route === "/account" ? "account" : route.split("/").pop() || "providers";
  const tabs = [
    ["providers", "Provider"],
    ["agent-policy", "Agentenrichtlinien"],
    ["appearance", "Darstellung"],
    ["integrations", "Integrationen"],
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
      {tab === "agent-policy" ? (
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
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await mutation("/settings/appearance", {
        method: "POST",
        body: new FormData(e.target as HTMLFormElement),
      });
      refreshData();
      setMessage("Darstellung gespeichert.");
    } catch (err) {
      setMessage(String(err));
    }
  };
  return (
    <Card>
      <CardHeader>
        <CardTitle>Darstellung & Bedienung</CardTitle>
        <CardDescription>
          Lege Theme und sichtbare Tastaturhinweise fest.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="grid gap-5" onSubmit={save}>
          <fieldset className="grid gap-2">
            <legend className="font-medium">Darstellung</legend>
            {["system", "light", "dark"].map((value) => (
              <label key={value} className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="theme"
                  value={value}
                  defaultChecked={data.Theme === value}
                />
                {value === "system"
                  ? "Systemdarstellung verwenden"
                  : value === "light"
                    ? "Hell"
                    : "Dunkel"}
              </label>
            ))}
          </fieldset>
          <fieldset className="grid gap-2">
            <legend className="font-medium">Tastatur</legend>
            <label className="flex items-center gap-2 text-sm">
              <input
                name="shortcut_hints"
                type="checkbox"
                value="true"
                defaultChecked={data.ShortcutHints}
              />{" "}
              Hinweise zu Shortcuts anzeigen
            </label>
          </fieldset>
          <Button className="w-fit" type="submit">
            Einstellungen speichern
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
  return (
    <Card>
      <CardContent className="flex min-h-56 items-center justify-center gap-3 text-sm text-muted-foreground">
        <LoaderCircle className="size-5 animate-spin" />
        Lade Betriebsdaten …
      </CardContent>
    </Card>
  );
}
function Failure() {
  return (
    <Card>
      <CardContent className="flex min-h-56 flex-col items-center justify-center gap-4 py-12 text-center">
        <p className="text-sm text-destructive">
          Daten konnten nicht geladen werden. Bitte erneut versuchen.
        </p>
        <Button type="button" size="sm" variant="outline" onClick={() => refreshData()}>
          Daten erneut laden
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
  const [workspace, setWorkspace] = useState("/home/agent/taskboard");
  const [kind, setKind] = useState("implementation");
  const [message, setMessage] = useState("");
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const form = new FormData();
    form.set("name", name);
    form.set("workspace", workspace);
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
            Workspace
            <Input
              required
              value={workspace}
              onChange={(e) => setWorkspace(e.target.value)}
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
      refreshData();
    } catch (err) {
      setMessage(String(err));
    }
  };
  const remove = async (path: string, label: string) => {
    if (!confirm(`${label} wirklich löschen?`)) return;
    try {
      await mutation(path, { method: "POST" });
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
function BoardDetail({ id }: { id: string }) {
  const { data, error } = useAPI<any>("/api/v1/boards/" + id);
  const [open, setOpen] = useState(false);
  const [labelsOpen, setLabelsOpen] = useState(false);
  const [settings, setSettings] = useState(false);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [message, setMessage] = useState("");
  const [draggedTask, setDraggedTask] = useState("");
  const touchDrag = useRef<{ taskID: string; startX: number; startY: number; active: boolean } | undefined>(undefined);
  const suppressTaskClick = useRef(false);
  const refresh = () => refreshData();
  if (error) return <Failure />;
  if (!data) return <Loading />;
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
                <label className="grid gap-2 text-sm font-medium">
                  Beschreibung
                  <textarea
                    name="description"
                    className="min-h-28 rounded-lg border bg-transparent p-2"
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                  />
                </label>
                <DialogFooter>
                  <Button type="submit">Aufgabe speichern</Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        </div>
      </div>
      {message && <p className="mb-3 text-sm text-destructive">{message}</p>}
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
              <Badge variant="secondary">
                {
                  data.Tasks.filter((task: any) => task.ColumnID === column.ID)
                    .length
                }
              </Badge>
            </div>
            <div className="space-y-2">
              {data.Tasks.filter(
                (task: any) => task.ColumnID === column.ID,
              ).map((task: any) => (
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
                    touchDrag.current = { taskID: task.ID, startX: event.clientX, startY: event.clientY, active: false };
                  }}
                  onPointerMove={(event) => {
                    const active = touchDrag.current;
                    if (event.pointerType !== "touch" || !active || active.taskID !== task.ID) return;
                    if (Math.hypot(event.clientX - active.startX, event.clientY - active.startY) > 12) {
                      active.active = true;
                      setDraggedTask(task.ID);
                    }
                  }}
                  onPointerUp={(event) => {
                    const active = touchDrag.current;
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
                  className={"block rounded-lg border bg-card p-3 text-sm shadow-sm transition hover:border-primary touch-none " + (draggedTask === task.ID ? "dragging opacity-60 ring-2 ring-primary" : "")}
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
            </div>
          </section>
        ))}
      </div>
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
              <Input name="name" required defaultValue={data.Board.Name} />
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

function TaskDetail({ id }: { id: string }) {
  const { data, error } = useAPI<any>("/api/v1/tasks/" + id);
  const [comment, setComment] = useState("");
  const [edit, setEdit] = useState(false);
  const [targets, setTargets] = useState(false);
  const [handoff, setHandoff] = useState(false);
  const [decision, setDecision] = useState(false);
  const [message, setMessage] = useState("");
  const refresh = () => refreshData();
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const task = data.Task;
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
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <section>
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
          <p className="mt-3 whitespace-pre-wrap text-sm text-muted-foreground">
            {task.Description || "Keine Beschreibung."}
          </p>
          {message && (
            <p className="mt-3 text-sm text-destructive">{message}</p>
          )}
          <Card className="mt-6">
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
                    <p className="mt-2 text-sm text-muted-foreground">
                      {interaction.Body}
                    </p>
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
              <div className="space-y-4">
                {data.Comments.map((entry: any) => (
                  <div key={entry.ID} className="border-b pb-4 last:border-0">
                    <p className="text-sm font-medium">{entry.Author}</p>
                    <p className="mt-1 whitespace-pre-wrap text-sm text-muted-foreground">
                      {entry.Body}
                    </p>
                  </div>
                ))}
              </div>
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
        </section>
        <aside className="space-y-4">
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
        </aside>
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
            <label className="grid gap-1 text-sm">
              Beschreibung
              <textarea
                name="description"
                className="min-h-24 rounded-md border bg-transparent p-2"
                defaultValue={task.Description}
              />
            </label>
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
  const { data, error } = useAPI<any>(`/api/v1/runs/${runID}/logs`);
  const [olderLogs, setOlderLogs] = useState<any[]>([]);
  const [olderAvailable, setOlderAvailable] = useState<boolean | undefined>();
  const [message, setMessage] = useState("");
  const logs = data?.entries || [];
  const visibleLogs = [...olderLogs, ...logs];
  const canLoadOlder = olderAvailable ?? Boolean(data?.truncated);
  const loadOlderLogs = async () => {
    const before = visibleLogs[0]?.Sequence;
    if (!before) return;
    try {
      const response = await fetch(`/api/v1/runs/${runID}/logs?before=${encodeURIComponent(before)}`, { credentials: "same-origin" });
      if (!response.ok) throw new Error(await response.text());
      const page = await response.json();
      setOlderLogs((entries) => [...(page.entries || []), ...entries]);
      setOlderAvailable(Boolean(page.truncated));
    } catch (err) {
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
          <pre className="max-h-[34rem] overflow-auto whitespace-pre-wrap rounded-lg bg-muted p-3 text-xs">
            {visibleLogs.map((log: any) => `[${log.Sequence}] ${log.Level}: ${log.Message}`).join("\n") || "Noch keine Protokolleinträge."}
          </pre>
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
  if (error) return <Failure />;
  if (!data) return <Loading />;
  const terminal = !["running", "queued"].includes(data.run.Status);
  const action = async (path: string, body?: FormData) => {
    setBusy(true);
    try {
      await mutation("/runs/" + id + path, { method: "POST", body });
      refreshData();
    } catch (err) {
      setMessage(String(err));
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
                  <Button disabled={busy} onClick={() => action("/apply")}>
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
