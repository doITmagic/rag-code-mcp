package php

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pkgParser "github.com/doITmagic/rag-code-mcp/v2/pkg/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzer_V2Interface(t *testing.T) {
	tmpDir := t.TempDir()
	ca := NewAnalyzer()

	t.Run("Interface", func(t *testing.T) {
		assert.Equal(t, "php", ca.Name())
		assert.True(t, ca.CanHandle("test.php"))
		assert.False(t, ca.CanHandle("test.html"))
	})

	t.Run("Analyze", func(t *testing.T) {
		code := `<?php
class User {
    public function login() {}
}
`
		phpFile := filepath.Join(tmpDir, "User.php")
		err := os.WriteFile(phpFile, []byte(code), 0644)
		require.NoError(t, err)

		res, err := ca.Analyze(context.Background(), phpFile)
		require.NoError(t, err)
		assert.Equal(t, "php", res.Language)

		foundUser := false
		foundLogin := false
		for _, sym := range res.Symbols {
			if sym.Name == "User" && sym.Type == pkgParser.Class {
				foundUser = true
			}
			if sym.Name == "login" && sym.Type == pkgParser.Method {
				foundLogin = true
			}
		}
		assert.True(t, foundUser)
		assert.True(t, foundLogin)
	})

	t.Run("Registry", func(t *testing.T) {
		a := pkgParser.GetByName("php")
		assert.NotNil(t, a)
		assert.Equal(t, "php", a.Name())
	})
}
