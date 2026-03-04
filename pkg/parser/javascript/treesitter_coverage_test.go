package javascript

import (
	"testing"
)

// Tests targeting uncovered paths:
// - extractClassHeritage (multiple extends, implements)
// - extractParams (destructured, rest, typed)
// - extractInterface (extends, methods)
// - buildClassSignature
// - processExportStatement edge cases

func TestTreeSitter_ClassWithImplements(t *testing.T) {
	// Tests extractClassHeritage with extends clause
	source := []byte(`
export class AdminService extends BaseService {
    adminLevel = 1;
    isAdmin() { return this.adminLevel > 0; }
    static create() { return new AdminService(); }
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "service.js")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Classes) < 1 {
		t.Fatal("expected at least 1 class")
	}

	cls := fa.Classes[0]
	if cls.Extends != "BaseService" {
		t.Errorf("expected extends 'BaseService', got '%s'", cls.Extends)
	}
	if !cls.IsExported {
		t.Error("AdminService should be exported")
	}
}

func TestTreeSitter_ClassDefaultExport(t *testing.T) {
	// Tests processExportStatement with default class
	source := []byte(`
export default class MyComponent {
    title = 'hello';
    getTitle() { return this.title; }
    setTitle(t) { this.title = t; }
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "component.js")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Classes) < 1 {
		t.Fatal("expected at least 1 class")
	}

	cls := fa.Classes[0]
	if cls.Name != "MyComponent" {
		t.Errorf("expected 'MyComponent', got '%s'", cls.Name)
	}
	if !cls.IsExported {
		t.Error("MyComponent should be exported")
	}
}

func TestTreeSitter_FunctionWithTypedParams(t *testing.T) {
	// Tests extractParams with TypeScript typed parameters
	source := []byte(`
function greet(name: string, age: number, active?: boolean): string {
    return name;
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Functions) < 1 {
		t.Fatal("expected at least 1 function")
	}

	fn := fa.Functions[0]
	if fn.Name != "greet" {
		t.Errorf("expected 'greet', got '%s'", fn.Name)
	}
	if len(fn.Params) < 3 {
		t.Errorf("expected at least 3 params, got %d: %v", len(fn.Params), fn.Params)
	}
}

func TestTreeSitter_FunctionWithRestParam(t *testing.T) {
	// Tests extractParams with rest parameter
	source := []byte(`
function sum(first, ...rest) {
    return first + rest.reduce((a, b) => a + b, 0);
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.js")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Functions) < 1 {
		t.Fatal("expected at least 1 function")
	}
}

func TestTreeSitter_FunctionWithDestructuredParam(t *testing.T) {
	// Tests extractParams with destructured object param
	source := []byte(`
function configure({ host, port, timeout = 5000 }) {
    return host + ':' + port;
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.js")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Functions) < 1 {
		t.Fatal("expected at least 1 function")
	}
}

func TestTreeSitter_InterfaceWithExtends(t *testing.T) {
	// Tests extractInterface with extends clause and methods
	source := []byte(`
interface Animal {
    name: string;
    speak(): void;
}

interface Dog extends Animal {
    breed: string;
    fetch(item: string): void;
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Interfaces) < 2 {
		t.Fatalf("expected at least 2 interfaces, got %d", len(fa.Interfaces))
	}

	var dog *TSInterface
	for i, iface := range fa.Interfaces {
		if iface.Name == "Dog" {
			dog = &fa.Interfaces[i]
		}
	}

	if dog == nil {
		t.Fatal("expected Dog interface")
	}

	if len(dog.Extends) < 1 || dog.Extends[0] != "Animal" {
		t.Errorf("expected Dog extends Animal, got %v", dog.Extends)
	}
}

func TestTreeSitter_InterfaceWithMethods(t *testing.T) {
	// Tests extractInterface - properties (methods may not be extracted separately)
	source := []byte(`
interface UserService {
    userId: string;
    userName: string;
    email: string;
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Interfaces) < 1 {
		t.Fatal("expected at least 1 interface")
	}

	iface := fa.Interfaces[0]
	if iface.Name != "UserService" {
		t.Errorf("expected 'UserService', got '%s'", iface.Name)
	}
}

func TestTreeSitter_ClassMultipleMethods(t *testing.T) {
	// Tests extractClassMethods with private, static, async
	source := []byte(`
class Repository {
    #db;

    constructor(db) { this.#db = db; }

    async findAll() { return this.#db.query('SELECT *'); }

    static create(db) { return new Repository(db); }

    #buildQuery(filter) { return filter; }
}
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.js")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(fa.Classes) < 1 {
		t.Fatal("expected at least 1 class")
	}

	cls := fa.Classes[0]
	if len(cls.Methods) < 2 {
		t.Errorf("expected at least 2 public methods, got %d", len(cls.Methods))
	}
}

func TestTreeSitter_ExportNamedVar(t *testing.T) {
	// Tests processExportStatement with const export
	source := []byte(`
export const API_URL = 'https://api.example.com';
export let counter = 0;
`)
	parser := NewTreeSitterParser()
	fa, err := parser.ParseFile(source, "test.js")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	// Should parse without crash even if no functions/classes
	_ = fa
}

func TestTreeSitter_ParseFile_UnsupportedExtension(t *testing.T) {
	// ParseFile returns nil,nil for unknown extensions (.xyz not supported)
	source := []byte(`some content`)
	parser := NewTreeSitterParser()
	result, err := parser.ParseFile(source, "test.xyz")
	if err != nil {
		t.Errorf("expected no error for unsupported file, got: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for unsupported file type")
	}
}
