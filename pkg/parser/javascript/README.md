# JavaScript, TypeScript, React & Vue Parser (`pkg/parser/javascript`)

The `javascript` package implements an AST-based analyzer for **JavaScript** (`.js`, `.mjs`, `.cjs`), **TypeScript** (`.ts`), **React JSX/TSX** (`.jsx`, `.tsx`), and **Vue Single-File Components** (`.vue`), powered by **Tree-sitter** (`gotreesitter`) and a dedicated Vue SFC parser.

## 🎯 Extracted Symbols

1. **Functions & Arrow Functions**:
   - Named declarations (`function foo()`, `export default function()`), anonymous default exports (named after the file stem), generator functions, and `const`/`let` arrow/function expressions (including `React.memo` and `React.forwardRef` wrappers).
2. **React Components & Hooks**:
   - Detects PascalCase functional and class components in `.jsx`/`.tsx`/`.js`/`.ts`, extracts props and React hooks (`useState`, `useEffect`, custom `use*` hooks).
3. **Classes & Abstract Classes**:
   - Extracts `class` and `abstract class` declarations, constructors, methods, getters/setters, inheritance (`extends`), `implements` clauses, and JSDoc comments on classes and methods.
4. **TypeScript Types, Interfaces & Enums**:
   - Extracts `interface` declarations (properties, method signatures, `extends`), `type` aliases (`type ID = string | number`), and `enum` declarations (including initialized members `enum_assignment`).
5. **Vue Single-File Components (`vue/`)**:
   - Parses `.vue` files (`<template>`, `<script>`, `<script setup>`, `<style>`).
   - Supports both **Options API** (`data`, `props`, `computed`, `methods`, `watch`, lifecycle hooks) and **Composition API** (`<script setup>`, `defineProps`, `defineEmits`, `defineExpose`, custom composables `use*`).

---

## 🏗️ Package Structure

- `analyzer.go`: Implements `parser.Analyzer` and coordinates JS/TS and Vue analysis.
- `treesitter.go`: Tree-sitter AST parser for JS, TS, JSX, and TSX (`gotreesitter`).
- `vue/analyzer.go`: Vue SFC parser for Options API and `<script setup>` Composition API.
- `types.go`: Symbol and AST metadata structures (`FileAnalysis`, `FunctionInfo`, `ClassInfo`, `InterfaceInfo`, `TypeAliasInfo`, `EnumInfo`).

---

## 📋 Supported Technologies

| Technology | Status | Details |
|------------|--------|---------|
| **JavaScript (ES6+)** | ✅ Production | Functions, classes, default/named exports, JSDoc, CommonJS & ES modules. |
| **TypeScript (`.ts`, `.tsx`)** | ✅ Production | Tree-sitter AST: interfaces, type aliases, enums, generics, abstract classes, decorators. |
| **React (`.jsx`, `.tsx`)** | ✅ Production | Functional & class components, `memo`/`forwardRef` wrappers, props, hooks. |
| **Vue (`.vue` SFC)** | ✅ Production | `<script setup>` Composition API, Options API, props, emits, custom composables. |
