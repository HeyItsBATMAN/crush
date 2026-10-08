package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/stretchr/testify/require"
)

// stopRunner builds a Stop hooks.Runner from a single command, going
// through the config-loading path so the event name is normalized.
func stopRunner(t *testing.T, command string) *hooks.Runner {
	t.Helper()
	cfg := &config.Config{
		Hooks: map[string][]config.HookConfig{
			hooks.EventStop: {{Command: command}},
		},
	}
	require.NoError(t, cfg.ValidateHooks())
	return hooks.NewRunner(cfg.Hooks[hooks.EventStop], t.TempDir(), t.TempDir())
}

func TestFireStopHooksRunsAtTurnEnd(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "seen.txt")
	runner := stopRunner(t, `printf '%s|%s|%s' "$CRUSH_EVENT" "$CRUSH_SESSION_TITLE" "$CRUSH_STOP_MESSAGE" > `+out)

	a := NewSessionAgent(SessionAgentOptions{StopHooks: runner}).(*sessionAgent)

	a.fireStopHooks(context.Background(), "sess-1", "My session", "hello")

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, "Stop|My session|hello", string(got))
}

func TestFireStopHooksSkippedForSubAgents(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "seen.txt")
	runner := stopRunner(t, `printf 'ran' > `+out)

	a := NewSessionAgent(SessionAgentOptions{
		IsSubAgent: true,
		StopHooks:  runner,
	}).(*sessionAgent)

	a.fireStopHooks(context.Background(), "sess-1", "My session", "hello")

	_, err := os.Stat(out)
	require.ErrorIs(t, err, os.ErrNotExist, "sub-agents must not fire Stop hooks")
}

func TestFireStopHooksWithoutRunnerIsNoop(t *testing.T) {
	t.Parallel()

	a := NewSessionAgent(SessionAgentOptions{}).(*sessionAgent)

	require.NotPanics(t, func() {
		a.fireStopHooks(context.Background(), "sess-1", "My session", "hello")
	})
}

// TestSetStopHooksTakesEffect verifies a runner injected after
// construction (the config-reload path) is picked up, and that clearing it
// with nil disables Stop hooks again.
func TestSetStopHooksTakesEffect(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := filepath.Join(dir, "seen.txt")
	a := NewSessionAgent(SessionAgentOptions{}).(*sessionAgent)

	a.SetStopHooks(stopRunner(t, `printf 'ran' >> `+out))
	a.fireStopHooks(context.Background(), "sess-1", "My session", "hello")

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, "ran", string(got))

	a.SetStopHooks(nil)
	a.fireStopHooks(context.Background(), "sess-1", "My session", "hello")

	got, err = os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, "ran", string(got), "cleared Stop hooks must not run")
}
