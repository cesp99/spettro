package tui

import "strings"

type commandDef struct {
	name string
	desc string
}

var allCommands = []commandDef{
	{"/help", "show help"},
	{"/models", "switch model"},
	{"/connect", "connect a provider"},
	{"/mode", "cycle mode"},
	{"/approve", "execute pending plan"},
	{"/permission", "set permission level"},
	{"/budget", "set token budget per request  usage: /budget <n|0>"},
	{"/init", "analyze codebase and write SPETTRO.md"},
	{"/compact", "summarize conversation (optionally focused)"},
	{"/tasks", "manage session tasks"},
	{"/mcp", "list/read/auth MCP resources"},
	{"/skills", "list local skills/prompts"},
	{"/plan", "switch plan mode or run plan task"},
	{"/permissions", "show/set permission level"},
	{"/clear", "clear conversation history"},
	{"/resume", "resume a previous conversation"},
	{"/exit", "exit spettro"},
}

var permissionCommands = []commandDef{
	{"/permission yolo", "no approval required for any action"},
	{"/permission restricted", "ask once, remember for session"},
	{"/permission ask-first", "always ask before executing"},
}

func filterCommands(query string) []commandDef {
	if query == "" {
		return append([]commandDef(nil), allCommands...)
	}
	q := strings.ToLower(query)
	var out []commandDef
	for _, c := range allCommands {
		if strings.Contains(c.name, q) || strings.Contains(c.desc, q) {
			out = append(out, c)
		}
	}
	return out
}

const helpText = `commands:
  /help          this message
  /exit /quit    quit spettro  (or ctrl+c twice)
  /mode          cycle to next mode  (or shift+tab)
  /setup         run setup wizard
  /models        open model selector (connected providers only)
  /models p:m    set model directly
  /connect       connect a provider or local endpoint
  /permission    set permission: yolo | restricted | ask-first
  /permissions   show or set permission level
  /approve       approve and execute pending plan (coding mode)
  /plan [prompt] switch to plan mode or run a plan request
  /tasks         manage tasks (list/add/done/set/show)
  /mcp           manage MCP resources (list/read/auth)
  /skills        list local skills/prompts
  /index         index project files → .spettro/index.json
  /coauthor      show co-author info for git commits
  /compact [x]   summarize conversation (optional focus instruction)
  /clear         clear conversation history (auto-saves first)
  /resume        resume a previous saved conversation

keys:
  shift+tab      cycle mode (plan → coding → ask)
  f2             cycle to next favorite model
  shift+f2       cycle to previous favorite model

in model selector:
  f              toggle favorite (★) for highlighted model
  c              open connect provider dialog`
