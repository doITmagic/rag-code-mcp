package tests

import (
	"testing"

	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/php"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/python"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTools(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tools Suite")
}
