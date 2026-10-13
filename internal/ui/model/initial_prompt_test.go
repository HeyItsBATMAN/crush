package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/stretchr/testify/require"
)

// configuredTestConfig returns a config with a single enabled provider so
// IsConfigured reports true.
func configuredTestConfig() *config.Config {
	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("test", config.ProviderConfig{ID: "test"})
	return &config.Config{Providers: providers}
}

func TestInitialPromptCmd(t *testing.T) {
	t.Parallel()

	t.Run("nil when no prompt was provided", func(t *testing.T) {
		t.Parallel()

		u := &UI{com: &common.Common{Workspace: &testWorkspace{cfg: configuredTestConfig()}}}
		require.Nil(t, u.initialPromptCmd())
	})

	t.Run("nil when the workspace is unconfigured and keeps the prompt", func(t *testing.T) {
		t.Parallel()

		u := &UI{
			com:           &common.Common{Workspace: &testWorkspace{cfg: &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}}},
			initialPrompt: "hello",
		}
		require.Nil(t, u.initialPromptCmd())
		// The prompt must survive so onboarding can still use it.
		require.Equal(t, "hello", u.initialPrompt)
	})

	t.Run("emits sendMessageMsg and clears the prompt", func(t *testing.T) {
		t.Parallel()

		u := &UI{
			com:           &common.Common{Workspace: &testWorkspace{cfg: configuredTestConfig()}},
			initialPrompt: "explain this codebase",
		}

		cmd := u.initialPromptCmd()
		require.NotNil(t, cmd)

		sent, ok := cmd().(sendMessageMsg)
		require.True(t, ok, "expected a sendMessageMsg, got %T", cmd())
		require.Equal(t, "explain this codebase", sent.Content)
		require.Empty(t, u.initialPrompt)

		// A second call must not resend the same prompt.
		require.Nil(t, u.initialPromptCmd())
	})
}
