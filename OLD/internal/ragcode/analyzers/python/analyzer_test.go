package python

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewCodeAnalyzer(t *testing.T) {
	analyzer := NewCodeAnalyzer()
	if analyzer == nil {
		t.Fatal("NewCodeAnalyzer returned nil")
	}
	if analyzer.modules == nil {
		t.Fatal("modules map not initialized")
	}
}

func TestExtractModuleDocstring(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name: "simple docstring",
			content: `"""This is a module docstring."""
import os`,
			expected: "This is a module docstring.",
		},
		{
			name: "multiline docstring",
			content: `"""
This is a multiline
module docstring.
"""
import os`,
			expected: "This is a multiline\nmodule docstring.",
		},
		{
			name: "with shebang and encoding",
			content: `#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Module docstring after shebang."""
import os`,
			expected: "Module docstring after shebang.",
		},
		{
			name:     "no docstring",
			content:  `import os`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := strings.Split(tt.content, "\n")
			result := analyzer.extractModuleDocstring(lines)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestExtractImports(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `import os
import sys as system
from typing import List, Dict
from collections import OrderedDict as OD
from pathlib import Path`

	lines := strings.Split(content, "\n")
	imports := analyzer.extractImports(lines)

	if len(imports) != 5 {
		t.Fatalf("expected 5 imports, got %d", len(imports))
	}

	// Check first import
	if imports[0].Module != "os" || imports[0].IsFrom {
		t.Errorf("first import incorrect: %+v", imports[0])
	}

	// Check aliased import
	if imports[1].Module != "sys" || imports[1].Alias != "system" {
		t.Errorf("aliased import incorrect: %+v", imports[1])
	}

	// Check from import
	if imports[2].Module != "typing" || !imports[2].IsFrom || len(imports[2].Names) != 2 {
		t.Errorf("from import incorrect: %+v", imports[2])
	}
}

func TestExtractClasses(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `class MyClass:
    """A simple class."""
    
    def __init__(self, name: str):
        """Initialize the class."""
        self.name = name
    
    def greet(self) -> str:
        """Return a greeting."""
        return f"Hello, {self.name}"

class ChildClass(MyClass):
    """A child class."""
    pass
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 2 {
		t.Fatalf("expected 2 classes, got %d", len(classes))
	}

	// Check first class
	if classes[0].Name != "MyClass" {
		t.Errorf("expected class name 'MyClass', got %q", classes[0].Name)
	}
	if classes[0].Description != "A simple class." {
		t.Errorf("expected docstring 'A simple class.', got %q", classes[0].Description)
	}
	if len(classes[0].Methods) != 2 {
		t.Errorf("expected 2 methods, got %d", len(classes[0].Methods))
	}

	// Check child class
	if classes[1].Name != "ChildClass" {
		t.Errorf("expected class name 'ChildClass', got %q", classes[1].Name)
	}
	if len(classes[1].Bases) != 1 || classes[1].Bases[0] != "MyClass" {
		t.Errorf("expected base class 'MyClass', got %v", classes[1].Bases)
	}
}

func TestExtractFunctions(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `def simple_function():
    """A simple function."""
    pass

async def async_function(x: int, y: int) -> int:
    """An async function."""
    return x + y

@decorator
def decorated_function():
    """A decorated function."""
    pass
`

	lines := strings.Split(content, "\n")
	functions := analyzer.extractFunctions(lines, "test.py", []byte(content))

	if len(functions) != 3 {
		t.Fatalf("expected 3 functions, got %d", len(functions))
	}

	// Check simple function
	if functions[0].Name != "simple_function" {
		t.Errorf("expected function name 'simple_function', got %q", functions[0].Name)
	}

	// Check async function
	if functions[1].Name != "async_function" || !functions[1].IsAsync {
		t.Errorf("async function not detected correctly: %+v", functions[1])
	}
	if len(functions[1].Parameters) != 2 {
		t.Errorf("expected 2 parameters, got %d", len(functions[1].Parameters))
	}
	if functions[1].ReturnType != "int" {
		t.Errorf("expected return type 'int', got %q", functions[1].ReturnType)
	}

	// Check decorated function
	if functions[2].Name != "decorated_function" {
		t.Errorf("expected function name 'decorated_function', got %q", functions[2].Name)
	}
	if len(functions[2].Decorators) != 1 || functions[2].Decorators[0] != "decorator" {
		t.Errorf("decorator not detected: %v", functions[2].Decorators)
	}
}

func TestParseParameters(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"empty", "", 0},
		{"self only", "self", 1},
		{"simple params", "x, y, z", 3},
		{"typed params", "x: int, y: str", 2},
		{"with defaults", "x: int = 0, y: str = 'hello'", 2},
		{"args and kwargs", "*args, **kwargs", 2},
		{"complex", "self, x: int, y: List[str] = None, *args, **kwargs", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := analyzer.parseParameters(tt.input)
			if len(params) != tt.expected {
				t.Errorf("expected %d params, got %d: %+v", tt.expected, len(params), params)
			}
		})
	}
}

func TestExtractVariablesAndConstants(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `MAX_SIZE = 100
DEFAULT_NAME = "test"
my_variable: int = 42
another_var = "hello"
`

	lines := strings.Split(content, "\n")
	variables, constants := analyzer.extractVariablesAndConstants(lines, "test.py")

	if len(constants) != 2 {
		t.Errorf("expected 2 constants, got %d", len(constants))
	}
	if len(variables) != 2 {
		t.Errorf("expected 2 variables, got %d", len(variables))
	}

	// Check constant
	if constants[0].Name != "MAX_SIZE" || constants[0].Value != "100" {
		t.Errorf("constant not parsed correctly: %+v", constants[0])
	}

	// Check variable with type annotation
	found := false
	for _, v := range variables {
		if v.Name == "my_variable" && v.Type == "int" {
			found = true
			break
		}
	}
	if !found {
		t.Error("typed variable not found")
	}
}

func TestIsConstantName(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"MAX_SIZE", true},
		{"DEFAULT_VALUE", true},
		{"API_KEY", true},
		{"myVariable", false},
		{"my_variable", false},
		{"MyClass", false},
		{"_PRIVATE_CONST", false}, // Starts with underscore
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isConstantName(tt.name)
			if result != tt.expected {
				t.Errorf("isConstantName(%q) = %v, expected %v", tt.name, result, tt.expected)
			}
		})
	}
}

func TestAnalyzePaths(t *testing.T) {
	// Create a temporary directory with Python files
	tmpDir, err := os.MkdirTemp("", "python_analyzer_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a sample Python file
	sampleCode := `"""Sample module for testing."""

MAX_RETRIES = 3

class SampleClass:
    """A sample class."""
    
    def __init__(self, value: int):
        """Initialize with a value."""
        self.value = value
    
    def get_value(self) -> int:
        """Return the value."""
        return self.value

def sample_function(x: int, y: int) -> int:
    """Add two numbers."""
    return x + y
`

	filePath := filepath.Join(tmpDir, "sample.py")
	if err := os.WriteFile(filePath, []byte(sampleCode), 0644); err != nil {
		t.Fatalf("failed to write sample file: %v", err)
	}

	// Analyze
	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzePaths failed: %v", err)
	}

	// Verify chunks
	if len(chunks) == 0 {
		t.Fatal("no chunks returned")
	}

	// Check for expected chunk types
	hasClass := false
	hasMethod := false
	hasFunction := false
	hasConst := false

	for _, chunk := range chunks {
		switch chunk.Type {
		case "class":
			hasClass = true
			if chunk.Name != "SampleClass" {
				t.Errorf("expected class name 'SampleClass', got %q", chunk.Name)
			}
		case "method":
			hasMethod = true
		case "function":
			hasFunction = true
			if chunk.Name != "sample_function" {
				t.Errorf("expected function name 'sample_function', got %q", chunk.Name)
			}
		case "const":
			hasConst = true
			if chunk.Name != "MAX_RETRIES" {
				t.Errorf("expected constant name 'MAX_RETRIES', got %q", chunk.Name)
			}
		}
	}

	if !hasClass {
		t.Error("class chunk not found")
	}
	if !hasMethod {
		t.Error("method chunk not found")
	}
	if !hasFunction {
		t.Error("function chunk not found")
	}
	if !hasConst {
		t.Error("constant chunk not found")
	}
}

func TestAnalyzeFile(t *testing.T) {
	// Create a temporary Python file
	tmpFile, err := os.CreateTemp("", "test_*.py")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := `"""Test module."""

class TestClass:
    """Test class."""
    pass

def test_function():
    """Test function."""
    pass
`

	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("AnalyzeFile failed: %v", err)
	}

	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks, got %d", len(chunks))
	}

	// Verify all chunks have language set to "python"
	for _, chunk := range chunks {
		if chunk.Language != "python" {
			t.Errorf("expected language 'python', got %q", chunk.Language)
		}
	}
}

func TestDataclassDetection(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `from dataclasses import dataclass

@dataclass
class Point:
    """A point in 2D space."""
    x: float
    y: float
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(classes))
	}

	if !classes[0].IsDataclass {
		t.Error("dataclass not detected")
	}
}

func TestAbstractClassDetection(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `from abc import ABC, abstractmethod

class AbstractBase(ABC):
    """An abstract base class."""
    
    @abstractmethod
    def abstract_method(self):
        """Must be implemented."""
        pass
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(classes))
	}

	if !classes[0].IsAbstract {
		t.Error("abstract class not detected")
	}
}

func TestStaticAndClassMethods(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `class MyClass:
    """A class with different method types."""
    
    @staticmethod
    def static_method():
        """A static method."""
        pass
    
    @classmethod
    def class_method(cls):
        """A class method."""
        pass
    
    def instance_method(self):
        """An instance method."""
        pass
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(classes))
	}

	methods := classes[0].Methods
	if len(methods) != 3 {
		t.Fatalf("expected 3 methods, got %d", len(methods))
	}

	// Find and check each method type
	staticFound := false
	classMethodFound := false
	instanceFound := false

	for _, m := range methods {
		switch m.Name {
		case "static_method":
			staticFound = true
			if !m.IsStatic {
				t.Error("static method not marked as static")
			}
		case "class_method":
			classMethodFound = true
			if !m.IsClassMethod {
				t.Error("class method not marked as classmethod")
			}
		case "instance_method":
			instanceFound = true
			if m.IsStatic || m.IsClassMethod {
				t.Error("instance method incorrectly marked")
			}
		}
	}

	if !staticFound {
		t.Error("static method not found")
	}
	if !classMethodFound {
		t.Error("class method not found")
	}
	if !instanceFound {
		t.Error("instance method not found")
	}
}

func TestPropertyDetection(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `class MyClass:
    """A class with properties."""
    
    def __init__(self):
        self._value = 0
    
    @property
    def value(self) -> int:
        """Get the value."""
        return self._value
    
    @value.setter
    def value(self, val: int):
        """Set the value."""
        self._value = val
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(classes))
	}

	properties := classes[0].Properties
	if len(properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(properties))
	}

	prop := properties[0]
	if prop.Name != "value" {
		t.Errorf("expected property name 'value', got %q", prop.Name)
	}
	if !prop.HasGetter {
		t.Error("property getter not detected")
	}
	if !prop.HasSetter {
		t.Error("property setter not detected")
	}
}

func TestAsyncFunctionDetection(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `async def fetch_data(url: str) -> dict:
    """Fetch data from URL."""
    pass

def sync_function():
    """A synchronous function."""
    pass
`

	lines := strings.Split(content, "\n")
	functions := analyzer.extractFunctions(lines, "test.py", []byte(content))

	if len(functions) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(functions))
	}

	asyncFound := false
	syncFound := false

	for _, fn := range functions {
		if fn.Name == "fetch_data" {
			asyncFound = true
			if !fn.IsAsync {
				t.Error("async function not marked as async")
			}
		}
		if fn.Name == "sync_function" {
			syncFound = true
			if fn.IsAsync {
				t.Error("sync function incorrectly marked as async")
			}
		}
	}

	if !asyncFound {
		t.Error("async function not found")
	}
	if !syncFound {
		t.Error("sync function not found")
	}
}

func TestGetModules(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	// Analyze a simple file
	tmpFile, err := os.CreateTemp("", "test_*.py")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := `"""Test module."""
def test():
    pass
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("failed to write: %v", err)
	}
	tmpFile.Close()

	_, err = analyzer.AnalyzeFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("AnalyzeFile failed: %v", err)
	}

	modules := analyzer.GetModules()
	if len(modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(modules))
	}
}

func TestEnumDetection(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `from enum import Enum, IntEnum

class Color(Enum):
    """Color enumeration."""
    RED = 1
    GREEN = 2
    BLUE = 3

class Priority(IntEnum):
    """Priority levels."""
    LOW = 1
    MEDIUM = 2
    HIGH = 3
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 2 {
		t.Fatalf("expected 2 classes, got %d", len(classes))
	}

	for _, class := range classes {
		if !class.IsEnum {
			t.Errorf("class %s should be marked as Enum", class.Name)
		}
	}
}

func TestProtocolDetection(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `from typing import Protocol

class Drawable(Protocol):
    """Protocol for drawable objects."""
    def draw(self) -> None:
        ...
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(classes))
	}

	if !classes[0].IsProtocol {
		t.Error("class should be marked as Protocol")
	}
}

func TestMultiLineImports(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `from typing import (
    Optional,
    List,
    Dict,
    Union,
)

from collections import OrderedDict
`

	lines := strings.Split(content, "\n")
	imports := analyzer.extractImports(lines)

	if len(imports) != 2 {
		t.Fatalf("expected 2 import statements, got %d", len(imports))
	}

	// Check multi-line import
	typingImport := imports[0]
	if typingImport.Module != "typing" {
		t.Errorf("expected module 'typing', got '%s'", typingImport.Module)
	}
	if len(typingImport.Names) < 4 {
		t.Errorf("expected at least 4 names imported, got %d", len(typingImport.Names))
	}
}

func TestIncludeTestsOption(t *testing.T) {
	// Test that NewCodeAnalyzerWithOptions works
	analyzer := NewCodeAnalyzerWithOptions(true)
	if !analyzer.includeTests {
		t.Error("includeTests should be true")
	}

	analyzer2 := NewCodeAnalyzerWithOptions(false)
	if analyzer2.includeTests {
		t.Error("includeTests should be false")
	}
}

func TestMixinDetection(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `class LoggingMixin:
    """Mixin for logging functionality."""
    def log(self, message: str) -> None:
        print(message)

class UserMixin:
    """User-related mixin."""
    pass

class MyClass(Base, LoggingMixin):
    """Class using a mixin."""
    pass
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 3 {
		t.Fatalf("expected 3 classes, got %d", len(classes))
	}

	// LoggingMixin should be detected as mixin
	if !classes[0].IsMixin {
		t.Error("LoggingMixin should be marked as mixin")
	}

	// UserMixin should be detected as mixin
	if !classes[1].IsMixin {
		t.Error("UserMixin should be marked as mixin")
	}

	// MyClass inherits from a mixin, so it should also be marked
	if !classes[2].IsMixin {
		t.Error("MyClass should be marked as mixin (inherits from LoggingMixin)")
	}
}

func TestMetaclassDetection(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `from abc import ABCMeta

class MyAbstractClass(metaclass=ABCMeta):
    """Class with metaclass."""
    pass

class AnotherClass(Base, metaclass=CustomMeta):
    """Another class with custom metaclass."""
    pass
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 2 {
		t.Fatalf("expected 2 classes, got %d", len(classes))
	}

	if classes[0].Metaclass != "ABCMeta" {
		t.Errorf("expected metaclass 'ABCMeta', got '%s'", classes[0].Metaclass)
	}

	if classes[1].Metaclass != "CustomMeta" {
		t.Errorf("expected metaclass 'CustomMeta', got '%s'", classes[1].Metaclass)
	}
}

func TestMethodCallExtraction(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `class MyClass:
    def process(self, data: str) -> None:
        self.validate(data)
        self.transform(data)
        result = Helper.compute(data)
        save_to_db(result)
        super().process(data)
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(classes))
	}

	if len(classes[0].Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(classes[0].Methods))
	}

	method := classes[0].Methods[0]

	// Should have detected calls to: validate, transform, Helper.compute, save_to_db, super().process
	if len(method.Calls) < 4 {
		t.Errorf("expected at least 4 method calls, got %d", len(method.Calls))
	}

	// Check for self.validate call
	foundValidate := false
	for _, call := range method.Calls {
		if call.Name == "validate" && call.Receiver == "self" {
			foundValidate = true
			break
		}
	}
	if !foundValidate {
		t.Error("self.validate call not detected")
	}
}

func TestTypeDependencyExtraction(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `from typing import Optional, List

class UserService:
    def get_user(self, user_id: int) -> User:
        pass
    
    def get_users(self) -> List[User]:
        pass
    
    def find_by_email(self, email: str) -> Optional[User]:
        pass
    
    def create_order(self, user: User, items: List[Product]) -> Order:
        pass
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(classes))
	}

	// Check class dependencies
	deps := classes[0].Dependencies

	// Should include User, Product, Order
	expectedDeps := map[string]bool{"User": false, "Product": false, "Order": false}
	for _, dep := range deps {
		if _, ok := expectedDeps[dep]; ok {
			expectedDeps[dep] = true
		}
	}

	for dep, found := range expectedDeps {
		if !found {
			t.Errorf("dependency '%s' not detected", dep)
		}
	}
}

func TestClassDependencies(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	content := `class BaseModel:
    pass

class User(BaseModel):
    manager: UserManager
    
    def get_orders(self) -> List[Order]:
        pass

class Order(BaseModel):
    user: User
`

	lines := strings.Split(content, "\n")
	classes := analyzer.extractClasses(lines, "test.py", []byte(content))

	if len(classes) != 3 {
		t.Fatalf("expected 3 classes, got %d", len(classes))
	}

	// User class should depend on BaseModel, UserManager, Order
	userClass := classes[1]
	if userClass.Name != "User" {
		t.Fatalf("expected User class, got %s", userClass.Name)
	}

	// Check that BaseModel is in dependencies
	foundBaseModel := false
	for _, dep := range userClass.Dependencies {
		if dep == "BaseModel" {
			foundBaseModel = true
			break
		}
	}
	if !foundBaseModel {
		t.Error("BaseModel not found in User dependencies")
	}
}
