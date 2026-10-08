package dialog

import (
	"testing"

	"charm.land/bubbles/v2/textinput"
	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func newCommandsForTest(custom []commands.CustomCommand, mcp []commands.MCPPrompt, entries []skills.CatalogEntry) *Commands {
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	c := &Commands{
		com:            com,
		customCommands: custom,
		mcpPrompts:     mcp,
		skills:         entries,
		selected:       SystemCommands,
	}
	c.list = list.NewFilterableList()
	c.list.Focus()
	c.input = textinput.New()
	return c
}

func skillEntry(id, name, description string, invocable bool) skills.CatalogEntry {
	return skills.CatalogEntry{
		ID:            id,
		Name:          name,
		Description:   description,
		UserInvocable: invocable,
	}
}

// panelIDs returns the IDs of the items currently shown in the list.
func panelIDs(items []list.Item) []string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		if ci, ok := it.(*CommandItem); ok {
			ids = append(ids, ci.id)
		}
	}
	return ids
}

func TestCommandsCommandTypes(t *testing.T) {
	t.Parallel()

	plain := commands.CustomCommand{ID: "user:plain", Name: "user:plain", Content: "hi"}

	t.Run("system and skills are always present", func(t *testing.T) {
		c := newCommandsForTest(nil, nil, nil)
		require.Equal(t, []CommandType{SystemCommands, Skills}, c.commandTypes())
		require.False(t, c.hasSkills())
	})

	t.Run("user appears with a custom command", func(t *testing.T) {
		c := newCommandsForTest([]commands.CustomCommand{plain}, nil, nil)
		require.Equal(t, []CommandType{SystemCommands, UserCommands, Skills}, c.commandTypes())
	})

	t.Run("full cycle order", func(t *testing.T) {
		c := newCommandsForTest(
			[]commands.CustomCommand{plain},
			[]commands.MCPPrompt{{ID: "gh", PromptID: "gh"}},
			[]skills.CatalogEntry{skillEntry("/skills/deploy/SKILL.md", "deploy", "Deploy.", true)},
		)
		require.Equal(t, []CommandType{SystemCommands, UserCommands, MCPPrompts, Skills}, c.commandTypes())

		c.selected = SystemCommands
		require.Equal(t, UserCommands, c.nextCommandType())
		c.selected = UserCommands
		require.Equal(t, MCPPrompts, c.nextCommandType())
		c.selected = MCPPrompts
		require.Equal(t, Skills, c.nextCommandType())
		c.selected = Skills
		require.Equal(t, SystemCommands, c.nextCommandType())

		c.selected = SystemCommands
		require.Equal(t, Skills, c.previousCommandType())
	})
}

func TestCommandsSkillsPanelScopeToggle(t *testing.T) {
	t.Parallel()

	c := newCommandsForTest(nil, nil, []skills.CatalogEntry{
		skillEntry("/skills/deploy/SKILL.md", "deploy", "Deploy the app.", true),
		skillEntry("/skills/adhd/SKILL.md", "i-have-adhd", "Output style.", false),
	})

	c.setCommandItems(Skills)

	// Default scope: toggle row first, then only the user-invocable skill.
	ids := panelIDs(c.list.FilteredItems())
	require.Equal(t, []string{skillScopeToggleID, "skill_/skills/deploy/SKILL.md"}, ids)

	// Toggling to "all skills" keeps the toggle on top and reveals the rest.
	c.toggleSkillScope()
	require.True(t, c.showAllSkills)
	ids = panelIDs(c.list.FilteredItems())
	require.Equal(t, []string{
		skillScopeToggleID,
		"skill_/skills/deploy/SKILL.md",
		"skill_/skills/adhd/SKILL.md",
	}, ids)

	// Selecting a skill still yields the attach action.
	item, ok := c.list.FilteredItems()[1].(*CommandItem)
	require.True(t, ok)
	action, ok := item.Action().(ActionAttachSkill)
	require.True(t, ok)
	require.Equal(t, "deploy", action.Name)
	require.Equal(t, "/skills/deploy/SKILL.md", action.ID)
}

func TestCommandsSkillsPanelEmpty(t *testing.T) {
	t.Parallel()

	// No skills at all: the toggle row is still shown, followed by a note.
	c := newCommandsForTest(nil, nil, nil)
	c.setCommandItems(Skills)

	require.False(t, c.hasSkills())
	require.False(t, c.hasUserInvocableSkills())
	require.Equal(t, []string{skillScopeToggleID, skillScopeNoteID}, panelIDs(c.list.FilteredItems()))

	// Still empty once the user flips to "all skills".
	c.toggleSkillScope()
	require.Equal(t, []string{skillScopeToggleID, skillScopeNoteID}, panelIDs(c.list.FilteredItems()))
}

func TestCommandsSkillsPanelNonInvocableOnly(t *testing.T) {
	t.Parallel()

	c := newCommandsForTest(nil, nil, []skills.CatalogEntry{
		skillEntry("/skills/adhd/SKILL.md", "i-have-adhd", "Output style.", false),
	})

	c.setCommandItems(Skills)
	// Nothing invocable, so the note shows in the default scope.
	require.Equal(t, []string{skillScopeToggleID, skillScopeNoteID}, panelIDs(c.list.FilteredItems()))

	// But the override reveals it.
	c.toggleSkillScope()
	require.Equal(t, []string{skillScopeToggleID, "skill_/skills/adhd/SKILL.md"}, panelIDs(c.list.FilteredItems()))
}
