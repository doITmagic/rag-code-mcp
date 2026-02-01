# JavaScript & TypeScript Analyzer (Planned)

Planul de implementare pentru analizorul de cod JavaScript, TypeScript și framework-urile bazate pe acestea (React, Vue, NestJS). Acest analizor va folosi **Tree-sitter** pentru o parsare rapidă, incrementală și precisă, indiferent de complexitatea codului.

## Status: 🔜 PLANNED / ROADMAP

---

## 🎯 Obiective de Implementare

Spre deosebire de Go, unde folosim AST-ul nativ, pentru ecosistemul JS/TS vom adopta **Tree-sitter** pentru a asigura:
1. **Multilingvism**: Suport unitar pentru `.js`, `.ts`, `.jsx`, `.tsx`.
2. **Performanță**: Parsare ultra-rapidă și posibilitatea de indexare incrementală.
3. **Robustțe**: Tree-sitter poate parsa cod chiar și în timpul editării (error recovery), util pentru integrările IDE viitoare.

---

## 🏗️ Arhitectură Tehnică

### Dependențe
- `github.com/smacker/go-tree-sitter`: Binding-urile de Go pentru Tree-sitter.
- `github.com/tree-sitter/tree-sitter-javascript`: Parserul pentru JS.
- `github.com/tree-sitter/tree-sitter-typescript`: Parserul pentru TS și TSX.

### Integrare în RagCode
- Implementarea interfeței `PathAnalyzer` din `internal/ragcode/analyzers`.
- Maparea nodurilor Tree-sitter către structura canonică `CodeChunk` (v2).
- Detectarea automată a tipului de proiect (Node.js, React, Next.js) prin `package.json`.

---

## 🔍 Ce Vom Indexa

### 1. Funcții și Arrow Functions
```javascript
// Function Declaration
function calculateTotal(items) { ... }

// Arrow Function
const filterActive = (user) => user.active;
```
**Metadate**: Semnătură, parametri, JSDoc, tipuri (dacă e TS).

### 2. Clase și Metode
```typescript
class UserService {
    /** @param id user uid */
    async getUser(id: string): Promise<User> { ... }
}
```
**Metadate**: Nume clasă, vizibilitate (public/private), async/await, decoratori.

### 3. Componente React (JSX/TSX)
```tsx
export const UserProfile = ({ name }: Props) => {
    return <div>{name}</div>;
};
```
**Metadate**: Props, hook-uri utilizate (useState, useEffect), tipul componentei.

### 4. Importuri și Exporturi
- Identificarea dependențelor între fișiere pentru a ajuta modelul LLM să înțeleagă contextul global.

---

## 🗺️ Roadmap de Implementare

### Faza 1: Infrastructură (Alpha)
- [ ] Integrarea librăriei `go-tree-sitter`.
- [ ] Crearea structurii de foldere în `internal/ragcode/analyzers/javascript`.
- [ ] Implementarea parsării de bază pentru **funcții globale** și **clase**.

### Faza 2: TypeScript & Metadate (Beta)
- [ ] Suport complet pentru definiții de tipuri (`interface`, `type`).
- [ ] Extracția documentației din JSDoc/TSDoc.
- [ ] Suport pentru decoratori (esențial pentru NestJS/Angular).

### Faza 3: Framework Specialization (Production)
- [ ] Optimizări pentru **React** (extracție props din componente).
- [ ] Suport pentru fișiere `.vue` și `.svelte` (via tree-sitter parsers dedicate).
- [ ] **Template Search**: Indexarea variabilelor și a logicii din template-uri HTML/Blade/JSX pentru a permite regăsirea contextului din frontend.
- [ ] Detectarea rutelor (pentru Express, Koa, Fastify).

---

## 🛠️ Cum se va utiliza (Viitor)

Odată implementat, procesul va fi automat:
1. RagCode scanează workspace-ul.
2. Detectează `package.json` -> activează `javascript/typescript` analyzer.
3. Trimite fișierele către `CodeAnalyzerForProjectType("nodejs")`.
4. Generează vectori în colecția `ragcode-{workspace}-js`.

---

> **Notă**: Acest document servește ca specificație tehnică. Implementarea va începe după consolidarea stabilității v1.1.21.
