package hooks

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/charmbracelet/crush/internal/shell"
	"github.com/tidwall/gjson"
)

// SupportedOutputVersion is the highest envelope version this build
// understands. Hooks may omit `version` entirely (treated as 1) or pin
// an older version. Unknown higher versions are still parsed but logged.
const SupportedOutputVersion = 1

// EventInput describes a single hook event invocation. Not every field
// applies to every event: tool events (PreToolUse) carry ToolName and
// ToolInput, EventStop carries the session title and the final assistant
// Message, and EventSessionStart fires with only the session identity and
// directories.
type EventInput struct {
	// EventName is the hook event being fired (e.g. PreToolUse, Stop,
	// SessionStart).
	EventName string
	// SessionID is the session the event belongs to.
	SessionID string
	// SessionTitle is the human-readable session title. Set by EventStop.
	SessionTitle string
	// CWD is the working directory the hook runs in.
	CWD string
	// ProjectDir is the project root, exposed only via the environment.
	ProjectDir string
	// ToolName and ToolInput are set for tool events. ToolInput is the
	// raw JSON input the model sent to the tool.
	ToolName  string
	ToolInput string
	// Message is the final assistant text for the turn. Set by EventStop.
	Message string
}

// Payload is the JSON structure piped to hook commands via stdin.
// ToolInput is emitted as a parsed JSON object for compatibility with
// Claude Code hooks (which expect tool_input to be an object, not a
// string). Per-event fields that don't apply to the current event are
// omitted entirely rather than emitted empty.
type Payload struct {
	Event        string          `json:"event"`
	SessionID    string          `json:"session_id"`
	SessionTitle string          `json:"session_title,omitempty"`
	CWD          string          `json:"cwd"`
	ToolName     string          `json:"tool_name,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	Message      string          `json:"message,omitempty"`
}

// BuildPayload constructs the JSON stdin payload for a hook command.
func BuildPayload(in EventInput) []byte {
	p := Payload{
		Event:        in.EventName,
		SessionID:    in.SessionID,
		SessionTitle: in.SessionTitle,
		CWD:          in.CWD,
		ToolName:     in.ToolName,
		Message:      in.Message,
	}
	if in.ToolName != "" {
		toolInput := json.RawMessage(in.ToolInput)
		if !json.Valid(toolInput) {
			toolInput = json.RawMessage("{}")
		}
		p.ToolInput = toolInput
	}
	data, err := json.Marshal(p)
	if err != nil {
		return []byte("{}")
	}
	return data
}

// BuildEnv constructs the environment variable slice for a hook command.
// It includes all current process env vars plus hook-specific ones. Event
// specific variables are only set when the field they describe is
// populated, so a Stop hook never sees a stale CRUSH_TOOL_NAME.
func BuildEnv(in EventInput) []string {
	env := os.Environ()
	env = append(env, shell.CrushEnvMarkers()...)
	env = append(
		env,
		fmt.Sprintf("CRUSH_EVENT=%s", in.EventName),
		fmt.Sprintf("CRUSH_SESSION_ID=%s", in.SessionID),
		fmt.Sprintf("CRUSH_CWD=%s", in.CWD),
		fmt.Sprintf("CRUSH_PROJECT_DIR=%s", in.ProjectDir),
	)

	// Extract tool-specific env vars from the JSON input.
	if in.ToolName != "" {
		env = append(env, fmt.Sprintf("CRUSH_TOOL_NAME=%s", in.ToolName))
	}
	if in.SessionTitle != "" {
		env = append(env, fmt.Sprintf("CRUSH_SESSION_TITLE=%s", in.SessionTitle))
	}
	if in.ToolInput != "" {
		if cmd := gjson.Get(in.ToolInput, "command"); cmd.Exists() {
			env = append(env, fmt.Sprintf("CRUSH_TOOL_INPUT_COMMAND=%s", cmd.String()))
		}
		if fp := gjson.Get(in.ToolInput, "file_path"); fp.Exists() {
			env = append(env, fmt.Sprintf("CRUSH_TOOL_INPUT_FILE_PATH=%s", fp.String()))
		}
	}
	if in.Message != "" {
		env = append(env, fmt.Sprintf("CRUSH_STOP_MESSAGE=%s", in.Message))
	}

	return env
}

// parseStdout parses the JSON output from a hook command's stdout.
// Supports both Crush format and Claude Code format (hookSpecificOutput).
// When allowPlainText is set, stdout that is not valid JSON is returned
// verbatim as context. SessionStart sets it so hooks can simply print
// the text they want injected, matching Claude Code's SessionStart
// behavior; other events ignore non-JSON stdout.
func parseStdout(stdout string, allowPlainText bool) HookResult {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return HookResult{Decision: DecisionNone}
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		if allowPlainText {
			return HookResult{Context: stdout}
		}
		return HookResult{Decision: DecisionNone}
	}

	// Claude Code compat: if hookSpecificOutput is present, parse that.
	if hso, ok := raw["hookSpecificOutput"]; ok {
		return parseClaudeCodeOutput(hso)
	}

	var parsed struct {
		Version      int             `json:"version"`
		Decision     string          `json:"decision"`
		Halt         bool            `json:"halt"`
		Reason       string          `json:"reason"`
		Context      json.RawMessage `json:"context"`
		UpdatedInput json.RawMessage `json:"updated_input"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		if allowPlainText {
			return HookResult{Context: stdout}
		}
		return HookResult{Decision: DecisionNone}
	}

	if parsed.Version > SupportedOutputVersion {
		slog.Debug(
			"Hook output declared a newer envelope version than this build supports",
			"version", parsed.Version,
			"supported", SupportedOutputVersion,
		)
	}

	result := HookResult{
		Decision: parseDecision(parsed.Decision),
		Halt:     parsed.Halt,
		Reason:   parsed.Reason,
		Context:  parseContext(parsed.Context),
	}
	result.UpdatedInput = rawToString(parsed.UpdatedInput)
	return result
}

// parseContext accepts either a single string or an array of strings and
// returns a newline-joined value with empty entries dropped.
func parseContext(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// String form.
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
		return ""
	}
	// Array form.
	if raw[0] == '[' {
		var items []string
		if err := json.Unmarshal(raw, &items); err != nil {
			return ""
		}
		out := items[:0]
		for _, s := range items {
			if s != "" {
				out = append(out, s)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// parseClaudeCodeOutput handles the Claude Code hook output format:
// {"hookSpecificOutput": {"permissionDecision": "allow", ...}}
func parseClaudeCodeOutput(data json.RawMessage) HookResult {
	var hso struct {
		PermissionDecision       string          `json:"permissionDecision"`
		PermissionDecisionReason string          `json:"permissionDecisionReason"`
		UpdatedInput             json.RawMessage `json:"updatedInput"`
		AdditionalContext        string          `json:"additionalContext"`
	}
	if err := json.Unmarshal(data, &hso); err != nil {
		return HookResult{Decision: DecisionNone}
	}

	result := HookResult{
		Decision: parseDecision(hso.PermissionDecision),
		Reason:   hso.PermissionDecisionReason,
		Context:  hso.AdditionalContext,
	}

	// Marshal updatedInput back to a string for our opaque format.
	if len(hso.UpdatedInput) > 0 && string(hso.UpdatedInput) != "null" {
		result.UpdatedInput = string(hso.UpdatedInput)
	}

	return result
}

// rawToString converts a json.RawMessage to a string suitable for use
// as opaque tool input. It accepts both a JSON object (nested) and a
// JSON string (stringified, for backward compatibility).
func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// If it's a JSON string, unwrap it.
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	// Otherwise it's an object/array — use as-is.
	return string(raw)
}

func parseDecision(s string) Decision {
	switch strings.ToLower(s) {
	case "allow":
		return DecisionAllow
	case "deny":
		return DecisionDeny
	default:
		return DecisionNone
	}
}
