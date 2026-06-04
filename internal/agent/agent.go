package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nathan/hebb/internal/embed"
	"github.com/nathan/hebb/internal/memory"
	"github.com/nathan/hebb/internal/store"
)

const (
	managedStart      = "<!-- BEGIN HEBB MEMORY -->"
	managedEnd        = "<!-- END HEBB MEMORY -->"
	codexPluginName   = "hebb-memory"
	codexMarketplace  = "personal"
	codexPluginParent = "~/plugins"
)

type InstallOptions struct {
	Agent string
	Apply bool
	Force bool
}

type HookInput struct {
	HookEventName        string         `json:"hook_event_name"`
	CWD                  string         `json:"cwd"`
	UserPrompt           string         `json:"user_prompt"`
	Prompt               string         `json:"prompt"`
	Reason               string         `json:"reason"`
	LastAssistantMessage string         `json:"last_assistant_message"`
	ToolName             string         `json:"tool_name"`
	ToolInput            map[string]any `json:"tool_input"`
	ToolResult           any            `json:"tool_result"`
}

type Installer struct {
	Stdout io.Writer
}

func (i Installer) Install(ctx context.Context, opts InstallOptions) error {
	agent := strings.ToLower(strings.TrimSpace(opts.Agent))
	if agent == "" {
		agent = "all"
	}
	switch agent {
	case "all":
		if err := i.Install(ctx, InstallOptions{Agent: "codex", Apply: opts.Apply, Force: opts.Force}); err != nil {
			return err
		}
		return i.Install(ctx, InstallOptions{Agent: "claude", Apply: opts.Apply, Force: opts.Force})
	case "codex", "claude":
	default:
		return fmt.Errorf("unsupported agent %q; use codex, claude or all", opts.Agent)
	}

	if !opts.Apply {
		fmt.Fprintf(i.Stdout, "%s\n\n", Plan(agent))
		return nil
	}

	if err := ensureHebbReady(ctx); err != nil {
		return err
	}
	switch agent {
	case "codex":
		return i.installCodex(ctx)
	case "claude":
		return i.installClaude(ctx)
	}
	return nil
}

func (i Installer) installCodex(ctx context.Context) error {
	pluginPath, err := expandHome(codexPluginParent + "/" + codexPluginName)
	if err != nil {
		return err
	}
	marketplacePath, err := expandHome("~/.agents/plugins/marketplace.json")
	if err != nil {
		return err
	}
	if err := writeCodexPlugin(pluginPath); err != nil {
		return err
	}
	if err := ensureCodexMarketplace(marketplacePath); err != nil {
		return err
	}
	if err := runBestEffort(ctx, "codex", "plugin", "remove", codexPluginName+"@"+codexMarketplace); err != nil {
		fmt.Fprintf(i.Stdout, "codex plugin remove skipped: %v\n", err)
	}
	if err := run(ctx, "codex", "plugin", "add", codexPluginName, "--marketplace", codexMarketplace); err != nil {
		return err
	}
	if err := runBestEffort(ctx, "codex", "mcp", "remove", "hebb"); err != nil {
		fmt.Fprintf(i.Stdout, "codex mcp remove skipped: %v\n", err)
	}
	if err := run(ctx, "codex", "mcp", "add", "hebb", "--", "hebb", "mcp"); err != nil {
		return err
	}
	path, err := expandHome("~/.codex/AGENTS.md")
	if err != nil {
		return err
	}
	if err := upsertManagedBlock(path, Instructions("codex")); err != nil {
		return err
	}
	fmt.Fprintf(i.Stdout, "Codex configured with Hebb MCP, local plugin hooks and managed memory instructions.\nplugin: %s\n", pluginPath)
	return nil
}

func (i Installer) installClaude(ctx context.Context) error {
	mcpPath, err := expandHome("~/.claude.json")
	if err != nil {
		return err
	}
	if err := ensureClaudeMCP(mcpPath); err != nil {
		return err
	}
	settingsPath, err := expandHome("~/.claude/settings.json")
	if err != nil {
		return err
	}
	if err := ensureClaudeHooks(settingsPath); err != nil {
		return err
	}
	instructionsPath, err := expandHome("~/.claude/CLAUDE.md")
	if err != nil {
		return err
	}
	if err := upsertManagedBlock(instructionsPath, Instructions("claude")); err != nil {
		return err
	}
	fmt.Fprintln(i.Stdout, "Claude configured with Hebb MCP, hooks and managed memory instructions.")
	return nil
}

func Plan(agent string) string {
	if agent == "codex" {
		return `Hebb agent install plan for Codex:
- Create/update local plugin ~/plugins/hebb-memory
- Add plugin entry to ~/.agents/plugins/marketplace.json
- Install plugin with: codex plugin add hebb-memory --marketplace personal
- Plugin provides Hebb MCP, memory skill instructions and UserPromptSubmit/PostToolUse hooks
- Register MCP server: codex mcp add hebb -- hebb mcp
- Upsert managed instructions in ~/.codex/AGENTS.md

Run with --apply to write these changes.`
	}
	if agent == "claude" {
		return `Hebb agent install plan for Claude:
- Add MCP server "hebb" to ~/.claude.json
- Add UserPromptSubmit and Stop hooks to ~/.claude/settings.json
- Upsert managed instructions in ~/.claude/CLAUDE.md
- Hooks call hebb agent hook ... so context loading and capture happen automatically

Run with --apply to write these changes.`
	}
	return `Hebb agent install plan:
- Configure Codex MCP + instructions
- Configure Claude MCP + hooks + instructions

Run with --apply to write these changes.`
}

func Instructions(agentName string) string {
	return strings.TrimSpace(fmt.Sprintf(`
%s

## Hebb Memory

Hebb is the user's local long-term memory. Use it naturally and proactively; do not wait for the user to say "search memory" or "save memory".

At the start of a task, retrieve relevant context from Hebb using the MCP tool `+"`hebb_retrieve_context`"+` with a query based on the user's request, current entities, project names and likely preferences.

During the task, encode durable information with `+"`hebb_encode_trace`"+` when you learn:

- stable user preferences
- decisions and rationale
- project conventions
- procedures and runbooks
- warnings, gotchas and recurring bugs
- important facts that should survive across sessions

When a memory is useful, reinforce it with `+"`hebb_reinforce_trace`"+`. When a memory is stale, noisy or contradicted, inhibit it with `+"`hebb_inhibit_trace`"+` instead of deleting it.

Keep memory hygienic. Do not store secrets, credentials, raw transcript dumps or short-lived implementation chatter. Prefer concise traces with clear titles and actionable bodies.

Only save memory when the content is clearly durable. Avoid saving your own final status messages, command outputs, generic summaries or implementation chatter.

Use global memory by default. Add a scope only when the user explicitly wants a memory restricted to a project or context.

Agent configured by: %s
%s`, managedStart, agentName, managedEnd))
}

func HandleHook(ctx context.Context, s *store.Store, mode string, input io.Reader, output io.Writer) error {
	var payload HookInput
	body, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) > 0 {
		_ = json.Unmarshal(body, &payload)
	}

	switch mode {
	case "session-start":
		return hookSessionStart(ctx, s, payload, output)
	case "user-prompt-submit":
		return hookUserPrompt(ctx, s, payload, output)
	case "stop":
		return hookStop(ctx, s, payload, output)
	case "codex-post-tool-use":
		return hookCodexPostToolUse(ctx, s, payload, output)
	default:
		return fmt.Errorf("unknown hook mode %q", mode)
	}
}

func hookSessionStart(ctx context.Context, s *store.Store, payload HookInput, output io.Writer) error {
	query := "user preferences durable decisions procedures warnings current work"
	results, _ := s.Retrieve(ctx, store.RetrieveOptions{Query: query, Limit: 8})
	contextText := formatRetrieved(results)
	if contextText == "" {
		return writeHookContext(output, "SessionStart", "Hebb memory is enabled. No relevant memories were found yet.")
	}
	return writeHookContext(output, "SessionStart", "Relevant Hebb memories:\n\n"+contextText)
}

func hookUserPrompt(ctx context.Context, s *store.Store, payload HookInput, output io.Writer) error {
	prompt := strings.TrimSpace(payload.UserPrompt)
	if prompt == "" {
		prompt = strings.TrimSpace(payload.Prompt)
	}
	if prompt == "" {
		return nil
	}
	vector := embedHookQuery(ctx, prompt)
	results, _ := s.Retrieve(ctx, store.RetrieveOptions{Query: prompt, Limit: 12, Vector: vector, MinVectorScore: 0.72})
	if shouldCaptureUserPrompt(prompt) {
		_, _ = s.CreateTrace(ctx, store.TraceInput{
			Kind:       memory.TraceObservation,
			Title:      firstLine(prompt, 80),
			Body:       truncate(prompt, 4000),
			Source:     "hebb agent hook:user-prompt-submit",
			Confidence: 0.55,
			Strength:   0.35,
			Salience:   0.35,
		}, nil)
	}
	contextText := formatRetrieved(results)
	if contextText == "" {
		return nil
	}
	return writeHookContext(output, "UserPromptSubmit", "Hebb recalled potentially relevant memories:\n\n"+contextText)
}

func embedHookQuery(ctx context.Context, query string) []float32 {
	vector, err := embed.NewClient("", "").Embed(ctx, query)
	if err != nil {
		return nil
	}
	return vector
}

func hookStop(ctx context.Context, s *store.Store, payload HookInput, output io.Writer) error {
	return nil
}

func hookCodexPostToolUse(ctx context.Context, s *store.Store, payload HookInput, output io.Writer) error {
	text := codexToolText(payload)
	if !shouldCaptureUserPrompt(text) {
		return nil
	}
	_, _ = s.CreateTrace(ctx, store.TraceInput{
		Kind:       memory.TraceObservation,
		Title:      firstLine(text, 80),
		Body:       truncate(text, 4000),
		Source:     "hebb codex plugin:post-tool-use",
		Confidence: 0.45,
		Strength:   0.3,
		Salience:   0.3,
	}, nil)
	return nil
}

func codexToolText(payload HookInput) string {
	var parts []string
	if payload.ToolName != "" {
		parts = append(parts, payload.ToolName)
	}
	if len(payload.ToolInput) > 0 {
		if data, err := json.Marshal(payload.ToolInput); err == nil {
			parts = append(parts, string(data))
		}
	}
	if payload.ToolResult != nil {
		if data, err := json.Marshal(payload.ToolResult); err == nil {
			parts = append(parts, string(data))
		}
	}
	return strings.Join(parts, "\n")
}

func writeHookContext(output io.Writer, event, text string) error {
	response := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     event,
			"additionalContext": text,
		},
		"systemMessage": text,
	}
	return json.NewEncoder(output).Encode(response)
}

func formatRetrieved(results []store.RetrievedTrace) string {
	var b strings.Builder
	for _, result := range results {
		if result.Trace.Status != memory.StatusActive {
			continue
		}
		if isNoisyAutoTrace(result.Trace) {
			continue
		}
		fmt.Fprintf(&b, "- [%d] %s: %s\n", result.Trace.ID, result.Trace.Title, strings.TrimSpace(result.Trace.Body))
	}
	return strings.TrimSpace(b.String())
}

func shouldCaptureUserPrompt(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if looksLikeCommandOutput(lower) {
		return false
	}
	strongPatterns := []string{
		"prefiro ", "eu prefiro", "minha preferência", "lembre", "lembra que", "salva ", "salve ",
		"remember that", "please remember", "always ", "never ", "sempre ", "nunca ",
		"decidimos ", "decisão:", "decision:", "convenção:", "procedimento:", "runbook:",
	}
	for _, pattern := range strongPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func isNoisyAutoTrace(trace memory.Trace) bool {
	if strings.HasPrefix(trace.Title, "Agent turn completed:") {
		return true
	}
	if strings.Contains(trace.Source, "hebb agent hook:stop") {
		return true
	}
	if looksLikeCommandOutput(strings.ToLower(trace.Body)) {
		return true
	}
	return false
}

func looksLikeCommandOutput(text string) bool {
	markers := []string{
		"ran git ", "called hebb.", "git status --short", "git branch --show-current", "git log -1",
		"exit status", "process exited", "wall time:", "chunk id:",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func firstLine(text string, limit int) string {
	text = strings.TrimSpace(strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")[0])
	return truncate(text, limit)
}

func truncate(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}

func ensureHebbReady(ctx context.Context) error {
	if _, err := exec.LookPath("hebb"); err != nil {
		return errors.New("hebb must be available on PATH before installing agent integration; run task install or put bin/hebb on PATH")
	}
	return run(ctx, "hebb", "init")
}

func writeCodexPlugin(pluginPath string) error {
	files := map[string][]byte{
		filepath.Join(pluginPath, ".codex-plugin", "plugin.json"):      []byte(codexPluginManifest()),
		filepath.Join(pluginPath, ".mcp.json"):                         []byte(codexPluginMCP()),
		filepath.Join(pluginPath, "hooks", "hooks.json"):               []byte(codexPluginHooks()),
		filepath.Join(pluginPath, "scripts", "user_prompt_submit.sh"):  []byte(codexUserPromptSubmitScript()),
		filepath.Join(pluginPath, "scripts", "post_tool_use.sh"):       []byte(codexPostToolUseScript()),
		filepath.Join(pluginPath, "skills", "hebb-memory", "SKILL.md"): []byte(codexMemorySkill()),
	}
	for path, data := range files {
		perm := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") {
			perm = 0o755
		}
		if err := writeFileWithBackup(path, data, perm); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(pluginPath, "hooks.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(filepath.Join(pluginPath, "scripts", "session_start.sh")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func codexPluginManifest() string {
	return `{
  "name": "hebb-memory",
  "version": "0.1.0",
  "description": "Local-first long-term memory for Codex using Hebb.",
  "author": {
    "name": "Hebb contributors"
  },
  "homepage": "https://github.com/NathanFirmo/hebb",
  "repository": "https://github.com/NathanFirmo/hebb",
  "license": "MIT",
  "keywords": ["memory", "mcp", "local-first", "agents"],
  "skills": "./skills/",
  "mcpServers": "./.mcp.json",
  "hooks": "./hooks/hooks.json",
  "interface": {
    "displayName": "Hebb Memory",
    "shortDescription": "Local long-term memory for Codex",
    "longDescription": "Adds Hebb as a local-first memory layer for Codex with MCP tools, conservative memory instructions and lifecycle hooks.",
    "developerName": "Hebb",
    "category": "Productivity",
    "capabilities": ["Read", "Write"],
    "websiteURL": "https://github.com/NathanFirmo/hebb",
    "privacyPolicyURL": "https://github.com/NathanFirmo/hebb",
    "termsOfServiceURL": "https://github.com/NathanFirmo/hebb",
    "defaultPrompt": [
      "Recall my relevant Hebb memories",
      "Save this durable preference to Hebb",
      "Show Hebb memory stats"
    ],
    "brandColor": "#3B5BDB",
    "screenshots": []
  }
}
`
}

func codexPluginMCP() string {
	return `{
  "mcpServers": {
    "hebb": {
      "command": "hebb",
      "args": ["mcp"]
    }
  }
}
`
}

func codexPluginHooks() string {
	return `{
  "hooks": {
    "UserPromptSubmit": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "\"$PLUGIN_ROOT/scripts/user_prompt_submit.sh\""
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "\"$PLUGIN_ROOT/scripts/post_tool_use.sh\""
          }
        ]
      }
    ]
  }
}
`
}

func codexUserPromptSubmitScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail

if command -v hebb >/dev/null 2>&1; then
  hebb agent hook user-prompt-submit || true
fi
`
}

func codexPostToolUseScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail

if command -v hebb >/dev/null 2>&1; then
  hebb agent hook codex-post-tool-use >/dev/null 2>&1 || true
fi
`
}

func codexMemorySkill() string {
	return `---
name: hebb-memory
description: Use Hebb local-first long-term memory proactively for durable user preferences, decisions, procedures, warnings and project conventions.
---

# Hebb Memory

Use Hebb naturally as the user's local long-term memory. Do not wait for explicit phrasing like "search memory" or "save memory" when memory use is clearly relevant.

At the start of a task, call ` + "`hebb_retrieve_context`" + ` with a query based on the user's request, current entities, project names and likely preferences.

Encode durable information with ` + "`hebb_encode_trace`" + ` when you learn stable preferences, decisions, procedures, runbooks, warnings, gotchas or important facts that should survive across sessions.

Reinforce useful retrieved memories with ` + "`hebb_reinforce_trace`" + `. Inhibit stale or contradicted memories with ` + "`hebb_inhibit_trace`" + `.

Keep memory hygienic. Do not save secrets, raw transcript dumps, command output, generic final answers or short-lived implementation chatter.

Use global memory by default unless the user explicitly requests a scope.
`
}

func ensureCodexMarketplace(path string) error {
	var root map[string]any
	if err := readJSONFile(path, &root); err != nil {
		return err
	}
	if root["name"] == nil {
		root["name"] = codexMarketplace
	}
	if root["interface"] == nil {
		root["interface"] = map[string]any{"displayName": "Personal"}
	}
	plugins, _ := root["plugins"].([]any)
	entry := map[string]any{
		"name": codexPluginName,
		"source": map[string]any{
			"source": "local",
			"path":   "./plugins/" + codexPluginName,
		},
		"policy": map[string]any{
			"installation":   "AVAILABLE",
			"authentication": "ON_INSTALL",
		},
		"category": "Productivity",
	}
	replaced := false
	for index, item := range plugins {
		plugin, _ := item.(map[string]any)
		if plugin["name"] == codexPluginName {
			plugins[index] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		plugins = append(plugins, entry)
	}
	root["plugins"] = plugins
	return writeJSONFileWithBackup(path, root)
}

func ensureClaudeMCP(path string) error {
	var root map[string]any
	if err := readJSONFile(path, &root); err != nil {
		return err
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		root["mcpServers"] = servers
	}
	servers["hebb"] = map[string]any{
		"type":    "stdio",
		"command": "hebb",
		"args":    []any{"mcp"},
		"env":     map[string]any{},
	}
	return writeJSONFileWithBackup(path, root)
}

func ensureClaudeHooks(path string) error {
	var root map[string]any
	if err := readJSONFile(path, &root); err != nil {
		return err
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	removeClaudeHook(hooks, "SessionStart", "hebb agent hook session-start")
	ensureClaudeHook(hooks, "UserPromptSubmit", "hebb agent hook user-prompt-submit")
	ensureClaudeHook(hooks, "Stop", "hebb agent hook stop")
	return writeJSONFileWithBackup(path, root)
}

func removeClaudeHook(hooks map[string]any, event, command string) {
	current, _ := hooks[event].([]any)
	if len(current) == 0 {
		return
	}
	var next []any
	for _, item := range current {
		entry, _ := item.(map[string]any)
		hookItems, _ := entry["hooks"].([]any)
		var nextHookItems []any
		for _, hookItem := range hookItems {
			hook, _ := hookItem.(map[string]any)
			if hook["command"] == command {
				continue
			}
			nextHookItems = append(nextHookItems, hookItem)
		}
		if len(nextHookItems) == 0 {
			continue
		}
		entry["hooks"] = nextHookItems
		next = append(next, entry)
	}
	if len(next) == 0 {
		delete(hooks, event)
		return
	}
	hooks[event] = next
}

func ensureClaudeHook(hooks map[string]any, event, command string) {
	current, _ := hooks[event].([]any)
	for _, item := range current {
		entry, _ := item.(map[string]any)
		hookItems, _ := entry["hooks"].([]any)
		for _, hookItem := range hookItems {
			hook, _ := hookItem.(map[string]any)
			if hook["command"] == command {
				return
			}
		}
	}
	current = append(current, map[string]any{
		"matcher": "*",
		"hooks": []any{
			map[string]any{
				"type":          "command",
				"command":       command,
				"timeout":       10,
				"statusMessage": "Syncing Hebb memory...",
			},
		},
	})
	hooks[event] = current
}

func readJSONFile(path string, dst *map[string]any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		*dst = map[string]any{}
		return nil
	}
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		*dst = map[string]any{}
		return nil
	}
	return json.Unmarshal(data, dst)
}

func writeJSONFileWithBackup(path string, value map[string]any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileWithBackup(path, data, 0o644)
}

func upsertManagedBlock(path, block string) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return writeFileWithBackup(path, []byte(block+"\n"), 0o644)
	}
	if err != nil {
		return err
	}
	text := string(content)
	start := strings.Index(text, managedStart)
	end := strings.Index(text, managedEnd)
	if start >= 0 && end >= start {
		end += len(managedEnd)
		text = strings.TrimSpace(text[:start]) + "\n\n" + block + "\n\n" + strings.TrimSpace(text[end:])
	} else {
		text = strings.TrimRight(text, "\n") + "\n\n" + block + "\n"
	}
	return writeFileWithBackup(path, []byte(strings.TrimSpace(text)+"\n"), 0o644)
}

func writeFileWithBackup(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil {
		backup := fmt.Sprintf("%s.bak-%s", path, time.Now().UTC().Format("20060102T150405Z"))
		if err := os.WriteFile(backup, existing, perm); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, perm)
}

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func runBestEffort(ctx context.Context, name string, args ...string) error {
	err := run(ctx, name, args...)
	if err != nil && strings.Contains(err.Error(), "not found") {
		return nil
	}
	return err
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
