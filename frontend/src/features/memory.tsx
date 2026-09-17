import { useEffect, useMemo, useState } from "react";
import { BrainCircuit, Check, Search, Trash2, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

type MemoryItem = { ID: string; Kind: string; Text: string; Confidence?: number; Source?: { MessageID: string; RunID: string; OccurredAt: string } };
type MemoryResponse = { messages: MemoryItem[]; context: { items: MemoryItem[]; usedTokens: number; tokenBudget: number; truncated: boolean } };

function csrf() {
  return document.cookie.split("; ").find((value) => value.startsWith("taskboard_csrf="))?.split("=").slice(1).join("=") ?? "";
}

export default function Memory() {
  const [scope, setScope] = useState(() => ({ tenant_id: localStorage.getItem("memory-tenant") ?? "", project_id: localStorage.getItem("memory-project") ?? "", task_id: localStorage.getItem("memory-task") ?? "", agent_id: localStorage.getItem("memory-agent") ?? "" }));
  const [query, setQuery] = useState("");
  const [data, setData] = useState<MemoryResponse>();
  const [error, setError] = useState("");
  const [fact, setFact] = useState({ subject: "", predicate: "", object: "", message_id: "", run_id: "" });
  const params = useMemo(() => new URLSearchParams({ ...scope, q: query, budget: "1200" }), [scope, query]);
  const load = () => fetch(`/api/v1/memory?${params}`, { credentials: "same-origin" }).then((r) => r.ok ? r.json() : Promise.reject(new Error("Scope unvollständig oder Memory nicht verfügbar."))).then(setData).catch((e) => setError(e.message));
  useEffect(() => { if (Object.values(scope).every(Boolean)) void load(); }, [params.toString()]);
  const setScopeValue = (key: keyof typeof scope, value: string) => { setScope((old) => ({ ...old, [key]: value })); localStorage.setItem(`memory-${key.replace("_id", "")}`, value); setError(""); };
  const status = async (id: string, value: "confirmed" | "revoked") => {
    const response = await fetch(`/api/v1/memory/facts/${id}/status?${params}`, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf() }, body: JSON.stringify({ status: value }) });
    if (!response.ok) setError(await response.text()); else void load();
  };
  const createFact = async (event: React.FormEvent) => {
    event.preventDefault();
    try {
      const response = await fetch(`/api/v1/memory/facts?${params}`, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf() }, body: JSON.stringify({ ...fact, object: JSON.parse(fact.object), valid_from: new Date().toISOString(), change_reason: "UI-Korrektur" }) });
      if (!response.ok) throw new Error(await response.text());
      setFact({ subject: "", predicate: "", object: "", message_id: "", run_id: "" }); void load();
    } catch (e) { setError(e instanceof Error ? e.message : "Fact konnte nicht gespeichert werden."); }
  };
  const remove = async () => { if (!window.confirm("Alle Memory-Daten dieses Scopes löschen?")) return; const response = await fetch(`/api/v1/memory?${params}`, { method: "DELETE", credentials: "same-origin", headers: { "X-CSRF-Token": csrf() } }); if (!response.ok) setError(await response.text()); else void load(); };
  const items = data?.context?.items ?? [];
  return <div className="space-y-6">
    <Card className="border-primary/30 bg-primary/[0.03]"><CardHeader><div className="flex items-start gap-3"><BrainCircuit className="mt-1 size-5 text-primary" /><div><CardTitle>Memory prüfen</CardTitle><CardDescription>Jede Erinnerung bleibt an Scope, Nachricht und Run gebunden.</CardDescription></div></div></CardHeader><CardContent className="grid gap-3 md:grid-cols-4">{(Object.keys(scope) as (keyof typeof scope)[]).map((key) => <Input key={key} aria-label={key} placeholder={key.replace("_id", " ID")} value={scope[key]} onChange={(e) => setScopeValue(key, e.target.value)} />)}<div className="flex gap-2 md:col-span-4"><Input aria-label="Memory durchsuchen" placeholder="Nach Inhalt suchen …" value={query} onChange={(e) => setQuery(e.target.value)} /><Button onClick={() => void load()}><Search className="mr-2 size-4" />Suchen</Button><Button variant="destructive" onClick={() => void remove()}><Trash2 className="mr-2 size-4" />Scope löschen</Button></div></CardContent></Card>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    <section className="grid gap-6 lg:grid-cols-[1.4fr_0.8fr]"><Card><CardHeader><CardTitle>Kontext-Pack</CardTitle><CardDescription>{data ? `${items.length} Treffer · ${data.context.usedTokens ?? 0}/${data.context.tokenBudget ?? 0} Tokens` : "Scope ausfüllen, um Erinnerungen zu laden."}</CardDescription></CardHeader><CardContent className="space-y-3">{items.length ? items.map((item) => <article key={item.ID} className="rounded-lg border-l-4 border-primary bg-muted/40 p-4"><div className="flex items-start justify-between gap-3"><p className="text-sm leading-6">{item.Text}</p>{item.Kind === "fact" && <div className="flex shrink-0 gap-1"><Button size="icon" variant="ghost" aria-label="Fact bestätigen" onClick={() => void status(item.ID, "confirmed")}><Check className="size-4 text-emerald-600" /></Button><Button size="icon" variant="ghost" aria-label="Fact widerrufen" onClick={() => void status(item.ID, "revoked")}><X className="size-4 text-destructive" /></Button></div>}</div>{item.Source && <p className="mt-2 text-xs text-muted-foreground">Quelle: {item.Source.MessageID} · Run {item.Source.RunID} · {new Date(item.Source.OccurredAt).toLocaleString()}</p>}</article>) : <p className="py-10 text-center text-sm text-muted-foreground">Keine passenden Erinnerungen.</p>}</CardContent></Card>
      <Card><CardHeader><CardTitle>Fact korrigieren</CardTitle><CardDescription>Neue Version mit Provenance anlegen.</CardDescription></CardHeader><CardContent><form className="space-y-3" onSubmit={createFact}><Input required placeholder="Subjekt" value={fact.subject} onChange={(e) => setFact({ ...fact, subject: e.target.value })} /><Input required placeholder="Prädikat" value={fact.predicate} onChange={(e) => setFact({ ...fact, predicate: e.target.value })} /><Input required placeholder={'Objekt als JSON, z. B. "Berlin"'} value={fact.object} onChange={(e) => setFact({ ...fact, object: e.target.value })} /><Input required placeholder="Nachrichten-ID" value={fact.message_id} onChange={(e) => setFact({ ...fact, message_id: e.target.value })} /><Input required placeholder="Run-ID" value={fact.run_id} onChange={(e) => setFact({ ...fact, run_id: e.target.value })} /><Button type="submit" className="w-full">Fact speichern</Button></form><div className="mt-4 flex items-center gap-2 text-xs text-muted-foreground"><Badge variant="outline">append-only</Badge><span>Änderungen bleiben historisch nachvollziehbar.</span></div></CardContent></Card></section>
  </div>;
}
