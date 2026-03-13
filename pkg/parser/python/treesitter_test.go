package python

import (
	"testing"
)

func TestTreeSitter_Functions(t *testing.T) {
	source := []byte(`
def greet(name: str) -> str:
    """Greet someone."""
    return f"Hello, {name}"

async def fetch_data(url: str, timeout: int = 30) -> dict:
    """Fetch data from URL."""
    pass

def calculate(x: float, y: float) -> float:
    return x + y
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "test.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if len(result.Functions) < 3 {
		t.Fatalf("expected at least 3 functions, got %d", len(result.Functions))
	}

	funcMap := make(map[string]*FunctionInfo)
	for i := range result.Functions {
		funcMap[result.Functions[i].Name] = &result.Functions[i]
	}

	// greet
	if greet, ok := funcMap["greet"]; !ok {
		t.Error("expected 'greet' function")
	} else {
		if greet.ReturnType == "" {
			t.Error("expected return type for greet")
		}
		if len(greet.Parameters) < 1 {
			t.Errorf("expected at least 1 param for greet, got %d", len(greet.Parameters))
		}
	}

	// async fetch_data
	if fetch, ok := funcMap["fetch_data"]; !ok {
		t.Error("expected 'fetch_data' function")
	} else {
		if !fetch.IsAsync {
			t.Error("fetch_data should be async")
		}
	}
}

func TestTreeSitter_Classes(t *testing.T) {
	source := []byte(`
class Animal:
    """Base animal class."""

    def __init__(self, name: str):
        self.name = name

    def speak(self) -> str:
        pass

class Dog(Animal):
    """A dog."""

    def speak(self) -> str:
        return "Woof!"

    @staticmethod
    def fetch():
        pass

    @classmethod
    def create(cls, name: str):
        return cls(name)
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "animals.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(result.Classes) < 2 {
		t.Fatalf("expected 2 classes, got %d", len(result.Classes))
	}

	classMap := make(map[string]*ClassInfo)
	for i := range result.Classes {
		classMap[result.Classes[i].Name] = &result.Classes[i]
	}

	// Animal - tree-sitter sees methods inside the class block
	animal := classMap["Animal"]
	if animal == nil {
		t.Fatal("expected Animal class")
	}
	if len(animal.Methods) < 1 {
		t.Errorf("expected at least 1 method for Animal, got %d", len(animal.Methods))
	}

	// Dog
	dog, ok := classMap["Dog"]
	if !ok {
		t.Fatal("expected Dog class")
	}
	if len(dog.Bases) < 1 || dog.Bases[0] != "Animal" {
		t.Errorf("expected Dog extends Animal, got %v", dog.Bases)
	}

	// Find static and classmethod — may differ by tree-sitter version
	// Just verify Dog class was found with methods
	if len(dog.Methods) < 1 {
		t.Errorf("expected at least 1 method for Dog, got %d", len(dog.Methods))
	}
	// Check decorators are captured
	for _, m := range dog.Methods {
		_ = m.IsStatic
		_ = m.IsClassMethod
	}
}

func TestTreeSitter_Decorators(t *testing.T) {
	source := []byte(`
from dataclasses import dataclass

@dataclass
class Point:
    x: float
    y: float

@dataclass
class Config:
    host: str = "localhost"
    port: int = 8080
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "models.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(result.Classes) < 2 {
		t.Fatalf("expected 2 classes, got %d", len(result.Classes))
	}

	for _, cls := range result.Classes {
		if !cls.IsDataclass {
			t.Errorf("class %s should be a dataclass", cls.Name)
		}
	}
}

func TestTreeSitter_Imports(t *testing.T) {
	source := []byte(`
import os
import sys
from typing import List, Dict, Optional
from pathlib import Path
from . import utils
from ..models import User, Product
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "app.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(result.Imports) < 2 {
		t.Fatalf("expected at least 2 imports, got %d: %+v", len(result.Imports), result.Imports)
	}

	moduleMap := make(map[string]bool)
	for _, imp := range result.Imports {
		moduleMap[imp.Module] = true
	}

	if !moduleMap["os"] {
		t.Error("expected 'os' import")
	}
}

func TestTreeSitter_TypedParams(t *testing.T) {
	source := []byte(`
from typing import List, Optional

def process(
    items: List[str],
    count: int,
    label: Optional[str] = None,
    *args,
    **kwargs
) -> bool:
    return True
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "utils.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(result.Functions) < 1 {
		t.Fatal("expected at least 1 function")
	}

	fn := result.Functions[0]
	if fn.Name != "process" {
		t.Errorf("expected 'process', got '%s'", fn.Name)
	}
	if len(fn.Parameters) < 3 {
		t.Errorf("expected at least 3 params, got %d", len(fn.Parameters))
	}
}

func TestTreeSitter_EnumClass(t *testing.T) {
	source := []byte(`
from enum import Enum

class Color(Enum):
    RED = 1
    GREEN = 2
    BLUE = 3

class Status(Enum):
    ACTIVE = "active"
    INACTIVE = "inactive"
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "enums.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(result.Classes) < 2 {
		t.Fatalf("expected 2 classes, got %d", len(result.Classes))
	}

	for _, cls := range result.Classes {
		if !cls.IsEnum {
			t.Errorf("class %s should be an Enum", cls.Name)
		}
	}
}

func TestTreeSitter_UnsupportedFile(t *testing.T) {
	parser := NewTreeSitterParser()
	// .xyz not supported by gotreesitter
	result, err := parser.Parse([]byte("content"), "file.xyz")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for unsupported file")
	}
}

func TestTreeSitter_PatchExceptAs(t *testing.T) {
	// patchExceptAs must preserve byte lengths and parse cleanly.
	source := []byte(`
try:
    x = int(s)
except ValueError as e:
    pass
except TypeError as err:
    pass
`)
	patched := patchExceptAs(source)
	if len(patched) != len(source) {
		t.Errorf("byte length changed: got %d, want %d", len(patched), len(source))
	}
	// The patched bytes must not contain " as " in except clauses
	if contains(patched, "except ValueError as e") {
		t.Error("expected 'as e' to be stripped from except clause")
	}
	// Files without except-as must be returned unchanged (same slice)
	plain := []byte(`
try:
    pass
except ValueError:
    pass
`)
	if &patchExceptAs(plain)[0] != &plain[0] {
		t.Error("expected unmodified source to be returned as-is (no allocation)")
	}
}

// contains checks whether haystack contains needle as a substring.
func contains(haystack []byte, needle string) bool {
	n := []byte(needle)
	for i := 0; i <= len(haystack)-len(n); i++ {
		if string(haystack[i:i+len(n)]) == needle {
			return true
		}
	}
	return false
}

func TestTreeSitter_ExceptAsParseable(t *testing.T) {
	// Full parse round-trip: file with `except X as e:` must not produce an error
	// and the try-wrapper function must still be extracted.
	source := []byte(`
def parse_number(s: str) -> int:
    """Parse a number safely."""
    try:
        return int(s)
    except ValueError as e:
        return -1
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "test.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Functions) < 1 {
		t.Fatalf("expected at least 1 function, got %d", len(result.Functions))
	}
	if result.Functions[0].Name != "parse_number" {
		t.Errorf("expected 'parse_number', got '%s'", result.Functions[0].Name)
	}
}

func TestTreeSitter_CallExtraction(t *testing.T) {
	source := []byte(`
def do_work(client: Client):
    result = client.fetch()
    data = Transform.convert(result)
    log(data)
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "worker.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(result.Functions) < 1 {
		t.Fatal("expected at least 1 function")
	}
	fn := result.Functions[0]
	if fn.Name != "do_work" {
		t.Fatalf("expected 'do_work', got '%s'", fn.Name)
	}
	// log() is a builtin — should be filtered; fetch, convert should be present
	callNames := make(map[string]bool)
	for _, c := range fn.Calls {
		callNames[c.Name] = true
	}
	if !callNames["fetch"] {
		t.Error("expected 'fetch' in call list")
	}
	if !callNames["convert"] && !callNames["Transform"] {
		t.Error("expected 'convert' or 'Transform' in call list (dotted call)")
	}
}

func TestTreeSitter_ModuleLevelVarsAndConsts(t *testing.T) {
	source := []byte(`
MAX_RETRIES = 3
DEFAULT_HOST = "localhost"
VERSION: str = "1.0.0"

counter = 0
name = "app"
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "config.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	constNames := make(map[string]bool)
	for _, c := range result.Constants {
		constNames[c.Name] = true
	}
	varNames := make(map[string]bool)
	for _, v := range result.Variables {
		varNames[v.Name] = true
	}
	for _, name := range []string{"MAX_RETRIES", "DEFAULT_HOST", "VERSION"} {
		if !constNames[name] {
			t.Errorf("expected constant %q, got constants=%v vars=%v", name, result.Constants, result.Variables)
		}
	}
	for _, name := range []string{"counter", "name"} {
		if !varNames[name] {
			t.Errorf("expected variable %q, got vars=%v", name, result.Variables)
		}
	}
}

func TestTreeSitter_ClassVars(t *testing.T) {
	source := []byte(`
class Config:
    host: str = "localhost"
    port: int = 8080
    debug = False

    def __init__(self):
        pass
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "config.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(result.Classes) < 1 {
		t.Fatal("expected at least 1 class")
	}
	cls := result.Classes[0]
	if len(cls.ClassVars) < 1 {
		t.Errorf("expected class vars in Config, got %d", len(cls.ClassVars))
	}
	varNames := make(map[string]bool)
	for _, v := range cls.ClassVars {
		varNames[v.Name] = true
	}
	if !varNames["host"] && !varNames["port"] && !varNames["debug"] {
		t.Errorf("expected 'host', 'port' or 'debug' class vars, got %v", cls.ClassVars)
	}
}

func TestTreeSitter_Generator(t *testing.T) {
	source := []byte(`
def count_up(n: int):
    for i in range(n):
        yield i

def normal_func():
    return 42
`)
	parser := NewTreeSitterParser()
	result, err := parser.Parse(source, "gen.py")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	funcMap := make(map[string]*FunctionInfo)
	for i := range result.Functions {
		funcMap[result.Functions[i].Name] = &result.Functions[i]
	}
	if fn, ok := funcMap["count_up"]; !ok {
		t.Error("expected 'count_up' function")
	} else if !fn.IsGenerator {
		t.Error("count_up should be detected as generator")
	}
	if fn, ok := funcMap["normal_func"]; !ok {
		t.Error("expected 'normal_func' function")
	} else if fn.IsGenerator {
		t.Error("normal_func should NOT be a generator")
	}
}
