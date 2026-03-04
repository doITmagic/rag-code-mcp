package javascript

import (
	"testing"
)

func TestExtractFunctions_Classic(t *testing.T) {
	source := `
function hello(name) {
    console.log(name);
}

export function greet(name, age) {
    return name + age;
}

export default function main() {
    hello("world");
}

async function fetchData(url) {
    const res = await fetch(url);
    return res.json();
}
`
	SetSourceCache(source)
	fns := ExtractFunctions(source, "test.js")

	if len(fns) != 4 {
		t.Fatalf("expected 4 functions, got %d", len(fns))
	}

	// hello
	if fns[0].Name != "hello" {
		t.Errorf("expected 'hello', got '%s'", fns[0].Name)
	}
	if fns[0].IsExported {
		t.Error("hello should not be exported")
	}

	// greet
	if fns[1].Name != "greet" {
		t.Errorf("expected 'greet', got '%s'", fns[1].Name)
	}
	if !fns[1].IsExported {
		t.Error("greet should be exported")
	}
	if len(fns[1].Params) != 2 {
		t.Errorf("greet expected 2 params, got %d", len(fns[1].Params))
	}

	// main
	if fns[2].Name != "main" {
		t.Errorf("expected 'main', got '%s'", fns[2].Name)
	}
	if !fns[2].IsDefault {
		t.Error("main should be default export")
	}

	// fetchData
	if fns[3].Name != "fetchData" {
		t.Errorf("expected 'fetchData', got '%s'", fns[3].Name)
	}
	if !fns[3].IsAsync {
		t.Error("fetchData should be async")
	}
}

func TestExtractFunctions_Arrow(t *testing.T) {
	source := `
const add = (a, b) => {
    return a + b;
}

export const multiply = (a, b) => a * b;

export default const handler = async (req, res) => {
    res.send("ok");
}
`
	SetSourceCache(source)
	fns := ExtractFunctions(source, "test.js")

	if len(fns) < 2 {
		t.Fatalf("expected at least 2 arrow functions, got %d", len(fns))
	}

	if fns[0].Name != "add" {
		t.Errorf("expected 'add', got '%s'", fns[0].Name)
	}
	if !fns[0].IsArrow {
		t.Error("add should be arrow function")
	}

	if fns[1].Name != "multiply" {
		t.Errorf("expected 'multiply', got '%s'", fns[1].Name)
	}
	if !fns[1].IsExported {
		t.Error("multiply should be exported")
	}
}

func TestExtractClasses(t *testing.T) {
	source := `
export class UserService extends BaseService {
    constructor(db) {
        super(db);
    }

    async getUser(id) {
        return this.db.find(id);
    }

    static create(data) {
        return new UserService(data);
    }

    #validate(input) {
        return input != null;
    }
}

class SimpleClass {
    hello() {
        console.log("hello");
    }
}
`
	SetSourceCache(source)
	classes := ExtractClasses(source, "test.ts")

	if len(classes) != 2 {
		t.Fatalf("expected 2 classes, got %d", len(classes))
	}

	// UserService
	cls := classes[0]
	if cls.Name != "UserService" {
		t.Errorf("expected 'UserService', got '%s'", cls.Name)
	}
	if cls.Extends != "BaseService" {
		t.Errorf("expected extends 'BaseService', got '%s'", cls.Extends)
	}
	if !cls.IsExported {
		t.Error("UserService should be exported")
	}
	if len(cls.Methods) < 3 {
		t.Fatalf("expected at least 3 methods, got %d", len(cls.Methods))
	}

	// Check constructor
	foundConstructor := false
	for _, m := range cls.Methods {
		if m.Name == "constructor" {
			foundConstructor = true
		}
		if m.Name == "getUser" && !m.IsAsync {
			t.Error("getUser should be async")
		}
		if m.Name == "create" && !m.IsStatic {
			t.Error("create should be static")
		}
	}
	if !foundConstructor {
		t.Error("expected constructor method")
	}
}

func TestExtractClasses_Implements(t *testing.T) {
	source := `
export abstract class Repository extends Base implements Readable, Writable {
    abstract find(id: string): Promise<Entity>;
}
`
	SetSourceCache(source)
	classes := ExtractClasses(source, "test.ts")

	if len(classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(classes))
	}

	cls := classes[0]
	if !cls.IsAbstract {
		t.Error("Repository should be abstract")
	}
	if len(cls.Implements) != 2 {
		t.Errorf("expected 2 implements, got %d: %v", len(cls.Implements), cls.Implements)
	}
}

func TestExtractTSInterfaces(t *testing.T) {
	source := `
export interface UserProps extends BaseProps {
    name: string;
    age?: number;
    email: string;
}

interface Config {
    debug: boolean;
    port: number;
}
`
	SetSourceCache(source)
	interfaces := ExtractTSInterfaces(source, "test.ts")

	if len(interfaces) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(interfaces))
	}

	// UserProps
	iface := interfaces[0]
	if iface.Name != "UserProps" {
		t.Errorf("expected 'UserProps', got '%s'", iface.Name)
	}
	if !iface.IsExported {
		t.Error("UserProps should be exported")
	}
	if len(iface.Extends) != 1 || iface.Extends[0] != "BaseProps" {
		t.Errorf("expected extends BaseProps, got %v", iface.Extends)
	}
	if len(iface.Properties) != 3 {
		t.Fatalf("expected 3 properties, got %d", len(iface.Properties))
	}
	// Check optional
	foundOptional := false
	for _, p := range iface.Properties {
		if p.Name == "age" && p.Optional {
			foundOptional = true
		}
	}
	if !foundOptional {
		t.Error("age should be optional")
	}
}

func TestExtractTSTypeAliases(t *testing.T) {
	source := `
export type UserID = string;
type Status = 'active' | 'inactive' | 'banned';
export type Handler = (req: Request, res: Response) => void;
`
	SetSourceCache(source)
	types := ExtractTSTypeAliases(source, "test.ts")

	if len(types) != 3 {
		t.Fatalf("expected 3 types, got %d", len(types))
	}

	if types[0].Name != "UserID" {
		t.Errorf("expected 'UserID', got '%s'", types[0].Name)
	}
	if !types[0].IsExported {
		t.Error("UserID should be exported")
	}
	if types[0].Definition != "string" {
		t.Errorf("expected definition 'string', got '%s'", types[0].Definition)
	}

	if types[1].Name != "Status" {
		t.Errorf("expected 'Status', got '%s'", types[1].Name)
	}
}

func TestExtractTSEnums(t *testing.T) {
	source := `
export enum Direction {
    Up,
    Down,
    Left,
    Right,
}

const enum Color {
    Red,
    Green,
    Blue,
}
`
	SetSourceCache(source)
	enums := ExtractTSEnums(source, "test.ts")

	if len(enums) != 2 {
		t.Fatalf("expected 2 enums, got %d", len(enums))
	}

	if enums[0].Name != "Direction" {
		t.Errorf("expected 'Direction', got '%s'", enums[0].Name)
	}
	if !enums[0].IsExported {
		t.Error("Direction should be exported")
	}
	if len(enums[0].Members) != 4 {
		t.Errorf("expected 4 members, got %d: %v", len(enums[0].Members), enums[0].Members)
	}

	if enums[1].Name != "Color" {
		t.Errorf("expected 'Color', got '%s'", enums[1].Name)
	}
	if !enums[1].IsConst {
		t.Error("Color should be const enum")
	}
}

func TestExtractImports(t *testing.T) {
	source := `
import React from 'react';
import { useState, useEffect } from 'react';
import * as path from 'path';
import './styles.css';
const express = require('express');
const { Router } = require('express');
`
	imports := ExtractImports(source)

	if len(imports) < 5 {
		t.Fatalf("expected at least 5 imports, got %d", len(imports))
	}

	// Default import
	found := false
	for _, imp := range imports {
		if imp.Default == "React" && imp.Source == "react" {
			found = true
		}
	}
	if !found {
		t.Error("expected React default import from 'react'")
	}

	// Named imports
	found = false
	for _, imp := range imports {
		if imp.Source == "react" && len(imp.Named) >= 2 {
			found = true
		}
	}
	if !found {
		t.Error("expected named imports from 'react'")
	}

	// Namespace import
	found = false
	for _, imp := range imports {
		if imp.Namespace == "path" && imp.Source == "path" {
			found = true
		}
	}
	if !found {
		t.Error("expected namespace import 'path'")
	}

	// require
	found = false
	for _, imp := range imports {
		if imp.Default == "express" && imp.Source == "express" {
			found = true
		}
	}
	if !found {
		t.Error("expected require('express')")
	}
}

func TestExtractExports(t *testing.T) {
	source := `
export { UserService, AuthService };
export default App;
`
	exports := ExtractExports(source)

	if len(exports) < 3 {
		t.Fatalf("expected at least 3 exports, got %d", len(exports))
	}

	// Check named exports
	foundUser := false
	foundDefault := false
	for _, exp := range exports {
		if exp.Name == "UserService" {
			foundUser = true
		}
		if exp.Name == "App" && exp.IsDefault {
			foundDefault = true
		}
	}
	if !foundUser {
		t.Error("expected UserService named export")
	}
	if !foundDefault {
		t.Error("expected App default export")
	}
}

func TestBuildFunctionSignature(t *testing.T) {
	tests := []struct {
		fn       JSFunction
		expected string
	}{
		{
			fn:       JSFunction{Name: "hello", Params: []string{"name"}},
			expected: "function hello(name)",
		},
		{
			fn:       JSFunction{Name: "greet", Params: []string{"a", "b"}, IsExported: true, IsAsync: true},
			expected: "export async function greet(a, b)",
		},
		{
			fn:       JSFunction{Name: "add", Params: []string{"a", "b"}, IsArrow: true},
			expected: "const add = (a, b) =>",
		},
	}

	for _, tt := range tests {
		result := buildFunctionSignature(tt.fn)
		if result != tt.expected {
			t.Errorf("expected '%s', got '%s'", tt.expected, result)
		}
	}
}

func TestParseParams(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"a, b, c", []string{"a", "b", "c"}},
		{"name: string, age: number", []string{"name", "age"}},
		{"x = 10, y = 20", []string{"x", "y"}},
		{"...args", []string{"args"}},
	}

	for _, tt := range tests {
		result := parseParams(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("parseParams(%q): expected %v, got %v", tt.input, tt.expected, result)
			continue
		}
		for i, v := range result {
			if v != tt.expected[i] {
				t.Errorf("parseParams(%q)[%d]: expected '%s', got '%s'", tt.input, i, tt.expected[i], v)
			}
		}
	}
}

func TestFindClosingBrace(t *testing.T) {
	lines := []string{
		"function test() {",
		"    if (true) {",
		"        console.log('hello');",
		"    }",
		"}",
	}

	result := findClosingBrace(lines, 0)
	if result != 5 {
		t.Errorf("expected line 5, got %d", result)
	}

	result = findClosingBrace(lines, 1)
	if result != 4 {
		t.Errorf("expected line 4, got %d", result)
	}
}
