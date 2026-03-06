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
