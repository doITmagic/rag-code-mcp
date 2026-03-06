package javascript

import (
	"testing"
)

func TestTreeSitter_ParseFunctions(t *testing.T) {
	source := []byte(`
function hello(name) {
    console.log(name);
}

export async function fetchData(url) {
    const res = await fetch(url);
    return res.json();
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.js")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if fa == nil {
		t.Fatal("expected non-nil result")
	}

	if len(fa.Functions) < 2 {
		t.Fatalf("expected at least 2 functions, got %d", len(fa.Functions))
	}

	// hello
	if fa.Functions[0].Name != "hello" {
		t.Errorf("expected 'hello', got '%s'", fa.Functions[0].Name)
	}

	// fetchData
	found := false
	for _, fn := range fa.Functions {
		if fn.Name == "fetchData" {
			found = true
			if !fn.IsExported {
				t.Error("fetchData should be exported")
			}
			if !fn.IsAsync {
				t.Error("fetchData should be async")
			}
		}
	}
	if !found {
		t.Error("expected fetchData function")
	}
}

func TestTreeSitter_ParseClasses(t *testing.T) {
	source := []byte(`
export class UserService extends BaseService {
    constructor(db) {
        super(db);
    }

    async getUser(id) {
        return this.db.find(id);
    }

    static create() {
        return new UserService();
    }
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.js")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Classes) < 1 {
		t.Fatalf("expected at least 1 class, got %d", len(fa.Classes))
	}

	cls := fa.Classes[0]
	if cls.Name != "UserService" {
		t.Errorf("expected 'UserService', got '%s'", cls.Name)
	}
	if cls.Extends != "BaseService" {
		t.Errorf("expected extends 'BaseService', got '%s'", cls.Extends)
	}
	if !cls.IsExported {
		t.Error("UserService should be exported")
	}
	if len(cls.Methods) < 2 {
		t.Errorf("expected at least 2 methods, got %d", len(cls.Methods))
	}
}

func TestTreeSitter_ParseTSInterface(t *testing.T) {
	source := []byte(`
export interface UserProps {
    name: string;
    age?: number;
    email: string;
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Interfaces) < 1 {
		t.Fatalf("expected at least 1 interface, got %d", len(fa.Interfaces))
	}

	iface := fa.Interfaces[0]
	if iface.Name != "UserProps" {
		t.Errorf("expected 'UserProps', got '%s'", iface.Name)
	}
	if !iface.IsExported {
		t.Error("UserProps should be exported")
	}
}

func TestTreeSitter_ParseArrowFunction(t *testing.T) {
	source := []byte(`
const add = (a, b) => a + b;

export const multiply = (x, y) => {
    return x * y;
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.js")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Functions) < 1 {
		t.Fatalf("expected at least 1 arrow function, got %d", len(fa.Functions))
	}

	foundAdd := false
	for _, fn := range fa.Functions {
		if fn.Name == "add" {
			foundAdd = true
			if !fn.IsArrow {
				t.Error("add should be arrow function")
			}
		}
	}
	if !foundAdd {
		t.Error("expected 'add' arrow function")
	}
}

func TestTreeSitter_ParseJSX(t *testing.T) {
	// JSX is where tree-sitter shines vs regex
	source := []byte(`
import React from 'react';

export function App({ name }) {
    const isActive = name.length > 0;
    
    return (
        <div className={isActive ? "active" : "inactive"}>
            <h1>{name}</h1>
            {isActive && <span>Active!</span>}
        </div>
    );
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.tsx")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Functions) < 1 {
		t.Fatalf("expected at least 1 function, got %d", len(fa.Functions))
	}

	found := false
	for _, fn := range fa.Functions {
		if fn.Name == "App" {
			found = true
			if !fn.IsExported {
				t.Error("App should be exported")
			}
		}
	}
	if !found {
		t.Error("expected App function")
	}
}

func TestTreeSitter_ParseEnum(t *testing.T) {
	source := []byte(`
export enum Direction {
    Up,
    Down,
    Left,
    Right,
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Enums) < 1 {
		t.Fatalf("expected at least 1 enum, got %d", len(fa.Enums))
	}

	if fa.Enums[0].Name != "Direction" {
		t.Errorf("expected 'Direction', got '%s'", fa.Enums[0].Name)
	}
}

func TestTreeSitter_ParseTypeAlias(t *testing.T) {
	source := []byte(`
export type UserID = string;
type Status = 'active' | 'inactive';
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Types) < 1 {
		t.Fatalf("expected at least 1 type alias, got %d", len(fa.Types))
	}

	found := false
	for _, ta := range fa.Types {
		if ta.Name == "UserID" && ta.IsExported {
			found = true
		}
	}
	if !found {
		t.Error("expected exported type alias UserID")
	}
}

func TestTreeSitter_ParseImports(t *testing.T) {
	source := []byte(`
import React from 'react';
import { useState, useEffect } from 'react';
import * as path from 'path';
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.tsx")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Imports) < 2 {
		t.Fatalf("expected at least 2 imports, got %d", len(fa.Imports))
	}

	foundDefault := false
	foundNamed := false
	for _, imp := range fa.Imports {
		if imp.Default == "React" && imp.Source == "react" {
			foundDefault = true
		}
		if len(imp.Named) > 0 && imp.Source == "react" {
			foundNamed = true
		}
	}
	if !foundDefault {
		t.Error("expected React default import")
	}
	if !foundNamed {
		t.Error("expected named imports from react")
	}
}
