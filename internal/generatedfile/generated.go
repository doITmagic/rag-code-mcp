package generatedfile

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const Prefix = "<!-- ragcode:generated sha256="

func Marker(content string) string {
	hash := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%s%x -->\n", Prefix, hash)
}

func Valid(content string) bool {
	start := strings.Index(content, Prefix)
	if start < 0 {
		return false
	}
	end := strings.Index(content[start:], " -->\n")
	if end < 0 {
		return false
	}
	end += start
	want := content[start+len(Prefix) : end]
	plain := content[:start] + content[end+len(" -->\n"):]
	got := sha256.Sum256([]byte(plain))
	return want == fmt.Sprintf("%x", got)
}
