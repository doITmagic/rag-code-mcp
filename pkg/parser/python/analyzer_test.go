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
}
