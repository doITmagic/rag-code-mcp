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

func TestTreeSitter_ExportedFunctionsKeepJSDoc(t *testing.T) {
	source := []byte(`/** Adds an item to the cart. */
export async function addToCart(item) { return item; }

/** Applies a coupon code. */
export const applyCouponCode = (code) => code;
`)
	fa, err := NewTreeSitterParser().ParseFile(source, "cart.ts")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"addToCart": "Adds an item to the cart.", "applyCouponCode": "Applies a coupon code."}
	for _, fn := range fa.Functions {
		if doc, ok := want[fn.Name]; ok {
			if fn.Docstring != doc {
				t.Errorf("%s docstring = %q, want %q", fn.Name, fn.Docstring, doc)
			}
			delete(want, fn.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing exported functions: %v", want)
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

func TestTreeSitter_AnonymousDefaultExports(t *testing.T) {
	parser := NewTreeSitterParser()

	t.Run("anonymous default function", func(t *testing.T) {
		fa, err := parser.ParseFile([]byte(`export default function(a, b) { return a + b; }`), "src/components/Button.jsx")
		if err != nil {
			t.Fatal(err)
		}
		if len(fa.Functions) != 1 {
			t.Fatalf("expected 1 function, got %d", len(fa.Functions))
		}
		fn := fa.Functions[0]
		if fn.Name != "Button" || !fn.IsDefault || !fn.IsExported {
			t.Errorf("got %+v, want Name=Button IsDefault=true IsExported=true", fn)
		}
	})

	t.Run("anonymous default async function", func(t *testing.T) {
		fa, err := parser.ParseFile([]byte(`export default async function(req) { return req; }`), `C:\app\routes\handler.ts`)
		if err != nil {
			t.Fatal(err)
		}
		if len(fa.Functions) != 1 {
			t.Fatalf("expected 1 function, got %d", len(fa.Functions))
		}
		fn := fa.Functions[0]
		if fn.Name != "handler" || !fn.IsDefault || !fn.IsExported || !fn.IsAsync {
			t.Errorf("got %+v, want Name=handler IsDefault=true IsExported=true IsAsync=true", fn)
		}
	})

	t.Run("anonymous default arrow function", func(t *testing.T) {
		fa, err := parser.ParseFile([]byte(`export default (props) => <div>{props.title}</div>;`), "src/Card.tsx")
		if err != nil {
			t.Fatal(err)
		}
		if len(fa.Functions) != 1 {
			t.Fatalf("expected 1 function, got %d", len(fa.Functions))
		}
		fn := fa.Functions[0]
		if fn.Name != "Card" || !fn.IsDefault || !fn.IsExported || !fn.IsArrow {
			t.Errorf("got %+v, want Name=Card IsDefault=true IsExported=true IsArrow=true", fn)
		}
	})

	t.Run("anonymous default class", func(t *testing.T) {
		fa, err := parser.ParseFile([]byte(`export default class { render() {} }`), "src/Modal.js")
		if err != nil {
			t.Fatal(err)
		}
		if len(fa.Classes) != 1 {
			t.Fatalf("expected 1 class, got %d", len(fa.Classes))
		}
		cls := fa.Classes[0]
		if cls.Name != "Modal" || !cls.IsDefault || !cls.IsExported {
			t.Errorf("got %+v, want Name=Modal IsDefault=true IsExported=true", cls)
		}
	})
}

func TestTreeSitter_TypeAliasAndEnumAssignment(t *testing.T) {
	source := []byte(`
export type AliasToIdentifier = ExistingType;
export type GenericAlias = Array<string>;
export type TupleAlias = [string, number];

export enum HttpStatus {
	OK = 200,
	NotFound = "NOT_FOUND",
	Pending,
}
`)
	fa, err := NewTreeSitterParser().ParseFile(source, "types.ts")
	if err != nil {
		t.Fatal(err)
	}

	wantTypes := map[string]string{
		"AliasToIdentifier": "ExistingType",
		"GenericAlias":      "Array<string>",
		"TupleAlias":        "[string, number]",
	}
	if len(fa.Types) != len(wantTypes) {
		t.Fatalf("expected %d type aliases, got %d: %+v", len(wantTypes), len(fa.Types), fa.Types)
	}
	for _, ta := range fa.Types {
		if wantDef, ok := wantTypes[ta.Name]; !ok {
			t.Errorf("unexpected type alias name %q (full: %+v)", ta.Name, ta)
		} else if ta.Definition != wantDef {
			t.Errorf("type %s definition = %q, want %q", ta.Name, ta.Definition, wantDef)
		}
	}

	if len(fa.Enums) != 1 {
		t.Fatalf("expected 1 enum, got %d", len(fa.Enums))
	}
	gotMembers := fa.Enums[0].Members
	wantMembers := []string{"OK", "NotFound", "Pending"}
	if len(gotMembers) != len(wantMembers) {
		t.Fatalf("enum members = %v, want %v", gotMembers, wantMembers)
	}
	for i := range wantMembers {
		if gotMembers[i] != wantMembers[i] {
			t.Errorf("enum member[%d] = %q, want %q", i, gotMembers[i], wantMembers[i])
		}
	}
}

func TestTreeSitter_JSDocNotPollutedByPlainBlockComment(t *testing.T) {
	source := []byte(`/** First function doc. */
function first() {}

/* Plain non-JSDoc block comment */
function second() {}
`)
	fa, err := NewTreeSitterParser().ParseFile(source, "doc.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(fa.Functions) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(fa.Functions))
	}
	if fa.Functions[0].Docstring != "First function doc." {
		t.Errorf("first docstring = %q, want %q", fa.Functions[0].Docstring, "First function doc.")
	}
	if fa.Functions[1].Docstring != "" {
		t.Errorf("second docstring = %q, want empty", fa.Functions[1].Docstring)
	}
}

func TestTreeSitter_JSDocOnClassesMethodsAndInterfaces(t *testing.T) {
	source := []byte(`/** Exported interface doc. */
export interface Serializable {
	id: string;
}

/** Non-exported interface doc. */
interface InternalConfig {
	debug: boolean;
}

/** Exported class doc. */
export class AccountService {
	/** Method doc. */
	getAccount(id: string) {
		return id;
	}
}

/** Non-exported class doc. */
class HelperClass {}
`)
	fa, err := NewTreeSitterParser().ParseFile(source, "service.ts")
	if err != nil {
		t.Fatal(err)
	}

	if len(fa.Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(fa.Interfaces))
	}
	if fa.Interfaces[0].Docstring != "Exported interface doc." {
		t.Errorf("Serializable docstring = %q", fa.Interfaces[0].Docstring)
	}
	if fa.Interfaces[1].Docstring != "Non-exported interface doc." {
		t.Errorf("InternalConfig docstring = %q", fa.Interfaces[1].Docstring)
	}

	if len(fa.Classes) != 2 {
		t.Fatalf("expected 2 classes, got %d", len(fa.Classes))
	}
	if fa.Classes[0].Docstring != "Exported class doc." {
		t.Errorf("AccountService docstring = %q", fa.Classes[0].Docstring)
	}
	if len(fa.Classes[0].Methods) != 1 || fa.Classes[0].Methods[0].Docstring != "Method doc." {
		t.Errorf("AccountService.getAccount docstring = %+v", fa.Classes[0].Methods)
	}
	if fa.Classes[1].Docstring != "Non-exported class doc." {
		t.Errorf("HelperClass docstring = %q", fa.Classes[1].Docstring)
	}
}

func TestTreeSitter_AbstractClasses(t *testing.T) {
	source := []byte(`
abstract class BaseRepo {
	abstract findById(id: string): void;
}

export abstract class ExportedRepo extends BaseRepo {
	save() {}
}
`)
	fa, err := NewTreeSitterParser().ParseFile(source, "repo.ts")
	if err != nil {
		t.Fatal(err)
	}
	if len(fa.Classes) != 2 {
		t.Fatalf("expected 2 abstract classes, got %d: %+v", len(fa.Classes), fa.Classes)
	}
	if fa.Classes[0].Name != "BaseRepo" || !fa.Classes[0].IsAbstract || fa.Classes[0].IsExported {
		t.Errorf("BaseRepo = %+v", fa.Classes[0])
	}
	if fa.Classes[1].Name != "ExportedRepo" || !fa.Classes[1].IsAbstract || !fa.Classes[1].IsExported {
		t.Errorf("ExportedRepo = %+v", fa.Classes[1])
	}
}

func TestTreeSitter_LexicalDeclarationOnlyExtractsActualFunctions(t *testing.T) {
	source := []byte(`
const label = "Select a function => here";
const doubled = items.map(x => x * 2);
const filtered = items.filter(function(x) { return x > 0; });
const MemoButton = React.memo((props) => <button>{props.label}</button>);
const ForwardInput = forwardRef(function(props, ref) { return <input ref={ref} />; });
const realArrow = (x) => x + 1;
`)
	fa, err := NewTreeSitterParser().ParseFile(source, "comp.tsx")
	if err != nil {
		t.Fatal(err)
	}

	got := make(map[string]bool)
	for _, fn := range fa.Functions {
		got[fn.Name] = true
	}

	for _, unexpected := range []string{"label", "doubled", "filtered"} {
		if got[unexpected] {
			t.Errorf("non-function variable %q was falsely indexed as a function", unexpected)
		}
	}
	for _, expected := range []string{"MemoButton", "ForwardInput", "realArrow"} {
		if !got[expected] {
			t.Errorf("expected function %q to be indexed, got %v", expected, got)
		}
	}
}

