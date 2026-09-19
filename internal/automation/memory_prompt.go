package automation

import (
	"fmt"
	"sort"
	"strings"
	"taskboard/internal/memory"
)

// formatMemoryContext is intentionally a prompt-only projection. It keeps the
// three memory kinds visibly separate and carries compact provenance so an
// agent can distinguish a remembered statement from current task context.
func formatMemoryContext(pack memory.ContextPack) string {
	if len(pack.Items) == 0 {
		return ""
	}
	items := append([]memory.RetrievalItem(nil), pack.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		order := func(kind string) int {
			switch kind {
			case "conversation":
				return 0
			case "fact":
				return 1
			case "graph":
				return 2
			default:
				return 3
			}
		}
		return order(items[i].Kind) < order(items[j].Kind)
	})

	var b strings.Builder
	b.WriteString("\n\n--- MEMORY CONTEXT (scoped; read-only reference) ---\n")
	b.WriteString(fmt.Sprintf("retrieval=%s · tokens=%d/%d · truncated=%t\n", pack.ID, pack.UsedTokens, pack.TokenBudget, pack.Truncated))
	last := ""
	for _, item := range items {
		kind := strings.ToLower(strings.TrimSpace(item.Kind))
		if kind == "" {
			kind = "other"
		}
		section := strings.ToUpper(kind)
		switch kind {
		case "conversation":
			section = "CONVERSATIONS"
		case "fact":
			section = "CONFIRMED FACTS"
		case "graph":
			section = "GRAPH RELATIONSHIPS"
		}
		if kind != last {
			b.WriteString("\n[" + section + "]\n")
			last = kind
		}
		text := memory.Redact(strings.TrimSpace(item.Text))
		if text == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s: %s (source message=%s run=%s task=%s agent=%s at=%s", item.ID, text, item.Source.MessageID, item.Source.RunID, item.Source.TaskID, item.Source.AgentID, item.Source.OccurredAt.UTC().Format("2006-01-02T15:04:05Z")))
		if item.Confidence > 0 {
			b.WriteString(fmt.Sprintf(" confidence=%.2f", item.Confidence))
		}
		b.WriteString(")\n")
	}
	b.WriteString("--- END MEMORY CONTEXT ---")
	return b.String()
}
