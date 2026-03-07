package indexer

import (
	"encoding/json"
	"testing"

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
