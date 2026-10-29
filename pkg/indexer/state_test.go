package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateHasNoLastPercent(t *testing.T) {
	s := NewState()
	data, err := json.Marshal(s)
	require.NoError(t, err)
	// LastPercent must NOT be serialized — progress tracking moved to index_status.json
	assert.NotContains(t, string(data), "last_percent")
}

// A daemon killed while it saves the state (kill -9, OOM kill) must leave
// the previous state readable, not a truncated file that forces a full
// re-embed of the collection on the next run.
func TestStateSaveSurvivesKill(t *testing.T) {
	if path := os.Getenv("RAGCODE_TEST_STATE_SAVE_LOOP"); path != "" {
		s := NewState()
		for i := range 20000 {
			name := fmt.Sprintf("/workspace/pkg/module%05d/file.go", i)
			s.Files[name] = FileState{Path: name, ModTime: time.Now(), Size: int64(i)}
		}
		for {
			if err := s.Save(path); err != nil {
				os.Exit(1)
			}
		}
	}
	path := StatePath(t.TempDir(), "kill")
	require.NoError(t, NewState().Save(path))
	for attempt := range 20 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStateSaveSurvivesKill$")
		cmd.Env = append(os.Environ(), "RAGCODE_TEST_STATE_SAVE_LOOP="+path)
		require.NoError(t, cmd.Start())
		time.Sleep(time.Duration(150+attempt*7) * time.Millisecond)
		require.NoError(t, cmd.Process.Kill())
		_ = cmd.Wait()
		_, err := LoadState(path)
		require.NoError(t, err, "state unreadable after a kill during save (attempt %d)", attempt)
	}
}
