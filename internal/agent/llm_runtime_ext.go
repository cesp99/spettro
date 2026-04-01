package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"spettro/internal/config"
	"spettro/internal/mcp"
	"spettro/internal/session"
)

func (r *toolRuntime) runAskUser(rawArgs []byte) (string, error) {
	var args struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("ask-user args: %w", err)
	}
	q := strings.TrimSpace(args.Question)
	if q == "" {
		return "", fmt.Errorf("ask-user: question is required")
	}
	if len(args.Options) == 0 {
		return "USER_INPUT_REQUIRED: " + q, nil
	}
	opts := make([]string, 0, len(args.Options))
	for _, o := range args.Options {
		o = strings.TrimSpace(o)
		if o != "" {
			opts = append(opts, o)
		}
	}
	return fmt.Sprintf("USER_INPUT_REQUIRED: %s\nOptions: %s", q, strings.Join(opts, " | ")), nil
}

func (r *toolRuntime) runTaskCreate(rawArgs []byte) (string, error) {
	var args struct {
		ID      string `json:"id"`
		Content string `json:"content"`
		Status  string `json:"status"`
		Owner   string `json:"owner"`
		Source  string `json:"source"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("task-create args: %w", err)
	}
	if strings.TrimSpace(r.sessionDir) == "" {
		return "", fmt.Errorf("task-create requires an active session")
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		id = fmt.Sprintf("task-%d", time.Now().UnixMilli())
	}
	item := session.Todo{
		ID:      id,
		Content: strings.TrimSpace(args.Content),
		Status:  strings.TrimSpace(args.Status),
		Owner:   strings.TrimSpace(args.Owner),
		Source:  strings.TrimSpace(args.Source),
	}
	if item.Status == "" {
		item.Status = "pending"
	}
	sid := filepath.Base(r.sessionDir)
	out, err := session.UpsertTodo(filepath.Dir(filepath.Dir(r.sessionDir)), sid, item)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(out)
	return string(raw), nil
}

func (r *toolRuntime) runTaskGet(rawArgs []byte) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("task-get args: %w", err)
	}
	if strings.TrimSpace(r.sessionDir) == "" {
		return "", fmt.Errorf("task-get requires an active session")
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return "", fmt.Errorf("task-get: id is required")
	}
	sid := filepath.Base(r.sessionDir)
	item, ok, err := session.GetTodo(filepath.Dir(filepath.Dir(r.sessionDir)), sid, id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("task-get: task %q not found", id)
	}
	raw, _ := json.Marshal(item)
	return string(raw), nil
}

func (r *toolRuntime) runTaskUpdate(rawArgs []byte) (string, error) {
	var args struct {
		ID      string `json:"id"`
		Content string `json:"content"`
		Status  string `json:"status"`
		Owner   string `json:"owner"`
		Source  string `json:"source"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("task-update args: %w", err)
	}
	if strings.TrimSpace(r.sessionDir) == "" {
		return "", fmt.Errorf("task-update requires an active session")
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return "", fmt.Errorf("task-update: id is required")
	}
	sid := filepath.Base(r.sessionDir)
	globalDir := filepath.Dir(filepath.Dir(r.sessionDir))
	prev, ok, err := session.GetTodo(globalDir, sid, id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("task-update: task %q not found", id)
	}
	if strings.TrimSpace(args.Content) != "" {
		prev.Content = strings.TrimSpace(args.Content)
	}
	if strings.TrimSpace(args.Status) != "" {
		prev.Status = strings.TrimSpace(args.Status)
	}
	if strings.TrimSpace(args.Owner) != "" {
		prev.Owner = strings.TrimSpace(args.Owner)
	}
	if strings.TrimSpace(args.Source) != "" {
		prev.Source = strings.TrimSpace(args.Source)
	}
	out, err := session.UpsertTodo(globalDir, sid, prev)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(out)
	return string(raw), nil
}

func (r *toolRuntime) runTaskList(rawArgs []byte) (string, error) {
	var args struct {
		Status string `json:"status"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("task-list args: %w", err)
	}
	if strings.TrimSpace(r.sessionDir) == "" {
		return "", fmt.Errorf("task-list requires an active session")
	}
	sid := filepath.Base(r.sessionDir)
	items, err := session.LoadTodos(filepath.Dir(filepath.Dir(r.sessionDir)), sid)
	if err != nil {
		return "", err
	}
	filter := strings.TrimSpace(args.Status)
	if filter != "" {
		out := make([]session.Todo, 0, len(items))
		for _, t := range items {
			if t.Status == filter {
				out = append(out, t)
			}
		}
		items = out
	}
	raw, _ := json.Marshal(items)
	return string(raw), nil
}

func (r *toolRuntime) runToolSearch(allowed map[string]struct{}, rawArgs []byte) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("tool-search args: %w", err)
	}
	q := strings.ToLower(strings.TrimSpace(args.Query))
	seen := map[string]struct{}{}
	var rows []string
	for id := range allowed {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		spec, hasSpec := r.toolPolicies[id]
		label := id
		risk := "unknown"
		acts := ""
		if hasSpec {
			if strings.TrimSpace(spec.Name) != "" {
				label = spec.Name
			}
			if strings.TrimSpace(spec.RiskLevel) != "" {
				risk = spec.RiskLevel
			}
			acts = strings.Join(spec.PermittedActions, ",")
		}
		hay := strings.ToLower(id + " " + label + " " + acts + " " + risk)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		score := 1
		if strings.Contains(strings.ToLower(id), q) || strings.Contains(strings.ToLower(label), q) {
			score += 3
		}
		if strings.Contains(acts, "search") {
			score++
		}
		rows = append(rows, fmt.Sprintf("%03d | %s | risk=%s | actions=%s", score, id, risk, emptyIfBlank(acts)))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i] > rows[j] })
	if len(rows) == 0 {
		return "no tools matched", nil
	}
	return strings.Join(rows, "\n"), nil
}

func (r *toolRuntime) runWebSearch(ctx context.Context, rawArgs []byte) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("web-search args: %w", err)
	}
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return "", fmt.Errorf("web-search: query is required")
	}
	if err := r.authorizeNetworkAccess(ctx, "web-search", q); err != nil {
		return "", err
	}
	u := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "spettro-web-search/1.0")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("web-search failed: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", fmt.Errorf("web-search read: %w", err)
	}
	out := string(body)
	if args.MaxResults <= 0 {
		args.MaxResults = 10
	}
	lines := extractDuckDuckGoResults(out, args.MaxResults)
	if len(lines) == 0 {
		return truncate(out, 4000), nil
	}
	return strings.Join(lines, "\n"), nil
}

var ddgResultAnchorRE = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
var htmlTagRE = regexp.MustCompile(`(?is)<[^>]+>`)

func extractDuckDuckGoResults(s string, limit int) []string {
	matches := ddgResultAnchorRE.FindAllStringSubmatch(s, -1)
	seen := map[string]struct{}{}
	var out []string
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		href := strings.TrimSpace(html.UnescapeString(m[1]))
		target := resolveDuckDuckGoResultURL(href)
		if target == "" {
			continue
		}
		title := strings.TrimSpace(html.UnescapeString(htmlTagRE.ReplaceAllString(m[2], "")))
		if title == "" {
			title = target
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, fmt.Sprintf("%s — %s", title, target))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func resolveDuckDuckGoResultURL(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	if strings.HasPrefix(href, "/l/?") {
		href = "https://duckduckgo.com" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if strings.Contains(strings.ToLower(u.Host), "duckduckgo.com") {
		encoded := u.Query().Get("uddg")
		if encoded == "" {
			return ""
		}
		decoded, err := url.QueryUnescape(encoded)
		if err != nil {
			return ""
		}
		decoded = strings.TrimSpace(decoded)
		if strings.HasPrefix(decoded, "http://") || strings.HasPrefix(decoded, "https://") {
			return decoded
		}
		return ""
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	return ""
}

func (r *toolRuntime) runMCPListResources(ctx context.Context, rawArgs []byte) (string, error) {
	var args struct {
		ServerID string `json:"server_id"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("mcp-list-resources args: %w", err)
	}
	if err := r.authorizeNetworkAccess(ctx, "mcp-list-resources", emptyIfBlank(args.ServerID)); err != nil {
		return "", err
	}
	rows, err := mcp.ListResources(r.cwd, strings.TrimSpace(args.ServerID))
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(rows)
	return string(raw), nil
}

func (r *toolRuntime) runMCPReadResource(ctx context.Context, rawArgs []byte) (string, error) {
	var args struct {
		ServerID   string `json:"server_id"`
		ResourceID string `json:"resource_id"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("mcp-read-resource args: %w", err)
	}
	if strings.TrimSpace(args.ServerID) == "" || strings.TrimSpace(args.ResourceID) == "" {
		return "", fmt.Errorf("mcp-read-resource: server_id and resource_id are required")
	}
	if err := r.authorizeNetworkAccess(ctx, "mcp-read-resource", args.ServerID+":"+args.ResourceID); err != nil {
		return "", err
	}
	out, err := mcp.ReadResource(r.cwd, strings.TrimSpace(args.ServerID), strings.TrimSpace(args.ResourceID))
	if err != nil {
		return "", err
	}
	return truncate(out, 12000), nil
}

func (r *toolRuntime) runMCPAuth(ctx context.Context, rawArgs []byte) (string, error) {
	var args struct {
		ServerID    string `json:"server_id"`
		Token       string `json:"token"`
		ExpiresAt   string `json:"expires_at"`
		Description string `json:"description"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("mcp-auth args: %w", err)
	}
	if strings.TrimSpace(args.ServerID) == "" {
		return "", fmt.Errorf("mcp-auth: server_id is required")
	}
	if strings.TrimSpace(args.Token) == "" {
		return "", fmt.Errorf("mcp-auth: token is required")
	}
	if err := r.authorizeNetworkAccess(ctx, "mcp-auth", args.ServerID); err != nil {
		return "", err
	}
	state := mcp.AuthState{
		ServerID:    strings.TrimSpace(args.ServerID),
		Token:       strings.TrimSpace(args.Token),
		UpdatedAt:   time.Now(),
		Description: strings.TrimSpace(args.Description),
	}
	if strings.TrimSpace(args.ExpiresAt) != "" {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(args.ExpiresAt)); err == nil {
			state.ExpiresAt = t
		}
	}
	if err := mcp.SaveAuth(r.cwd, state); err != nil {
		return "", err
	}
	return fmt.Sprintf("mcp auth updated for %s", state.ServerID), nil
}

func (r *toolRuntime) runFileEdit(rawArgs []byte) (string, error) {
	var args struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("file-edit args: %w", err)
	}
	abs, rel, err := r.resolvePath(args.Path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.OldString) == "" {
		return "", fmt.Errorf("file-edit: old_string is required")
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	content := string(raw)
	if !strings.Contains(content, args.OldString) {
		return "", fmt.Errorf("file-edit: old_string not found")
	}
	var updated string
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, args.OldString, args.NewString)
	} else {
		updated = strings.Replace(content, args.OldString, args.NewString, 1)
	}
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return "", err
	}
	r.mu.Lock()
	r.readSet[rel] = struct{}{}
	r.mu.Unlock()
	return fmt.Sprintf("edited %s", rel), nil
}

func (r *toolRuntime) runPlanModeToggle(rawArgs []byte, entering bool) (string, error) {
	var args struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		if entering {
			return "", fmt.Errorf("enter-plan-mode args: %w", err)
		}
		return "", fmt.Errorf("exit-plan-mode args: %w", err)
	}
	mode := "EXITED"
	if entering {
		mode = "ENTERED"
	}
	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		return fmt.Sprintf("PLAN_MODE_%s", mode), nil
	}
	return fmt.Sprintf("PLAN_MODE_%s: %s", mode, reason), nil
}

func (r *toolRuntime) runEnterWorktree(rawArgs []byte) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Branch string `json:"branch"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("enter-worktree args: %w", err)
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return "", fmt.Errorf("enter-worktree: path is required")
	}
	abs, _, err := r.resolvePath(path)
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(args.Branch)
	cmdArgs := []string{"worktree", "add", abs}
	if branch != "" {
		cmdArgs = append(cmdArgs, "-b", branch)
	}
	cmd := exec.Command("git", cmdArgs...)
	cmd.Dir = r.cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return truncate(string(out), 2000), fmt.Errorf("enter-worktree: %w", err)
	}
	return truncate(string(out), 2000), nil
}

func (r *toolRuntime) runExitWorktree(rawArgs []byte) (string, error) {
	var args struct {
		Path  string `json:"path"`
		Force bool   `json:"force"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("exit-worktree args: %w", err)
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return "", fmt.Errorf("exit-worktree: path is required")
	}
	abs, _, err := r.resolvePath(path)
	if err != nil {
		return "", err
	}
	cmdArgs := []string{"worktree", "remove", abs}
	if args.Force {
		cmdArgs = append(cmdArgs, "--force")
	}
	cmd := exec.Command("git", cmdArgs...)
	cmd.Dir = r.cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return truncate(string(out), 2000), fmt.Errorf("exit-worktree: %w", err)
	}
	return truncate(string(out), 2000), nil
}

func (r *toolRuntime) runSendMessage(rawArgs []byte) (string, error) {
	var args struct {
		Target  string `json:"target"`
		Message string `json:"message"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("send-message args: %w", err)
	}
	msg := strings.TrimSpace(args.Message)
	if msg == "" {
		return "", fmt.Errorf("send-message: message is required")
	}
	if strings.TrimSpace(r.sessionDir) == "" {
		return "", fmt.Errorf("send-message requires an active session")
	}
	sid := filepath.Base(r.sessionDir)
	globalDir := filepath.Dir(filepath.Dir(r.sessionDir))
	_ = session.AppendEvent(globalDir, sid, session.AgentEvent{
		At:      time.Now(),
		Kind:    "message",
		AgentID: r.agentID,
		Task:    msg,
		Status:  "sent",
		Summary: "to " + emptyIfBlank(args.Target) + ": " + truncate(msg, 180),
	})
	return "message sent", nil
}

type allowedNetworkFile struct {
	Allowed []string `json:"allowed"`
}

func allowedNetworkPath(cwd string) string {
	return filepath.Join(cwd, ".spettro", "allowed_network.json")
}

func loadAllowedNetworkSet(cwd string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	raw, err := os.ReadFile(allowedNetworkPath(cwd))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	var parsed allowedNetworkFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	for _, s := range parsed.Allowed {
		s = strings.TrimSpace(s)
		if s != "" {
			out[s] = struct{}{}
		}
	}
	return out, nil
}

func saveAllowedNetworkSet(cwd string, set map[string]struct{}) error {
	items := make([]string, 0, len(set))
	for s := range set {
		if strings.TrimSpace(s) != "" {
			items = append(items, s)
		}
	}
	sort.Strings(items)
	raw, err := json.MarshalIndent(allowedNetworkFile{Allowed: items}, "", "  ")
	if err != nil {
		return err
	}
	path := allowedNetworkPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (r *toolRuntime) authorizeNetworkAccess(ctx context.Context, toolID, target string) error {
	target = normalizeCommand(target)
	if target == "" {
		target = "(network)"
	}
	needsApproval := r.permission != config.PermissionYOLO
	if spec, ok := r.toolPolicies[toolID]; ok && !spec.RequiresApproval {
		needsApproval = false
	}
	toolRules := []config.PermissionRule{}
	if spec, ok := r.toolPolicies[toolID]; ok {
		toolRules = append(toolRules, spec.PermissionRules...)
	}
	switch evaluatePermissionRule("network", target, r.runtimeRules, r.agentRules, toolRules) {
	case config.RuleDeny:
		return fmt.Errorf("%s denied by policy for target %q", toolID, target)
	case config.RuleAllow:
		return nil
	}
	allowed, err := loadAllowedNetworkSet(r.cwd)
	if err != nil {
		return fmt.Errorf("read network approvals: %w", err)
	}
	if _, ok := allowed[target]; ok || !needsApproval {
		return nil
	}
	if r.shellApproval == nil {
		return fmt.Errorf("%s requires approval outside yolo mode", toolID)
	}
	decision, err := r.shellApproval(ctx, ShellApprovalRequest{Command: "network " + toolID + " " + target})
	if err != nil {
		return fmt.Errorf("network approval failed: %w", err)
	}
	switch decision {
	case ShellApprovalAllowOnce:
		return nil
	case ShellApprovalAllowAlways:
		allowed[target] = struct{}{}
		if err := saveAllowedNetworkSet(r.cwd, allowed); err != nil {
			return fmt.Errorf("persist network approval: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("%s denied by user", toolID)
	}
}
