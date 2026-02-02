# JavaScript & TypeScript Analyzer (Planned)

Implementation plan for the JavaScript and TypeScript code analyzer and based frameworks (React, Vue, NestJS). This analyzer will use **Tree-sitter** for fast, incremental, and precise parsing, regardless of code complexity.

## Status: 🔜 PLANNED / ROADMAP

---

## 🎯 Implementation Objectives

Unlike Go, where we use the native AST, for the JS/TS ecosystem we will adopt **Tree-sitter** to ensure:
1. **Multilingualism**: Unified support for `.js`, `.ts`, `.jsx`, `.tsx`.
2. **Performance**: Ultra-fast parsing and possibility for incremental indexing.
3. **Robustness**: Tree-sitter can parse code even during editing (error recovery), useful for future IDE integrations.

---

## 🏗️ Technical Architecture

### Dependencies
- `github.com/smacker/go-tree-sitter`: Go bindings for Tree-sitter.
- `github.com/tree-sitter/tree-sitter-javascript`: JS Parser.
- `github.com/tree-sitter/tree-sitter-typescript`: TS and TSX Parser.

### RagCode Integration
- Implementation of `PathAnalyzer` interface in `internal/ragcode/analyzers`.
- Mapping Tree-sitter nodes to the canonical `CodeChunk` (v2) structure.
- Automatic detection of project type (Node.js, React, Next.js) via `package.json`.

---

## 🔍 What We Will Index

### 1. Functions and Arrow Functions
```javascript
// Function Declaration
function calculateTotal(items) { ... }

// Arrow Function
const filterActive = (user) => user.active;
```
**Metadata**: Signature, parameters, JSDoc, types (if TS).

### 2. Classes and Methods
```typescript
class UserService {
    /** @param id user uid */
    async getUser(id: string): Promise<User> { ... }
}
```
**Metadata**: Class name, visibility (public/private), async/await, decorators.

### 3. React Components (JSX/TSX)
```tsx
export const UserProfile = ({ name }: Props) => {
    return <div>{name}</div>;
};
```
**Metadata**: Props, used hooks (useState, useEffect), component type.

### 4. Imports and Exports
- Identification of dependencies between files to help the LLM model understand the global context.

---

## 🗺️ Implementation Roadmap

### Phase 1: Infrastructure (Alpha)
- [ ] Integration of `go-tree-sitter` library.
- [ ] Creation of folder structure in `internal/ragcode/analyzers/javascript`.
- [ ] Implementation of basic parsing for **global functions** and **classes**.

### Phase 2: TypeScript & Metadata (Beta)
- [ ] Full support for type definitions (`interface`, `type`).
- [ ] Documentation extraction from JSDoc/TSDoc.
- [ ] Support for decorators (essential for NestJS/Angular).

### Phase 3: Framework Specialization (Production)
- [ ] Optimizations for **React** (props extraction from components).
- [ ] Support for `.vue` and `.svelte` files (via dedicated tree-sitter parsers).
- [ ] **Template Search**: Indexing variables and logic from HTML/Blade/JSX templates to allow context retrieval from frontend.
- [ ] Route detection (for Express, Koa, Fastify).

---

## 🛠️ How it will be used (Future)

Once implemented, the process will be automatic:
1. RagCode scans the workspace.
2. Detects `package.json` -> activates `javascript/typescript` analyzer.
3. Sends files to `CodeAnalyzerForProjectType("nodejs")`.
4. Generates vectors in the `ragcode-{workspace}-js` collection.

---

> **Note**: This document serves as a technical specification. Implementation will begin after consolidating the stability of v1.1.21.
