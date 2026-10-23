package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// sessionStartRunner builds a SessionStart hooks.Runner from a single
// command, going through the config-loading path so the event name is
// normalized.
func sessionStartRunner(t *testing.T, command string) *hooks.Runner {
	t.Helper()
	cfg := &config.Config{
		Hooks: map[string][]config.HookConfig{
			hooks.EventSessionStart: {{Command: command}},
		},
	}
	require.NoError(t, cfg.ValidateHooks())
	return hooks.NewRunner(cfg.Hooks[hooks.EventSessionStart], t.TempDir(), t.TempDir())
}

func TestFireSessionStartHooksInjectsContextAndPersists(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	runner := sessionStartRunner(t, `printf 'prime directives go here'`)
	a := NewSessionAgent(SessionAgentOptions{
		Sessions:          env.sessions,
		Messages:          env.messages,
		SessionStartHooks: runner,
	}).(*sessionAgent)

	got := a.fireSessionStartHooks(t.Context(), sess.ID)
	require.Equal(t, "prime directives go here", got)

	// The context is persisted as a hidden user message so later turns
	// keep it in the model's history without showing it in the UI.
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, message.User, msgs[0].Role)
	var tc message.TextContent
	for _, part := range msgs[0].Parts {
		if p, ok := part.(message.TextContent); ok {
			tc = p
			break
		}
	}
	require.Equal(t, "prime directives go here", tc.Text)
	require.True(t, tc.Hidden, "SessionStart context must be hidden from the UI")
}

func TestFireSessionStartHooksEmptyOutputInjectsNothing(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	runner := sessionStartRunner(t, `printf ''`)
	a := NewSessionAgent(SessionAgentOptions{
		Sessions:          env.sessions,
		Messages:          env.messages,
		SessionStartHooks: runner,
	}).(*sessionAgent)

	require.Empty(t, a.fireSessionStartHooks(t.Context(), sess.ID))

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, msgs, "no context means no message is persisted")
}

func TestFireSessionStartHooksSkippedForSubAgents(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "seen.txt")
	runner := sessionStartRunner(t, `printf 'ran' > `+out)

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	a := NewSessionAgent(SessionAgentOptions{
		IsSubAgent:        true,
		Sessions:          env.sessions,
		Messages:          env.messages,
		SessionStartHooks: runner,
	}).(*sessionAgent)

	require.Empty(t, a.fireSessionStartHooks(t.Context(), sess.ID))

	_, statErr := os.Stat(out)
	require.ErrorIs(t, statErr, os.ErrNotExist, "sub-agents must not fire SessionStart hooks")
}

// TestSetSessionStartHooksTakesEffect verifies a runner injected after
// construction (the config-reload path) is picked up, and that clearing it
// with nil disables SessionStart hooks again.
func TestSetSessionStartHooksTakesEffect(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	a := NewSessionAgent(SessionAgentOptions{
		Sessions: env.sessions,
		Messages: env.messages,
	}).(*sessionAgent)

	a.SetSessionStartHooks(sessionStartRunner(t, `printf 'ran'`))
	require.Equal(t, "ran", a.fireSessionStartHooks(t.Context(), sess.ID))

	a.SetSessionStartHooks(nil)
	require.Empty(t, a.fireSessionStartHooks(t.Context(), sess.ID))
}
