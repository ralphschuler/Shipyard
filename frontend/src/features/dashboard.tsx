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
  KnownActualCostMicrousd: number;
  EstimatedCostMicrousdV2: number;
  IncludedOrUnknownRuns: number;
  IncludedOrUnknownTokens: number;
  UsageByDimension: { Dimension: string; Name: string; Tokens: number; ActualMicrousd: number; EstimatedMicrousd: number }[];
  TelemetrySeries: { Day: string; ActualMicrousd: number; EstimatedMicrousd: number; Tokens: number }[];
  Notifications: { ID: string; Kind: string; Message: string }[];
};
type Attention = {
  BlockedTasks: number;
  FailedRuns7d: number;
  OpenInteractions: number;
  DueNext24h: number;
};

function useDashboard(range: string, from: string, to: string) {
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
      const query = new URLSearchParams({ range });
      if (range === "custom") { query.set("from", from); query.set("to", to); }
      fetch(`/api/v1/dashboard?${query}`, { credentials: "same-origin", signal: controller.signal })
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
  }, [range, from, to]);
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
  const [range, setRange] = useState("30d");
  const [customFrom, setCustomFrom] = useState(() => new Date(Date.now() - 6 * 86400000).toISOString().slice(0, 10));
  const [customTo, setCustomTo] = useState(() => new Date().toISOString().slice(0, 10));
  const { data, attention, error } = useDashboard(range, customFrom, customTo);
  if (error) return <Card><CardContent className="py-12 text-center text-sm text-destructive">Daten konnten nicht geladen werden. Bitte erneut versuchen.</CardContent></Card>;
  if (!data) return <Card><CardContent className="flex min-h-56 items-center justify-center gap-3 text-sm text-muted-foreground"><LoaderCircle className="size-5 animate-spin" />Lade Betriebsdaten …</CardContent></Card>;
  const series = data.CreatedSeries.map((metric, index) => ({ day: metric.Name, created: metric.Count, completed: data.CompletedSeries[index]?.Count ?? 0 }));
  const money = (microusd: number) => new Intl.NumberFormat("de-DE", { style: "currency", currency: "USD" }).format(microusd / 1e6);
  const stats = [["Offene Aufgaben", data.Active, "Im aktiven Workflow"], ["Abgeschlossen", data.Completed, "Seit Beginn"], ["Laufende Agents", data.Runs.Running, `${data.Runs.Queued} warten`], ["Istkosten", money(data.KnownActualCostMicrousd), "Providerseitig gemeldet"], ["Schätzung", money(data.EstimatedCostMicrousdV2), "Nach Preisregel berechnet"], ["Unklar / inkludiert", data.IncludedOrUnknownRuns, `${data.IncludedOrUnknownTokens.toLocaleString("de-DE")} Tokens ohne Grenzpreis`]];
  const priorityItems = attention ? [
    ["Blockierte Tasks", attention.BlockedTasks, "#/boards"],
    ["Fehlgeschlagene Runs · 7 Tage", attention.FailedRuns7d, "#/runs"],
    ["Offene Agent-Fragen", attention.OpenInteractions, "#/boards"],
    ["Fällig in 24 Stunden", attention.DueNext24h, "#/boards"],
  ] : [];
  const dimensions = data.UsageByDimension ?? [];
  return <>
    <div className="mb-6 flex flex-wrap items-center gap-2" aria-label="Zeitraum für Kosten und Usage">
      <span className="mr-2 text-sm text-muted-foreground">Zeitraum</span>
      {[['today','Heute'],['7d','7 Tage'],['30d','30 Tage'],['all','Gesamte Historie'],['custom','Benutzerdefiniert']].map(([value, label]) => <Button key={value} size="sm" variant={range === value ? "default" : "outline"} onClick={() => setRange(value)}>{label}</Button>)}
      {range === "custom" && <><label className="ml-2 text-sm">Von <input aria-label="Startdatum" type="date" value={customFrom} onChange={(event) => setCustomFrom(event.target.value)} /></label><label className="text-sm">Bis <input aria-label="Enddatum" type="date" value={customTo} onChange={(event) => setCustomTo(event.target.value)} /></label></>}
    </div>
    <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">{stats.map(([name, value, detail]) => <Card key={name as string}><CardHeader className="pb-2"><CardDescription>{name}</CardDescription><CardTitle className="text-2xl">{value}</CardTitle></CardHeader><CardContent className="text-xs text-muted-foreground">{detail}</CardContent></Card>)}</section>
    {priorityItems.length > 0 && <Card className="mt-6"><CardHeader><CardTitle>Braucht Aufmerksamkeit</CardTitle><CardDescription>Offene Punkte, die als Nächstes eine Entscheidung oder Prüfung brauchen.</CardDescription></CardHeader><CardContent className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">{priorityItems.map(([label, count, href]) => <a key={label as string} href={href as string} className="rounded-lg border p-4 transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><strong className="block text-2xl">{count}</strong><span className="mt-1 block text-sm text-muted-foreground">{label}</span></a>)}</CardContent></Card>}
    <section className="mt-6 grid gap-6 lg:grid-cols-5"><Card className="lg:col-span-3"><CardHeader><CardTitle>Durchsatz</CardTitle><CardDescription>Erstellt und abgeschlossen in den letzten 14 Tagen.</CardDescription></CardHeader><CardContent className="h-72"><MeasuredChart><LineChart data={series}><CartesianGrid vertical={false} strokeDasharray="3 3" /><XAxis dataKey="day" tickLine={false} axisLine={false} /><YAxis allowDecimals={false} tickLine={false} axisLine={false} /><Tooltip /><Line type="monotone" dataKey="created" stroke="var(--chart-1)" strokeWidth={2} dot={false} /><Line type="monotone" dataKey="completed" stroke="var(--chart-2)" strokeWidth={2} dot={false} /></LineChart></MeasuredChart></CardContent></Card><RunStatus runs={data.Runs} /></section>
    <section className="mt-6 grid gap-6 lg:grid-cols-5"><Card className="lg:col-span-3"><CardHeader><CardTitle>Kostenverlauf</CardTitle><CardDescription>Istkosten und Schätzungen pro Tag im gewählten Zeitraum.</CardDescription></CardHeader><CardContent className="h-72"><MeasuredChart><LineChart data={data.TelemetrySeries ?? []}><CartesianGrid vertical={false} strokeDasharray="3 3" /><XAxis dataKey="Day" tickLine={false} axisLine={false} /><YAxis tickLine={false} axisLine={false} tickFormatter={(value) => money(value)} /><Tooltip formatter={(value, name) => [money(Number(value)), name === "ActualMicrousd" ? "Istkosten" : "Schätzung"]} /><Line type="monotone" dataKey="ActualMicrousd" stroke="var(--chart-2)" strokeWidth={2} dot={false} /><Line type="monotone" dataKey="EstimatedMicrousd" stroke="var(--chart-1)" strokeWidth={2} dot={false} /></LineChart></MeasuredChart></CardContent></Card><Card><CardHeader><CardTitle>Tokenverlauf</CardTitle><CardDescription>Gesamter gemeldeter oder historischer Umfang.</CardDescription></CardHeader><CardContent className="h-72"><MeasuredChart><LineChart data={data.TelemetrySeries ?? []}><CartesianGrid vertical={false} strokeDasharray="3 3" /><XAxis dataKey="Day" hide /><YAxis allowDecimals={false} tickLine={false} axisLine={false} /><Tooltip /><Line type="monotone" dataKey="Tokens" stroke="var(--chart-3)" strokeWidth={2} dot={false} /></LineChart></MeasuredChart></CardContent></Card></section>
    <section className="mt-6 grid gap-6 lg:grid-cols-5"><Card className="lg:col-span-3"><CardHeader><CardTitle>Arbeit im Workflow</CardTitle><CardDescription>Aktive Aufgaben nach Board und Spalte.</CardDescription></CardHeader><CardContent className="h-64"><MeasuredChart><BarChart data={data.ByColumn}><CartesianGrid vertical={false} strokeDasharray="3 3" /><XAxis dataKey="Name" hide /><YAxis allowDecimals={false} tickLine={false} axisLine={false} /><Tooltip /><Bar dataKey="Count" fill="var(--chart-1)" radius={[5, 5, 0, 0]} /></BarChart></MeasuredChart></CardContent></Card><Notifications items={data.Notifications} /></section>
    <Card className="mt-6"><CardHeader><CardTitle>Usage nach Dimension</CardTitle><CardDescription>Tokenumfang sowie Istkosten und Schätzungen im gewählten Zeitraum.</CardDescription></CardHeader><CardContent><div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead><tr className="border-b"><th className="py-2 pr-4">Dimension</th><th className="py-2 pr-4">Name</th><th className="py-2 pr-4">Tokens</th><th className="py-2 pr-4">Istkosten</th><th className="py-2">Schätzung</th></tr></thead><tbody>{dimensions.map((item) => <tr key={`${item.Dimension}-${item.Name}`} className="border-b last:border-0"><td className="py-2 pr-4 text-muted-foreground">{item.Dimension}</td><td className="py-2 pr-4">{item.Name}</td><td className="py-2 pr-4">{item.Tokens.toLocaleString("de-DE")}</td><td className="py-2 pr-4">{money(item.ActualMicrousd)}</td><td className="py-2">{money(item.EstimatedMicrousd)}</td></tr>)}</tbody></table></div></CardContent></Card>
  </>;
}
