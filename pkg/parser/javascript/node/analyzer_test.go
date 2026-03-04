package node

import (
	"testing"
)

func TestAnalyzer_ExpressRoutes(t *testing.T) {
	source := `
const express = require('express');
const app = express();

app.get('/api/users', getUsers);
app.post('/api/users', createUser);
app.put('/api/users/:id', updateUser);
app.delete('/api/users/:id', deleteUser);
app.use('/api', apiRouter);
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "server.js")

	if len(info.Routes) != 5 {
		t.Fatalf("expected 5 routes, got %d", len(info.Routes))
	}

	expected := map[string]string{
		"/api/users":     "get",
		"/api/users/:id": "put",
		"/api":           "use",
	}

	for path, method := range expected {
		found := false
		for _, r := range info.Routes {
			if r.Path == path && r.Method == method {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected route %s %s", method, path)
		}
	}
}

func TestAnalyzer_RouterRoutes(t *testing.T) {
	source := `
const express = require('express');
const router = express.Router();

router.get('/', listItems);
router.post('/', createItem);
router.get('/:id', getItem);
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "routes.js")

	if len(info.Routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(info.Routes))
	}

	// All should be router-level
	for _, r := range info.Routes {
		if !r.IsRouter {
			t.Errorf("route %s %s should be router-level", r.Method, r.Path)
		}
	}
}

func TestAnalyzer_Middleware(t *testing.T) {
	source := `
const express = require('express');
const app = express();

app.use(express.json());
app.use(express.urlencoded({ extended: true }));
app.use(cors());
app.use(customMiddleware());
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "app.js")

	if len(info.Middleware) < 3 {
		t.Fatalf("expected at least 3 middleware, got %d", len(info.Middleware))
	}

	// Check built-in vs custom
	for _, m := range info.Middleware {
		switch m.Name {
		case "express.json", "express.urlencoded", "cors":
			if m.IsCustom {
				t.Errorf("%s should not be custom", m.Name)
			}
		case "customMiddleware":
			if !m.IsCustom {
				t.Errorf("%s should be custom", m.Name)
			}
		}
	}
}

func TestAnalyzer_Requires(t *testing.T) {
	source := `
const express = require('express');
const { Router } = require('express');
const path = require('path');
const utils = require('./utils');
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "app.js")

	if len(info.Requires) != 4 {
		t.Fatalf("expected 4 requires, got %d", len(info.Requires))
	}

	// Check local detection
	foundLocal := false
	for _, r := range info.Requires {
		if r.Module == "./utils" && r.IsLocal {
			foundLocal = true
		}
	}
	if !foundLocal {
		t.Error("expected ./utils as local require")
	}

	// Check destructured
	foundDestructured := false
	for _, r := range info.Requires {
		if r.Module == "express" && len(r.Binding) > 0 && r.Binding[0] == '{' {
			foundDestructured = true
		}
	}
	if !foundDestructured {
		t.Error("expected destructured require for express Router")
	}
}

func TestAnalyzer_ModuleExports(t *testing.T) {
	source := `
module.exports = {
    getUser,
    createUser,
};

exports.helper = function() {};
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "utils.js")

	if len(info.ModuleExports) != 2 {
		t.Fatalf("expected 2 exports, got %d", len(info.ModuleExports))
	}

	// module.exports = { ... } should be "object"
	if info.ModuleExports[0].Type != "object" {
		t.Errorf("expected type 'object', got '%s'", info.ModuleExports[0].Type)
	}

	// exports.helper should be "property"
	if info.ModuleExports[1].Name != "helper" {
		t.Errorf("expected 'helper', got '%s'", info.ModuleExports[1].Name)
	}
	if info.ModuleExports[1].Type != "property" {
		t.Errorf("expected type 'property', got '%s'", info.ModuleExports[1].Type)
	}
}

func TestIsNodeProject(t *testing.T) {
	tests := []struct {
		source   string
		expected bool
	}{
		{`const x = require('express');`, true},
		{`module.exports = {};`, true},
		{`import React from 'react';`, false},
		{`console.log("hello")`, false},
	}

	for _, tt := range tests {
		result := IsNodeProject(tt.source)
		if result != tt.expected {
			t.Errorf("IsNodeProject(%q...) = %v, want %v", tt.source[:20], result, tt.expected)
		}
	}
}

func TestAnalyzer_EmptyFile(t *testing.T) {
	analyzer := NewAnalyzer()
	info := analyzer.Analyze("// empty file", "empty.js")

	if len(info.Routes) != 0 || len(info.Middleware) != 0 ||
		len(info.Requires) != 0 || len(info.ModuleExports) != 0 {
		t.Error("expected empty results for empty file")
	}
}
