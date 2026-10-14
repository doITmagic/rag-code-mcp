# CSS / SCSS / LESS Parser (`pkg/parser/css`)

The `css` package analyzes stylesheets (`.css`, `.scss`, `.sass`, `.less`), extracting rulesets, media queries, keyframes, custom properties, and preprocessor mixins/variables into semantic symbols.

## 🎯 Extracted Symbols

1. **CSS Rulesets (`rule`)**: Selector signature and full declaration block with accurate start/end lines.
2. **Media Queries (`@media`)**: Responsive breakpoint rules and nested selectors.
3. **Keyframes (`@keyframes`)**: Animation definitions.
4. **Variables & Custom Properties (`variable`)**: CSS custom properties (`--color-primary`) and SCSS/LESS variables (`$primary`, `@primary`).
5. **SCSS Mixins (`@mixin`)**: Reusable stylesheet mixins with parameters.

## 📋 Technical Notes

- Handles both standard `/* ... */` block comments and SCSS/LESS `//` single-line comments without corrupting brace depth (`{` / `}`).
- Top-level `@charset`, `@import`, `@use`, `@forward`, and `$variable` declarations terminated by `;` are cleanly separated from subsequent selector rules.
