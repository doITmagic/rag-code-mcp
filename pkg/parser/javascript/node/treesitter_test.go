package node

import (
	"testing"
)

func TestTreeSitter_ExpressRoutes(t *testing.T) {
	source := []byte(`
const express = require('express');
const app = express();

app.get('/api/users', getUsers);
app.post('/api/users', createUser);
app.use('/api/admin', adminRouter);
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "server.js")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	if len(info.Routes) < 2 {
		t.Fatalf("expected at least 2 routes, got %d", len(info.Routes))
	}

	methods := make(map[string]bool)
	for _, r := range info.Routes {
		methods[r.Method] = true
	}
	if !methods["get"] {
		t.Error("expected GET route")
	}
	if !methods["post"] {
		t.Error("expected POST route")
	}
}

func TestTreeSitter_TypeScriptExpress(t *testing.T) {
	source := []byte(`
import express, { Request, Response } from 'express';

const app = express();

app.get('/api/products', (req: Request, res: Response) => {
    res.json({ products: [] });
});

app.post('/api/products', async (req: Request, res: Response) => {
    const body = req.body;
    res.status(201).json(body);
});
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "server.ts")
	if info == nil {
		t.Fatal("expected non-nil result for .ts file")
	}

	if len(info.Routes) < 2 {
		t.Fatalf("expected at least 2 routes in TS file, got %d", len(info.Routes))
	}
}

func TestTreeSitter_Requires(t *testing.T) {
	source := []byte(`
const express = require('express');
const { Router } = require('express');
const path = require('path');
const userService = require('./services/userService');
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "app.js")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	if len(info.Requires) < 3 {
		t.Fatalf("expected at least 3 requires, got %d", len(info.Requires))
	}

	foundLocal := false
	for _, r := range info.Requires {
		if r.IsLocal && r.Module == "./services/userService" {
			foundLocal = true
		}
	}
	if !foundLocal {
		t.Error("expected local require for userService")
	}
}

func TestTreeSitter_ModuleExports(t *testing.T) {
	source := []byte(`
const db = require('./db');

function getUser(id) {
    return db.find(id);
}

module.exports = getUser;
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "user.js")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	if len(info.ModuleExports) < 1 {
		t.Fatalf("expected at least 1 module export, got %d", len(info.ModuleExports))
	}
}

func TestTreeSitter_Middleware(t *testing.T) {
	source := []byte(`
const express = require('express');
const cors = require('cors');
const app = express();

app.use(cors());
app.use(express.json());
app.use(customMiddleware);
`)
	analyzer := NewTreeSitterAnalyzer()
	info := analyzer.Analyze(source, "server.js")
	if info == nil {
		t.Fatal("expected non-nil result")
	}

	if len(info.Middleware) < 1 {
		t.Fatalf("expected at least 1 middleware, got %d", len(info.Middleware))
	}
}
