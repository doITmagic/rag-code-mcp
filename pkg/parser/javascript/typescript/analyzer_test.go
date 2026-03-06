package typescript

import (
	"testing"
)

func TestAnalyzer_Generics(t *testing.T) {
	source := `
interface Repository<T extends Entity> {
    find(id: string): T;
    findAll(): T[];
}

function identity<T>(value: T): T {
    return value;
}

class Cache<K, V> {
    private store: Map<K, V> = new Map();
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "repo.ts")

	if len(info.Generics) < 1 {
		t.Fatalf("expected at least 1 generic, got %d", len(info.Generics))
	}

	names := make(map[string]bool)
	for _, g := range info.Generics {
		names[g.Name] = true
	}
	if !names["Repository"] {
		t.Error("expected Repository generic")
	}
}

func TestAnalyzer_TypeGuards(t *testing.T) {
	source := `
interface Dog { bark(): void; }
interface Cat { meow(): void; }

function isDog(animal: Dog | Cat): animal is Dog {
    return (animal as Dog).bark !== undefined;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "guards.ts")

	if len(info.TypeGuards) < 1 {
		t.Fatalf("expected at least 1 type guard, got %d", len(info.TypeGuards))
	}

	guard := info.TypeGuards[0]
	if guard.ParamName != "animal" {
		t.Errorf("expected param 'animal', got '%s'", guard.ParamName)
	}
	if guard.GuardType != "Dog" {
		t.Errorf("expected guard type 'Dog', got '%s'", guard.GuardType)
	}
}

func TestAnalyzer_MappedTypes(t *testing.T) {
	source := `
interface User {
    name: string;
    age: number;
    email: string;
}

type PartialUser = Partial<User>;
type UserName = Pick<User, 'name' | 'email'>;
type UserRecord = Record<string, User>;
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "types.ts")

	if len(info.MappedTypes) < 1 {
		t.Fatalf("expected at least 1 mapped type, got %d", len(info.MappedTypes))
	}

	names := make(map[string]bool)
	for _, m := range info.MappedTypes {
		names[m.Name] = true
	}
	if !names["Partial"] {
		t.Error("expected Partial usage")
	}
	if !names["Pick"] {
		t.Error("expected Pick usage")
	}
}

func TestAnalyzer_DeclFile(t *testing.T) {
	source := `
declare module 'my-library' {
    export interface Config {
        apiKey: string;
    }
    export function initialize(config: Config): void;
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "my-library.d.ts")

	if len(info.DeclFiles) < 1 {
		t.Fatalf("expected at least 1 decl file, got %d", len(info.DeclFiles))
	}
	if info.DeclFiles[0].ModuleName != "my-library" {
		t.Errorf("expected module 'my-library', got '%s'", info.DeclFiles[0].ModuleName)
	}
}

func TestIsTypeScriptFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"app.ts", true},
		{"app.tsx", true},
		{"types.d.ts", true},
		{"utils.mts", true},
		{"config.cts", true},
		{"app.js", false},
		{"app.jsx", false},
	}

	for _, tt := range tests {
		result := IsTypeScriptFile(tt.path)
		if result != tt.expected {
			t.Errorf("IsTypeScriptFile(%q) = %v, want %v", tt.path, result, tt.expected)
		}
	}
}

func TestAnalyzer_NonTSFile(t *testing.T) {
	source := `const x = 1;`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "app.js")

	// Should return empty info for non-TS files
	if len(info.Generics) != 0 || len(info.Decorators) != 0 {
		t.Error("expected empty info for JS file")
	}
}

func TestAnalyzer_Decorators(t *testing.T) {
	source := `
@Component({
    selector: 'app-root',
    templateUrl: './app.component.html'
})
class AppComponent {
    @Input() title: string;

    @HostListener('click')
    onClick() {}
}
`
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(source, "app.component.ts")

	if len(info.Decorators) < 1 {
		t.Fatalf("expected at least 1 decorator, got %d", len(info.Decorators))
	}

	foundComponent := false
	for _, d := range info.Decorators {
		if d.Name == "Component" {
			foundComponent = true
		}
	}
	if !foundComponent {
		t.Error("expected @Component decorator")
	}
}
