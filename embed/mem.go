package embed

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// ErrLowMemory is returned instead of calling Ollama when the machine has
// too little free memory to load the model: the load would only time out,
// be aborted and start again on the next retry.
var ErrLowMemory = errors.New("embed: not enough free memory to run the embedding model")

// warmFor is how long after a successful call the model is taken as still
// loaded (Ollama keeps it 5 minutes): no new memory is needed then.
const warmFor = 4 * time.Minute

// freeMB is a variable so tests can fake the machine.
var freeMB = availableMB

// checkMemory returns ErrLowMemory when a local Ollama would have to load the
// model with less than MinFreeMB of memory available.
func (o *Ollama) checkMemory() error {
	if o.MinFreeMB <= 0 || !o.local() || time.Since(time.Unix(o.lastOK.Load(), 0)) < warmFor {
		return nil
	}
	free, ok := freeMB()
	if !ok || free >= o.MinFreeMB {
		return nil
	}
	return fmt.Errorf("%w: %d MB free, at least %d MB needed (min_free_memory_mb): close applications to free memory, then retry", ErrLowMemory, free, o.MinFreeMB)
}

// local reports whether Ollama runs on this machine, where its memory is ours.
func (o *Ollama) local() bool {
	u, err := url.Parse(o.base)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
