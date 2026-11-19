package embed

import (
	"errors"
	"fmt"
)

// ErrLowMemory is returned instead of starting the server when the machine
// has too little free memory to load the model: the load would only swap,
// or be killed, and start again on the next call.
var ErrLowMemory = errors.New("embed: not enough free memory to run the embedding model")

// freeMB is a variable so tests can fake the machine.
var freeMB = availableMB

// checkMemory returns ErrLowMemory when the model would have to be loaded
// with less than factor times MinFreeMB of memory available: 1 for a call
// that needs the model, 2 for a warm-up that nobody waits for. A running server
// holds the model already, so only start asks.
func (l *Llama) checkMemory(factor int) error {
	need := factor * l.MinFreeMB
	if l.MinFreeMB <= 0 {
		return nil
	}
	free, ok := freeMB()
	if !ok || free >= need {
		return nil
	}
	return fmt.Errorf("%w: %d MB free, at least %d MB needed (min_free_memory_mb): close applications to free memory, then retry", ErrLowMemory, free, need)
}
