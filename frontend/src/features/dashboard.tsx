import { type ReactNode, useEffect, useRef, useState } from "react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { LoaderCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";

type Metric = { Name: string; Count: number };
type DashboardData = {
  Total: number;
  Active: number;
  Completed: number;
  ByColumn: Metric[];
  CreatedSeries: Metric[];
  CompletedSeries: Metric[];
  Runs: { Queued: number; Running: number; Succeeded: number; Failed: number };
  EstimatedCostMicrousd: number;
  Notifications: { ID: string; Kind: string; Message: string }[];
};
type Attention = {
  BlockedTasks: number;
  FailedRuns7d: number;
  OpenInteractions: number;
  DueNext24h: number;
};

function useDashboard() {
  const [data, setData] = useState<DashboardData>();
  const [attention, setAttention] = useState<Attention>();
  const [error, setError] = useState(false);
  useEffect(() => {
    let stopped = false;
    let timer: number | undefined;
    let controller: AbortController | undefined;
    let hasData = false;
    const load = () => {
      controller?.abort();
      controller = new AbortController();
      fetch("/api/v1/dashboard", { credentials: "same-origin", signal: controller.signal })
        .then((response) => (response.ok ? response.json() : Promise.reject()))
        .then((value) => {
          if (!stopped) {
            hasData = true;
            setData(value);
            setError(false);
          }
        })
        .catch((reason) => {
          if (!stopped && reason?.name !== "AbortError" && !hasData) setError(true);
        });
    };
    const loadAttention = () => {
      fetch("/dashboard/attention", { credentials: "same-origin" })
        .then((response) => (response.ok ? response.json() : Promise.reject()))
        .then((value) => !stopped && setAttention(value))
        // Attention is additive: a temporary unavailable metric must never
        // hide the dashboard's working operational data.
        .catch(() => undefined);
    };
    load();
    loadAttention();
    const refresh = (event: Event) => {
      const table = (event as CustomEvent<{ table?: string }>).detail?.table;
      if (table && !["boards", "tasks", "agent_runs", "agent_run_batches", "notifications", "automation_events", "agent_interactions", "task_decisions"].includes(table)) return;
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        load();
        loadAttention();
      }, 250);
    };
    window.addEventListener("taskboard:data-change", refresh);
    return () => {
      stopped = true;
      controller?.abort();
      window.clearTimeout(timer);
      window.removeEventListener("taskboard:data-change", refresh);
    };
  }, []);
  return { data, attention, error };
}

function csrf() {
  return document.cookie.split("; ").find((value) => value.startsWith("taskboard_csrf="))?.split("=").slice(1).join("") ?? "";
}

function MeasuredChart({ children }: { children: ReactNode }) {
  const host = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  useEffect(() => {
    const element = host.current;
    if (!element) return;
    const measure = () => {
      const { width, height } = element.getBoundingClientRect();
      setSize((previous) => previous.width === width && previous.height === height ? previous : { width, height });
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return <div ref={host} className="size-full">{size.width > 0 && size.height > 0 && <ResponsiveContainer width={size.width} height={size.height}>{children}</ResponsiveContainer>}</div>;
}

function RunStatus({ runs }: { runs: DashboardData["Runs"] }) {
  return <Card className="lg:col-span-2"><CardHeader><CardTitle>Run-Status</CardTitle><CardDescription>Aktueller Zustand aller Agentenläufe.</CardDescription></CardHeader><CardContent className="space-y-3">{Object.entries(runs).map(([name, value]) => <div key={name} className="flex justify-between text-sm"><span className="capitalize text-muted-foreground">{name}</span><Badge variant={name === "Failed" ? "destructive" : "secondary"}>{value}</Badge></div>)}</CardContent></Card>;
}

function Notifications({ items }: { items: DashboardData["Notifications"] }) {
  const [hidden, setHidden] = useState<string[]>([]);
  const visible = items.filter((item) => !hidden.includes(item.ID));
  const read = async (id: string) => {
    const response = await fetch(`/notifications/${id}/read`, { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": csrf() } });
    if (response.ok) setHidden((values) => [...values, id]);
  };
  return <Card className="lg:col-span-2"><CardHeader><CardTitle>Benachrichtigungen</CardTitle><CardDescription>Was jetzt deine Aufmerksamkeit braucht.</CardDescription></CardHeader><CardContent>{visible.length ? visible.map((item) => <div key={item.ID} className="flex gap-3"><div className="min-w-0 flex-1"><p className="text-sm">{item.Message}</p><p className="mt-1 text-xs text-muted-foreground">{item.Kind}</p><Separator className="my-3" /></div><Button size="sm" variant="ghost" className="shrink-0" onClick={() => void read(item.ID)}>Erledigt</Button></div>) : <p className="py-8 text-center text-sm text-muted-foreground">Keine offenen Hinweise.</p>}</CardContent></Card>;
}

export default function Dashboard() {
  const { data, attention, error } = useDashboard();
  if (error) return <Card><CardContent className="py-12 text-center text-sm text-destructive">Daten konnten nicht geladen werden. Bitte erneut versuchen.</CardContent></Card>;
  if (!data) return <Card><CardContent className="flex min-h-56 items-center justify-center gap-3 text-sm text-muted-foreground"><LoaderCircle className="size-5 animate-spin" />Lade Betriebsdaten …</CardContent></Card>;
  const series = data.CreatedSeries.map((metric, index) => ({ day: metric.Name, created: metric.Count, completed: data.CompletedSeries[index]?.Count ?? 0 }));
  const costs = new Intl.NumberFormat("de-DE", { style: "currency", currency: "USD" }).format(data.EstimatedCostMicrousd / 1e6);
  const stats = [["Offene Aufgaben", data.Active, "Im aktiven Workflow"], ["Abgeschlossen", data.Completed, "Seit Beginn"], ["Laufende Agents", data.Runs.Running, `${data.Runs.Queued} warten`], ["Geschätzte Kosten", costs, "Alle Agentenläufe"]];
  const priorityItems = attention ? [
    ["Blockierte Tasks", attention.BlockedTasks, "#/boards"],
    ["Fehlgeschlagene Runs · 7 Tage", attention.FailedRuns7d, "#/runs"],
    ["Offene Agent-Fragen", attention.OpenInteractions, "#/boards"],
    ["Fällig in 24 Stunden", attention.DueNext24h, "#/boards"],
  ] : [];
  return <>
    <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">{stats.map(([name, value, detail]) => <Card key={name as string}><CardHeader className="pb-2"><CardDescription>{name}</CardDescription><CardTitle className="text-2xl">{value}</CardTitle></CardHeader><CardContent className="text-xs text-muted-foreground">{detail}</CardContent></Card>)}</section>
    {priorityItems.length > 0 && <Card className="mt-6"><CardHeader><CardTitle>Braucht Aufmerksamkeit</CardTitle><CardDescription>Offene Punkte, die als Nächstes eine Entscheidung oder Prüfung brauchen.</CardDescription></CardHeader><CardContent className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">{priorityItems.map(([label, count, href]) => <a key={label as string} href={href as string} className="rounded-lg border p-4 transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><strong className="block text-2xl">{count}</strong><span className="mt-1 block text-sm text-muted-foreground">{label}</span></a>)}</CardContent></Card>}
    <section className="mt-6 grid gap-6 lg:grid-cols-5"><Card className="lg:col-span-3"><CardHeader><CardTitle>Durchsatz</CardTitle><CardDescription>Erstellt und abgeschlossen in den letzten 14 Tagen.</CardDescription></CardHeader><CardContent className="h-72"><MeasuredChart><LineChart data={series}><CartesianGrid vertical={false} strokeDasharray="3 3" /><XAxis dataKey="day" tickLine={false} axisLine={false} /><YAxis allowDecimals={false} tickLine={false} axisLine={false} /><Tooltip /><Line type="monotone" dataKey="created" stroke="var(--chart-1)" strokeWidth={2} dot={false} /><Line type="monotone" dataKey="completed" stroke="var(--chart-2)" strokeWidth={2} dot={false} /></LineChart></MeasuredChart></CardContent></Card><RunStatus runs={data.Runs} /></section>
    <section className="mt-6 grid gap-6 lg:grid-cols-5"><Card className="lg:col-span-3"><CardHeader><CardTitle>Arbeit im Workflow</CardTitle><CardDescription>Aktive Aufgaben nach Board und Spalte.</CardDescription></CardHeader><CardContent className="h-64"><MeasuredChart><BarChart data={data.ByColumn}><CartesianGrid vertical={false} strokeDasharray="3 3" /><XAxis dataKey="Name" hide /><YAxis allowDecimals={false} tickLine={false} axisLine={false} /><Tooltip /><Bar dataKey="Count" fill="var(--chart-1)" radius={[5, 5, 0, 0]} /></BarChart></MeasuredChart></CardContent></Card><Notifications items={data.Notifications} /></section>
  </>;
}
