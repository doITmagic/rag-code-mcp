package scoring

import "testing"

func TestDetectMatchReasonsSymbolName(t *testing.T) {
	r := DetectMatchReasons("Calculator", "Calculator", "", "", "")
	if !r.SymbolName {
		t.Error("expected SymbolName=true")
	}
	if r.Signature || r.Content || r.Docstring {
		t.Error("expected other fields false")
	}
}

func TestDetectMatchReasonsMultipleFields(t *testing.T) {
	r := DetectMatchReasons("auth token", "authenticate", "func authenticate(token string)", "", "validates auth")
	if !r.SymbolName {
		t.Error("expected SymbolName=true (auth in authenticate)")
	}
	if !r.Signature {
		t.Error("expected Signature=true (token in signature)")
	}
	if !r.Docstring {
		t.Error("expected Docstring=true (auth in docstring)")
	}
	if r.Content {
		t.Error("expected Content=false")
	}
}

func TestDetectMatchReasonsCaseInsensitive(t *testing.T) {
	r := DetectMatchReasons("CALCULATOR", "Calculator", "", "", "")
	if !r.SymbolName {
		t.Error("expected case-insensitive match on SymbolName")
	}
}

func TestDetectMatchReasonsShortTokensIgnored(t *testing.T) {
	// tokens "ab" and "x" are ≤2 chars → filtered out → no matches
	r := DetectMatchReasons("ab x", "ab", "x", "ab x", "ab")
	if r.SymbolName || r.Signature || r.Content || r.Docstring {
		t.Error("short tokens should be filtered, no match expected")
	}
}

func TestDetectMatchReasonsEmptyQuery(t *testing.T) {
	r := DetectMatchReasons("", "Calculator", "func Calculator()", "body", "docs")
	if r.SymbolName || r.Signature || r.Content || r.Docstring {
		t.Error("empty query should produce no matches")
	}
}

func TestDetectMatchReasonsNoMatch(t *testing.T) {
	r := DetectMatchReasons("Payment", "UserAuth", "func UserAuth()", "body code", "user authenticates")
	if r.SymbolName || r.Signature || r.Content || r.Docstring {
		t.Error("payment not in any field, expected all false")
	}
}
