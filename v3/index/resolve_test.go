package index

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A non-fresh run (resume, big pull) whose files call many symbols already
// in the store must resolve every one of them, not only the first 500.
func TestRunResolvesBeyondFiveHundredTargets(t *testing.T) {
	e := newEnv(t)
	var defs, calls []string
	for i := 0; i < 600; i++ {
		defs = append(defs, fmt.Sprintf("func T%d", i))
		calls = append(calls, fmt.Sprintf("func F%d -> b.T%d", i, i))
	}
	e.write(t, "b.go", strings.Join(defs, "\n"))
	_, err := e.run(t, nil)
	require.NoError(t, err)

	a := e.write(t, "a.go", strings.Join(calls, "\n"))
	_, err = e.run(t, nil)
	require.NoError(t, err)
	unresolved := 0
	for _, s := range e.symbols(t, a) {
		if s.Relations[0].TargetID == "" {
			unresolved++
		}
	}
	assert.Equal(t, 0, unresolved)

	// A later run without changes does not repair them.
	_, err = e.run(t, nil)
	require.NoError(t, err)
	unresolved = 0
	for _, s := range e.symbols(t, a) {
		if s.Relations[0].TargetID == "" {
			unresolved++
		}
	}
	assert.Equal(t, 0, unresolved, "after an unchanged rerun")
}
