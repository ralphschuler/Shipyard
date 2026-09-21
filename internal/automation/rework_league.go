package automation

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"taskboard/internal/domain"
)

const (
	reworkLeagueRunLimit        = 10
	reworkLeagueBriefLimit      = 180
	reworkLeagueFindingLimit    = 200
	reworkLeagueMaxFindings     = 8
	reworkLeagueFallbackNotes   = 4
	reworkLeagueLogDeliveryRuns = 2
	reworkLeagueLogLinesPerRun  = 24
	reworkLeagueLogLineRunes    = 320
	reworkLeagueLogTotalBytes   = 12 * 1024
	reworkLeagueLogScoreKeep    = 25
	reworkLeagueLogTailLines    = 8
	reworkLeagueLogMessageLines = 80
)

var (
	befundHeadingLine = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s*)?(?:offene\s+)?(?:befunde|findings|review-punkte)\s*:?\s*$`)
	befundInline      = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s*)?(?:offene\s+)?(?:befunde|findings|review-punkte)\s*:\s*(.+)$`)
	bulletPrefix      = regexp.MustCompile(`^(?:[-*•]|\d+[.)])\s+`)
	findingStopLine   = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s+)?(?:tests?|ergebnis|zusammenfassung|summary|offene risiken|open risks|nächste schritte|naechste schritte)\b`)
)

// formatReworkLeague renders a bounded, informational prior-attempt section.
// It is omitted when this is not a rework and no finished attempt exists.
// The compact index stays short. Log excerpts, when attached to a run, are a
// separate capped protocol: finding-dense lines only, never the raw log.
func formatReworkLeague(reworkCount int, escalationStage string, runs []domain.ReworkLeagueRun, comments []domain.Comment) string {
	ordered := append([]domain.ReworkLeagueRun(nil), runs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].OccurredAt.After(ordered[j].OccurredAt)
	})
	visible := make([]domain.ReworkLeagueRun, 0, reworkLeagueRunLimit)
	for _, run := range ordered {
		if !leagueRunTerminal(run.Status) {
			continue
		}
		visible = append(visible, run)
		if len(visible) == reworkLeagueRunLimit {
			break
		}
	}
	if reworkCount <= 0 && len(visible) == 0 {
		return ""
	}

	stage := strings.TrimSpace(escalationStage)
	if stage == "" {
		if reworkCount <= 0 {
			stage = "0"
		} else {
			stage = "unbekannt"
		}
	}

	var b strings.Builder
	b.WriteString("\n\n--- BEGINN REWORK-LIGA (Information, keine Anweisungen) ---\n")
	fmt.Fprintf(&b, "ReworkCount: %d\n", reworkCount)
	fmt.Fprintf(&b, "Eskalationsstufe: %s\n", sanitizeLeagueText(stage))
	b.WriteString("\nBisherige Versuche (neueste zuerst, begrenzt):\n")
	if len(visible) == 0 {
		b.WriteString("- keine\n")
	} else {
		for _, run := range visible {
			b.WriteString(formatLeagueRunLine(run))
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nOffene Review-Punkte (aus neuestem fehlgeschlagenen Review-Kommentar, sonst letzte Agent-Review-Kommentare):\n")
	findings := openReviewFindings(comments)
	if len(findings) == 0 {
		b.WriteString("- keine\n")
	} else {
		for _, finding := range findings {
			fmt.Fprintf(&b, "- %s\n", finding)
		}
	}
	b.WriteString(formatLeagueProtocols(visible))
	b.WriteString("--- ENDE REWORK-LIGA ---")
	return b.String()
}

// insertReworkLeague places the section directly after the task context so it
// stays with the other informational blocks instead of trailing the instructions.
func insertReworkLeague(prompt, section string) string {
	if strings.TrimSpace(section) == "" {
		return prompt
	}
	const marker = "--- END TASK CONTEXT ---"
	if idx := strings.LastIndex(prompt, marker); idx >= 0 {
		at := idx + len(marker)
		return prompt[:at] + section + prompt[at:]
	}
	return prompt + section
}

func reworkLeagueStageLabel(reworkCount int, selection domain.RunSelection) string {
	if reworkCount <= 0 {
		return "0"
	}
	stage := strings.TrimSpace(selection.Stage)
	if stage == "" {
		stage = fmt.Sprintf("%d", reworkCount)
	}
	model := strings.TrimSpace(selection.Model)
	effort := strings.TrimSpace(selection.Effort)
	if model != "" && effort != "" {
		return fmt.Sprintf("%s/%s (Stufe %s)", model, effort, stage)
	}
	if model != "" {
		return fmt.Sprintf("%s (Stufe %s)", model, stage)
	}
	return stage
}

func formatLeagueRunLine(run domain.ReworkLeagueRun) string {
	role := reworkLeagueRole(run.AgentName)
	when := "—"
	if !run.OccurredAt.IsZero() {
		when = run.OccurredAt.UTC().Format(time.RFC3339)
	}
	model := leagueModelEffort(run.Model, run.Effort)
	brief := leagueBrief(run)
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "—"
	}
	if role == "Review" {
		return fmt.Sprintf("- [%s] Review | %s | %s | %s", when, model, status, brief)
	}
	gate := strings.TrimSpace(run.GateStatus)
	if gate == "" {
		gate = "—"
	}
	applied := "nein"
	if run.Applied {
		applied = "ja"
	}
	return fmt.Sprintf("- [%s] %s | %s | %s/%s | applied=%s | %s", when, role, model, status, gate, applied, brief)
}

func reworkLeagueRole(agentName string) string {
	if isReviewAgent(agentName) {
		return "Review"
	}
	if isDeliveryAgent(agentName) {
		return "Delivery"
	}
	name := strings.TrimSpace(agentName)
	if name == "" {
		return "Agent"
	}
	return truncateLeagueText(name, 40)
}

func isDeliveryAgent(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "delivery agent" || strings.HasPrefix(n, "delivery agent ")
}

func leagueModelEffort(model, effort string) string {
	model = strings.TrimSpace(model)
	effort = strings.TrimSpace(effort)
	switch {
	case model != "" && effort != "":
		return model + "/" + effort
	case model != "":
		return model
	case effort != "":
		return effort
	default:
		return "—"
	}
}

func leagueBrief(run domain.ReworkLeagueRun) string {
	text := strings.TrimSpace(run.Summary)
	if text == "" {
		text = strings.TrimSpace(run.ErrorMessage)
	}
	text = truncateLeagueText(text, reworkLeagueBriefLimit)
	if text == "" {
		return "—"
	}
	return text
}

func leagueRunTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func openReviewFindings(comments []domain.Comment) []string {
	for i := len(comments) - 1; i >= 0; i-- {
		comment := comments[i]
		if !looksLikeReviewComment(comment) {
			continue
		}
		if looksPassedReview(comment.Body) {
			return nil
		}
		if looksFailed(comment.Body) {
			if items := extractBefunde(comment.Body); len(items) > 0 {
				return capFindings(items)
			}
			break
		}
	}
	return capFindings(fallbackAgentReviewLines(comments))
}

func fallbackAgentReviewLines(comments []domain.Comment) []string {
	var lines []string
	notes := 0
	for i := len(comments) - 1; i >= 0 && notes < reworkLeagueFallbackNotes; i-- {
		comment := comments[i]
		if !isAgentReviewComment(comment) || looksPassedReview(comment.Body) {
			continue
		}
		notes++
		if items := extractBefunde(comment.Body); len(items) > 0 {
			lines = append(lines, items...)
		} else if line := truncateLeagueText(comment.Body, reworkLeagueFindingLimit); line != "" {
			lines = append(lines, line)
		}
		if len(lines) > reworkLeagueMaxFindings {
			break
		}
	}
	return lines
}

func isAgentReviewComment(comment domain.Comment) bool {
	if looksLikeReviewComment(comment) {
		return true
	}
	author := strings.ToLower(strings.TrimSpace(comment.Author))
	if author != "agent" && !strings.Contains(author, "review") {
		return false
	}
	body := strings.ToLower(comment.Body)
	return strings.Contains(body, "befund") || strings.Contains(body, "finding")
}

func looksLikeReviewComment(comment domain.Comment) bool {
	author := strings.ToLower(strings.TrimSpace(comment.Author))
	body := strings.ToLower(comment.Body)
	if strings.Contains(author, "review") {
		return true
	}
	if strings.Contains(body, "befunde") || strings.Contains(body, "findings") {
		return true
	}
	if author == "agent" || strings.Contains(author, "agent") {
		switch {
		case strings.Contains(body, "code review"),
			strings.Contains(body, "review agent"),
			strings.Contains(body, "review nicht"),
			strings.Contains(body, "review failed"),
			strings.Contains(body, "review bestanden"),
			strings.Contains(body, "self-review"):
			return true
		}
	}
	return false
}

func looksFailed(body string) bool {
	lower := foldReviewNegations(strings.ToLower(body))
	for _, phrase := range []string{
		"nicht bestanden", "nicht erfüllt", "nicht erfuellt",
		"fehlgeschlagen", "abgelehnt",
		"status=failed", "status: failed", "status = failed",
		"review failed", "self-review failed",
		"unmet", "rejected", "mängel", "maengel",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return strings.Contains(lower, "failed")
}

func looksPassedReview(body string) bool {
	if looksFailed(body) {
		return false
	}
	lower := strings.ToLower(body)
	for _, phrase := range []string{
		"bestanden", "status=passed", "status: passed", "review passed",
		"keine befunde", "no findings",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func foldReviewNegations(lower string) string {
	replacer := strings.NewReplacer(
		"keine mängel", "",
		"keine maengel", "",
		"ohne mängel", "",
		"ohne maengel", "",
		"keine befunde", "",
		"no findings", "",
		"no failed", "",
		"not failed", "",
		"0 failed", "",
		"keine fehler", "",
	)
	return replacer.Replace(lower)
}

func extractBefunde(body string) []string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if befundHeadingLine.MatchString(line) {
			return collectFindingLines(lines[i+1:])
		}
		if match := befundInline.FindStringSubmatch(line); len(match) == 2 {
			items := splitInlineFindings(match[1])
			return append(items, collectFindingLines(lines[i+1:])...)
		}
	}
	return nil
}

func collectFindingLines(lines []string) []string {
	var items []string
	started := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if started {
				break
			}
			continue
		}
		if findingStopLine.MatchString(trimmed) {
			break
		}
		text := trimmed
		if stripped, ok := stripBullet(trimmed); ok {
			text = stripped
		}
		text = truncateLeagueText(text, reworkLeagueFindingLimit)
		if text == "" {
			continue
		}
		items = append(items, text)
		started = true
		if len(items) > reworkLeagueMaxFindings {
			break
		}
	}
	return items
}

func splitInlineFindings(rest string) []string {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return nil
	}
	parts := regexp.MustCompile(`\s+[;•]\s+|\s+-\s+`).Split(rest, -1)
	var items []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if stripped, ok := stripBullet(part); ok {
			part = stripped
		}
		part = truncateLeagueText(part, reworkLeagueFindingLimit)
		if part == "" {
			continue
		}
		items = append(items, part)
	}
	return items
}

func stripBullet(line string) (string, bool) {
	if prefix := bulletPrefix.FindString(line); prefix != "" {
		return strings.TrimSpace(line[len(prefix):]), true
	}
	return "", false
}

func capFindings(items []string) []string {
	if len(items) <= reworkLeagueMaxFindings {
		return items
	}
	capped := append([]string{}, items[:reworkLeagueMaxFindings]...)
	return append(capped, "…")
}

func truncateLeagueText(value string, limit int) string {
	value = sanitizeLeagueText(value)
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	if limit == 1 {
		return "…"
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

func sanitizeLeagueText(value string) string {
	value = strings.ReplaceAll(value, "\u0000", "")
	value = strings.NewReplacer(
		"\r\n", " ",
		"\n", " ",
		"\r", " ",
		"--- BEGINN REWORK-LIGA", " ",
		"--- ENDE REWORK-LIGA", " ",
		"--- BEGIN TASK CONTEXT", " ",
		"--- END TASK CONTEXT", " ",
		"--- SHIPYARD PLATFORM RULES", " ",
		"--- END PLATFORM RULES", " ",
	).Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

type leagueLogLine struct {
	seq, sub, score int
	level, text     string
}

// reworkLeagueLogIDs selects the attempts whose durable logs a Delivery rework
// prompt may quote: the latest failed Review, then the latest Delivery runs.
func reworkLeagueLogIDs(runs []domain.ReworkLeagueRun) []string {
	sources := leagueLogSources(runs)
	ids := make([]string, 0, len(sources))
	for _, run := range sources {
		id := strings.TrimSpace(run.ID)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func attachReworkLeagueLogs(runs []domain.ReworkLeagueRun, byID map[string][]domain.RunLog) []domain.ReworkLeagueRun {
	if len(byID) == 0 {
		return runs
	}
	wanted := map[string]struct{}{}
	for _, id := range reworkLeagueLogIDs(runs) {
		wanted[id] = struct{}{}
	}
	for i := range runs {
		if _, ok := wanted[runs[i].ID]; !ok {
			continue
		}
		runs[i].Logs = byID[runs[i].ID]
	}
	return runs
}

// leagueLogSources picks at most one Review attempt and the latest Delivery
// attempts. A newer passed Review suppresses older Review logs.
func leagueLogSources(runs []domain.ReworkLeagueRun) []domain.ReworkLeagueRun {
	ordered := append([]domain.ReworkLeagueRun(nil), runs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].OccurredAt.After(ordered[j].OccurredAt)
	})
	var review *domain.ReworkLeagueRun
	deliveries := make([]domain.ReworkLeagueRun, 0, reworkLeagueLogDeliveryRuns)
	passedReview := false
	for _, run := range ordered {
		if !leagueRunTerminal(run.Status) {
			continue
		}
		switch reworkLeagueRole(run.AgentName) {
		case "Review":
			if review != nil || passedReview {
				continue
			}
			blob := run.Summary + "\n" + run.ErrorMessage
			if looksPassedReview(blob) && !leagueStatusFailed(run.Status) {
				passedReview = true
				continue
			}
			copied := run
			review = &copied
		case "Delivery":
			if len(deliveries) >= reworkLeagueLogDeliveryRuns {
				continue
			}
			deliveries = append(deliveries, run)
		}
	}
	sources := make([]domain.ReworkLeagueRun, 0, reworkLeagueLogDeliveryRuns+1)
	if review != nil {
		sources = append(sources, *review)
	}
	sources = append(sources, deliveries...)
	return sources
}

func leagueStatusFailed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "cancelled":
		return true
	default:
		return false
	}
}

func formatLeagueProtocols(runs []domain.ReworkLeagueRun) string {
	sources := leagueLogSources(runs)
	if len(sources) == 0 {
		return ""
	}
	var blocks []string
	used := 0
	cut := false
	for _, run := range sources {
		if len(run.Logs) == 0 {
			continue
		}
		remaining := reworkLeagueLogTotalBytes - used
		if remaining < 80 {
			cut = true
			break
		}
		block, shortened := formatOneLeagueProtocol(run, remaining)
		if block == "" {
			cut = true
			break
		}
		blocks = append(blocks, block)
		used += len(block)
		if shortened {
			cut = true
			break
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nRun-Protokolle (Auszug aus den Run-Logs, fehlerdichte Zeilen, begrenzt):\n")
	for _, block := range blocks {
		b.WriteString(block)
	}
	if cut {
		b.WriteString("… Protokoll gekürzt …\n")
	}
	return b.String()
}

func formatOneLeagueProtocol(run domain.ReworkLeagueRun, budget int) (string, bool) {
	lines, omitted := selectLeagueLogLines(run.Logs)
	header := formatLeagueProtocolHeader(run)
	if budget < len(header)+1 {
		return "", true
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	budget -= len(header) + 1
	shortened := false
	if len(lines) == 0 {
		note := "  — keine fehlerdichten Zeilen —\n"
		if len(note) > budget {
			return b.String(), true
		}
		b.WriteString(note)
		return b.String(), false
	}
	written := 0
	for _, line := range lines {
		rendered := "  [" + leagueLogLevel(line.level) + "] " + truncateLeagueText(line.text, reworkLeagueLogLineRunes) + "\n"
		if len(rendered) > budget {
			shortened = true
			break
		}
		b.WriteString(rendered)
		budget -= len(rendered)
		written++
	}
	if written < len(lines) {
		shortened = true
		omitted += len(lines) - written
	}
	if omitted > 0 {
		note := fmt.Sprintf("  … %d Zeilen ausgelassen …\n", omitted)
		if len(note) <= budget {
			b.WriteString(note)
		} else {
			shortened = true
		}
	}
	return b.String(), shortened
}

func formatLeagueProtocolHeader(run domain.ReworkLeagueRun) string {
	role := reworkLeagueRole(run.AgentName)
	when := "—"
	if !run.OccurredAt.IsZero() {
		when = run.OccurredAt.UTC().Format(time.RFC3339)
	}
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "—"
	}
	header := fmt.Sprintf("Protokoll %s | %s | %s | %s", role, leagueModelEffort(run.Model, run.Effort), status, when)
	if id := shortRunID(run.ID); id != "" {
		header += " | run " + id
	}
	return header
}

func shortRunID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func leagueLogLevel(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "error", "warning", "info":
		return level
	case "":
		return "info"
	default:
		return truncateLeagueText(level, 16)
	}
}

func selectLeagueLogLines(logs []domain.RunLog) ([]leagueLogLine, int) {
	all := expandLeagueLogs(logs)
	var signal []leagueLogLine
	for _, line := range all {
		if line.score >= reworkLeagueLogScoreKeep {
			signal = append(signal, line)
		}
	}
	omitted := 0
	chosen := signal
	if len(chosen) == 0 {
		var tail []leagueLogLine
		for _, line := range all {
			if line.score > -40 {
				tail = append(tail, line)
			}
		}
		if len(tail) > reworkLeagueLogTailLines {
			omitted = len(tail) - reworkLeagueLogTailLines
			tail = tail[len(tail)-reworkLeagueLogTailLines:]
		}
		return tail, omitted
	}
	if len(chosen) > reworkLeagueLogLinesPerRun {
		omitted = len(chosen) - reworkLeagueLogLinesPerRun
		sort.SliceStable(chosen, func(i, j int) bool {
			if chosen[i].score != chosen[j].score {
				return chosen[i].score > chosen[j].score
			}
			if chosen[i].seq != chosen[j].seq {
				return chosen[i].seq > chosen[j].seq
			}
			return chosen[i].sub > chosen[j].sub
		})
		chosen = append([]leagueLogLine(nil), chosen[:reworkLeagueLogLinesPerRun]...)
	}
	sort.SliceStable(chosen, func(i, j int) bool {
		if chosen[i].seq != chosen[j].seq {
			return chosen[i].seq < chosen[j].seq
		}
		return chosen[i].sub < chosen[j].sub
	})
	return chosen, omitted
}

func expandLeagueLogs(logs []domain.RunLog) []leagueLogLine {
	ordered := append([]domain.RunLog(nil), logs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Sequence < ordered[j].Sequence
	})
	var lines []leagueLogLine
	for _, entry := range ordered {
		parts := strings.Split(strings.ReplaceAll(entry.Message, "\r\n", "\n"), "\n")
		parts = condenseMessageLines(parts, entry.Level)
		sub := 0
		for _, part := range parts {
			text := strings.TrimSpace(part)
			if text == "" {
				continue
			}
			lines = append(lines, leagueLogLine{
				seq:   entry.Sequence,
				sub:   sub,
				level: entry.Level,
				text:  text,
				score: leagueLogScore(entry.Level, text),
			})
			sub++
		}
	}
	return lines
}

func condenseMessageLines(parts []string, level string) []string {
	if len(parts) <= reworkLeagueLogMessageLines {
		return parts
	}
	type candidate struct {
		index int
		text  string
		score int
	}
	var kept []candidate
	for i, part := range parts {
		text := strings.TrimSpace(part)
		if text == "" {
			continue
		}
		score := leagueLogScore(level, text)
		if score >= reworkLeagueLogScoreKeep {
			kept = append(kept, candidate{index: i, text: text, score: score})
		}
	}
	start := len(parts) - 20
	if start < 0 {
		start = 0
	}
	for i := start; i < len(parts); i++ {
		text := strings.TrimSpace(parts[i])
		if text == "" {
			continue
		}
		kept = append(kept, candidate{index: i, text: text, score: leagueLogScore(level, text)})
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return kept[i].index < kept[j].index
	})
	deduped := make([]string, 0, len(kept))
	last := -1
	for _, item := range kept {
		if item.index == last {
			continue
		}
		last = item.index
		deduped = append(deduped, item.text)
	}
	if len(deduped) == 0 {
		return parts[:reworkLeagueLogMessageLines]
	}
	if len(deduped) > reworkLeagueLogMessageLines {
		deduped = deduped[len(deduped)-reworkLeagueLogMessageLines:]
	}
	return deduped
}

func leagueLogScore(level, line string) int {
	lower := strings.ToLower(line)
	score := 0
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error":
		score += 60
	case "warning":
		score += 35
	}
	for _, signal := range []struct {
		phrase string
		points int
	}{
		{"befunde", 50},
		{"befund", 40},
		{"finding", 35},
		{"nicht bestanden", 45},
		{"nicht erfüllt", 40},
		{"nicht erfuellt", 40},
		{"fehlgeschlagen", 45},
		{"qualitäts-gate", 45},
		{"qualitaets-gate", 45},
		{"panic", 45},
		{"test failed", 40},
		{"fail:", 30},
		{"failed", 25},
		{"fail", 25},
		{"exception", 30},
		{"error", 15},
		{"gate", 12},
	} {
		if strings.Contains(lower, signal.phrase) {
			score += signal.points
		}
	}
	for _, noise := range []string{
		"codex-agent gestartet",
		"sandbox-profil",
		"isolierter git-worktree",
		"memory-kontext",
		"kein .devcontainer",
		"projekt-checkout aktualisiert",
		"task-branch vor delivery",
	} {
		if strings.Contains(lower, noise) {
			score -= 80
		}
	}
	if len([]rune(line)) > 400 && score < 40 {
		score -= 30
	}
	return score
}
