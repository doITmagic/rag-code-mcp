package parser

import (
	"strings"
	"testing"
)

// The nesting guard is for code grammars only: brackets mean nothing to
// HTML, so a valid page whose inline script has 1001 "(" in a string must
// still be parsed.
func TestNestingGuardSkipsHTML(t *testing.T) {
	src := "<html><body>\n<div id=\"cart\">x</div>\n<script>var s = \"" + strings.Repeat("(", 1001) + "\";</script>\n</body></html>\n"
	if _, _, err := ParseTree("html", []byte(src)); err != nil {
		t.Fatalf("html refused: %v", err)
	}
}
