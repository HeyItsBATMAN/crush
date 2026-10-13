package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInteractiveFlagAvailableOnRunCmd guards the --interactive flag on
// `crush run`: it opens the interactive TUI seeded with the given prompt
// instead of running one non-interactive turn.
func TestInteractiveFlagAvailableOnRunCmd(t *testing.T) {
	t.Parallel()

	flag := runCmd.Flags().Lookup("interactive")
	require.NotNil(t, flag, "the --interactive flag must be registered on runCmd")
	require.Equal(t, "bool", flag.Value.Type())
	require.Equal(t, "i", flag.Shorthand)

	// It is meaningless alongside the non-interactive output flags and the
	// session-selection flags, so cobra must reject those combinations.
	for _, name := range []string{"quiet", "verbose", "session", "continue"} {
		other := runCmd.Flags().Lookup(name)
		require.NotNil(t, other)
		joined := strings.Join(other.Annotations[cobraFlagMutuallyExclusiveAnnotation], " ")
		require.Contains(t, joined, "interactive",
			"--interactive and --%s must be mutually exclusive", name)
	}
}

// cobraFlagMutuallyExclusiveAnnotation is the annotation key cobra sets on
// flags registered via MarkFlagsMutuallyExclusive. Kept as a local constant so
// the test does not depend on cobra's (unexported) symbol.
const cobraFlagMutuallyExclusiveAnnotation = "cobra_annotation_mutually_exclusive"
