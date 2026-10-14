package python

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPythonAnalyzer_Advanced(t *testing.T) {
	tmpDir := t.TempDir()

	code := `"""Module Level Doc"""
import os
from typing import List, Optional

VERSION = "1.2.3"
count = 0

@dataclass
class User:
    """A user class"""
    id: int
    name: str = "Anonymous"

    async def get_details(self) -> dict:
        return {"id": self.id}

def global_generator():
    yield 1
    yield 2

class BaseMixin:
    pass

class MyAdmin(User, BaseMixin):
    @staticmethod
    def log(msg: str):
        print(msg)
`
	filePath := filepath.Join(tmpDir, "app.py")
	err := os.WriteFile(filePath, []byte(code), 0644)
	require.NoError(t, err)

	analyzer := NewAnalyzer()

	t.Run("Basic checks", func(t *testing.T) {
		assert.Equal(t, "python", analyzer.Name())
		assert.True(t, analyzer.CanHandle("main.py"))
		assert.False(t, analyzer.CanHandle("main.go"))
	})

	t.Run("Analyze File", func(t *testing.T) {
		res, err := analyzer.Analyze(context.Background(), filePath)
		require.NoError(t, err)
		assert.Equal(t, "python", res.Language)

		symbols := make(map[string]bool)
		for _, s := range res.Symbols {
			symbols[s.Name] = true
			if s.Name == "app" {
				assert.Equal(t, "Module Level Doc", s.Docstring)
			}
			if s.Name == "User" {
				assert.Equal(t, true, s.Metadata["is_dataclass"])
				assert.Equal(t, "A user class", s.Docstring)
			}
			if s.Name == "get_details" {
				assert.Equal(t, true, s.Metadata["is_async"])
				assert.Equal(t, "User", s.Metadata["class"])
			}
			if s.Name == "global_generator" {
				assert.Equal(t, true, s.Metadata["is_generator"])
			}
			if s.Name == "VERSION" {
				assert.Equal(t, "const", string(s.Type))
			}
			if s.Name == "MyAdmin" {
				assert.True(t, s.Metadata["is_mixin"].(bool))
				bases := s.Metadata["bases"].([]string)
				assert.Contains(t, bases, "User")
				assert.Contains(t, bases, "BaseMixin")
			}
		}

		assert.True(t, symbols["app"])
		assert.True(t, symbols["User"])
		assert.True(t, symbols["get_details"])
		assert.True(t, symbols["global_generator"])
		assert.True(t, symbols["VERSION"])
		assert.True(t, symbols["count"])
		assert.True(t, symbols["MyAdmin"])
		assert.True(t, symbols["log"])
	})

	t.Run("Skip tests", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "test_app.py")
		require.NoError(t, os.WriteFile(testFile, []byte("def test_one(): pass"), 0644))

		res, err := analyzer.Analyze(context.Background(), tmpDir)
		require.NoError(t, err)
		for _, s := range res.Symbols {
			assert.NotEqual(t, "test_one", s.Name)
		}
	})

	t.Run("Directory with hidden and venv", func(t *testing.T) {
		venvDir := filepath.Join(tmpDir, "venv")
		require.NoError(t, os.Mkdir(venvDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(venvDir, "lib.py"), []byte("x = 1"), 0644))

		res, err := analyzer.Analyze(context.Background(), tmpDir)
		require.NoError(t, err)
		for _, s := range res.Symbols {
			assert.NotEqual(t, "venv.lib", s.Name)
		}
	})

	t.Run("Complexity and edge cases", func(t *testing.T) {
		complexCode := `
class MyMeta(type):
    pass

class Complex(metaclass=MyMeta):
    def action(self, data: List[Dict[str, User]]) -> Optional[Complex]:
        self.other_method()
        super().some_method()
        cls.factory()
        User.builtin_call()
        print("Hello")
        return None

    def other_method(self):
        pass

    @classmethod
    def factory(cls):
        pass
`
		path := filepath.Join(tmpDir, "complex.py")
		require.NoError(t, os.WriteFile(path, []byte(complexCode), 0644))

		res, err := analyzer.Analyze(context.Background(), path)
		require.NoError(t, err)

		var complexClass pkgParser.Symbol
		for _, s := range res.Symbols {
			if s.Name == "Complex" {
				complexClass = s
			}
		}

		assert.Equal(t, "MyMeta", complexClass.Metadata["metaclass"])
		deps := complexClass.Metadata["dependencies"].([]string)
		assert.Contains(t, deps, "MyMeta")
		assert.Contains(t, deps, "User")
	})

	t.Run("CRLF files preserve decorators and strip CR from Content", func(t *testing.T) {
		crlfCode := "class Service:\r\n    @staticmethod\r\n    def ping() -> str:\r\n        return \"pong\"\r\n"
		path := filepath.Join(tmpDir, "crlf_service.py")
		require.NoError(t, os.WriteFile(path, []byte(crlfCode), 0644))

		res, err := analyzer.Analyze(context.Background(), path)
		require.NoError(t, err)

		var pingSym *pkgParser.Symbol
		for i := range res.Symbols {
			if res.Symbols[i].Name == "ping" {
				pingSym = &res.Symbols[i]
			}
		}
		require.NotNil(t, pingSym, "ping method must be extracted from CRLF file")
		assert.Equal(t, true, pingSym.Metadata["is_static"], "decorator @staticmethod must be preserved on CRLF file")
		assert.NotContains(t, pingSym.Content, "\r", "extracted Content must not contain carriage returns")
	})

	t.Run("Multi-line signatures, call defaults, return hints with spaces, and Unicode names", func(t *testing.T) {
		sigCode := `from typing import Tuple, Dict, Any, Optional

def multi_line_func(
    items: list = list(),
    config: Dict[str, Any] = dict(a=1, b=2),
) -> Tuple[int, str]:
    """Multi-line function docstring."""
    return 0, "ok"

async def données_utilisateur(nom: str = str()) -> Optional[Tuple[str, int]]:
    return nom, 1

class Processor:
    def transform(
        self,
        payload: dict = dict(),
    ) -> Tuple[bool, str]:
        """Transform payload."""
        def _inner_helper(x: int) -> int:
            return x + 1
        return True, str(_inner_helper(1))

    class InnerClass:
        def inner_only(self) -> None:
            pass
`
		path := filepath.Join(tmpDir, "signatures.py")
		require.NoError(t, os.WriteFile(path, []byte(sigCode), 0644))

		res, err := analyzer.Analyze(context.Background(), path)
		require.NoError(t, err)

		byName := make(map[string]pkgParser.Symbol)
		var methodNames []string
		for _, s := range res.Symbols {
			byName[s.Name] = s
			if s.Type == "method" {
				methodNames = append(methodNames, s.Name)
			}
		}

		require.Contains(t, byName, "multi_line_func")
		mlf := byName["multi_line_func"]
		assert.Equal(t, "def multi_line_func(items: list, config: Dict[str, Any]) -> Tuple[int, str]", mlf.Signature)
		assert.Equal(t, "Multi-line function docstring.", mlf.Docstring)
		assert.Contains(t, mlf.Content, `return 0, "ok"`)

		require.Contains(t, byName, "données_utilisateur")
		uniFn := byName["données_utilisateur"]
		assert.Equal(t, "async def données_utilisateur(nom: str) -> Optional[Tuple[str, int]]", uniFn.Signature)
		assert.Equal(t, true, uniFn.Metadata["is_async"])

		require.Contains(t, byName, "transform")
		tr := byName["transform"]
		assert.Equal(t, "def transform(self, payload: dict) -> Tuple[bool, str]", tr.Signature)
		assert.Equal(t, "Transform payload.", tr.Docstring)

		assert.NotContains(t, methodNames, "_inner_helper", "nested helper def inside a method must not be extracted as a class method")
		assert.NotContains(t, methodNames, "inner_only", "inner class method must not be attributed as a direct method of outer class")
	})

	t.Run("Multiline triple-quoted strings at column 0 do not truncate blocks", func(t *testing.T) {
		sqlCode := `class Repository:
    def fetch_users(self) -> list:
        query = """
SELECT id, name
FROM users
WHERE active = 1
"""
        return self.execute(query)

    def execute(self, q: str) -> list:
        return []
`
		path := filepath.Join(tmpDir, "repo.py")
		require.NoError(t, os.WriteFile(path, []byte(sqlCode), 0644))

		res, err := analyzer.Analyze(context.Background(), path)
		require.NoError(t, err)

		byName := make(map[string]pkgParser.Symbol)
		for _, s := range res.Symbols {
			byName[s.Name] = s
		}

		require.Contains(t, byName, "Repository")
		require.Contains(t, byName, "fetch_users")
		require.Contains(t, byName, "execute", "method after multiline SQL string must still be extracted inside Repository")

		fetchSym := byName["fetch_users"]
		assert.Contains(t, fetchSym.Content, "return self.execute(query)", "fetch_users block must include lines after column-0 multiline string")
	})
}

