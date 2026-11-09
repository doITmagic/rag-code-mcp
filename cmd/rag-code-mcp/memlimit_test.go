package main

import (
	"math"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetMemoryLimit(t *testing.T) {
	old := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(old) })

	t.Setenv("GOMEMLIMIT", "")
	debug.SetMemoryLimit(math.MaxInt64)
	setMemoryLimit()
	assert.Equal(t, int64(1<<30), debug.SetMemoryLimit(-1))

	t.Setenv("GOMEMLIMIT", "2GiB") // the runtime already applied it; leave it alone
	debug.SetMemoryLimit(math.MaxInt64)
	setMemoryLimit()
	assert.Equal(t, int64(math.MaxInt64), debug.SetMemoryLimit(-1))
}
