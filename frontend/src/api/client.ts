import { useEffect, useState } from "react";

export type LiveChange = {
  table?: string;
  action?: string;
  id?: string;
  run_id?: string;
};

export function refreshData(change: LiveChange = {}) {
  window.dispatchEvent(
    new CustomEvent<LiveChange>("taskboard:data-change", { detail: change }),
  );
}

export function endpointUsesChange(endpoint: string, change: LiveChange) {
  const table = change.table;
  if (!table) return true;
  const matches = (...tables: string[]) => tables.includes(table);
  if (endpoint.includes("/settings/appearance")) return matches("workspace_preferences");
  if (endpoint.includes("/settings/providers")) return matches("provider_settings");
  if (endpoint.includes("/settings/agent-policy")) return matches("agent_prompt_policy");
  if (endpoint.includes("/settings/integrations")) return matches("integration_connections");
  if (endpoint.includes("/account")) return matches("api_tokens", "users");
  if (endpoint.includes("/board-templates")) return false;
  if (endpoint.includes("/skills")) return matches("skill_sources", "skills", "installed_skills", "agent_skills");
  if (endpoint.includes("/agents")) return matches("agents", "agent_skills", "installed_skills", "skills");
  if (endpoint.includes("/automations")) return matches("automation_rules", "automation_events", "agents", "workflow_columns", "labels", "boards");
  if (endpoint.includes("/schedules")) return matches("automation_rules", "agents", "boards");
  if (endpoint.includes("/webhooks")) return matches("webhook_subscriptions", "webhook_deliveries");
  if (endpoint.includes("/projects") || endpoint.includes("/project-groups")) return matches("projects", "board_projects", "project_groups", "project_group_members", "project_sources", "integration_connections");
  if (/^\/api\/v1\/runs\/[^/?]+\/logs(?:\?|$)/.test(endpoint)) return matches("agent_run_logs", "agent_runs");
  if (/^\/api\/v1\/runs\/[^/?]+(?:\?|$)/.test(endpoint)) return matches("agent_runs", "agent_run_batches", "workflow_runs", "workflow_steps", "task_repository_targets", "task_comments", "tasks");
  if (endpoint.includes("/runs")) return matches("agent_runs", "agent_run_batches", "workflow_runs", "workflow_steps", "task_repository_targets", "task_comments", "tasks");
  if (endpoint.includes("/tasks/")) return matches("tasks", "task_comments", "task_labels", "labels", "task_target_projects", "task_target_groups", "task_repository_targets", "agent_interactions", "task_decisions", "task_transitions", "agent_runs", "workflow_columns", "transitions");
  if (endpoint.includes("/boards/")) return matches("boards", "board_projects", "workflow_columns", "transitions", "tasks", "labels", "task_labels", "task_target_projects", "task_target_groups", "task_comments", "agent_runs");
  if (endpoint.includes("/boards")) return matches("boards", "tasks", "workflow_columns");
  if (endpoint.includes("/dashboard")) return matches("boards", "tasks", "agent_runs", "agent_run_batches", "notifications", "automation_events", "agent_interactions", "task_decisions");
  if (endpoint.includes("/audit")) return matches("audit_events");
  if (endpoint.includes("/memory")) return matches("memory_conversations", "memory_facts", "memory_fact_versions", "memory_audit_events");
  return true;
}

export function useAPI<T>(endpoint: string) {
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
      fetch(endpoint, { credentials: "same-origin", signal: controller.signal })
        .then((r) => (r.ok ? r.json() : Promise.reject()))
        .then((value) => {
          if (!stopped) { hasData = true; setData(value); setError(false); }
        })
        .catch((reason) => {
          if (!stopped && reason?.name !== "AbortError" && !hasData) setError(true);
        });
    };
    setData(undefined);
    setError(false);
    timer = window.setTimeout(load, 0);
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
  return document.cookie.split("; ").find((v) => v.startsWith("taskboard_csrf="))?.split("=").slice(1).join("") ?? "";
}

export async function mutation(url: string, init: RequestInit) {
  let body = init.body;
  const headers = new Headers(init.headers);
  if (body instanceof FormData) {
    const encoded = new URLSearchParams();
    body.forEach((value, key) => { if (typeof value === "string") encoded.append(key, value); });
    body = encoded;
    headers.set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8");
  }
  headers.set("X-CSRF-Token", csrf());
  const response = await fetch(url, { ...init, body, credentials: "same-origin", headers });
  if (!response.ok) throw new Error(await response.text());
  if (!response.redirected && response.headers.get("content-type")?.includes("text/html")) {
    const page = new DOMParser().parseFromString(await response.text(), "text/html");
    throw new Error(page.querySelector("[role=alert], .error")?.textContent?.trim() ?? "Änderung konnte nicht gespeichert werden.");
  }
  return response;
}
