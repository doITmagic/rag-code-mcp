package php

import (
	"strings"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

var routeMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true,
	"any": true, "match": true, "resource": true, "group": true,
}

// frameworkSymbols derives Laravel route and WordPress hook symbols from the
// calls seen while walking. The call shape is the only evidence: no project
// detection.
func frameworkSymbols(calls []rawCall) []parser.Symbol {
	var out []parser.Symbol
	for _, call := range calls {
		if call.arg == "" {
			continue
		}
		s := parser.Symbol{
			Name: call.arg, Module: call.ns, Owner: call.owner, Content: call.content,
			StartLine: call.start, EndLine: call.end, Exported: true,
		}
		s.Signature, _, _ = strings.Cut(call.content, "\n")
		switch {
		case (call.recv == "Route" || call.recvFQN == `Illuminate\Support\Facades\Route`) && routeMethods[call.name]:
			s.Kind = parser.KindRoute
			s.Metadata = map[string]string{"framework": "laravel", "method": call.name}
		case call.recv == "" && (call.name == "add_action" || call.name == "add_filter"):
			s.Kind = parser.KindHook
			s.Metadata = map[string]string{"framework": "wordpress", "kind": strings.TrimPrefix(call.name, "add_")}
		default:
			continue
		}
		out = append(out, s)
	}
	return out
}
