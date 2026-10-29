package php

import (
	"testing"
	"time"

	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/version"
)

func TestParseTerminatesOnTrailingLessThan(t *testing.T) {
	for _, src := range []string{"x<", "<", "x<\n", "x<\n\n", "x<\r", "x<\r\n", "a<b<", "<?php echo 1; ?>\n<\n"} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() { _ = recover() }()
			_, _ = Parse([]byte(src), conf.Config{Version: &version.Version{Major: 8, Minor: 1}})
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			// The runaway scanner allocates without bound; stop the process now.
			panic("php parser did not terminate for " + src)
		}
	}
}
