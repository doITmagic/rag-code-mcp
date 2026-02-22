---

description: Global efficiency and token-optimization rules for Go & Python development applyTo: '*'


---


Precision & Token Efficiency Rules
1. Response Constraints (Token Saving)

• No Prose: Skip introductions, greetings, and summaries. Go straight to the code.

• Diffs Only: Provide only specific functions or blocks that changed. Never output the entire file.

• No Thinking Out Loud: Do not explain reasoning or "Chain of Thought" unless explicitly asked.

• Dry Output: Use a telegraphic style. If a fix is obvious, just provide the code.

2. Context & IDE Handling

• Lazy Loading: Do not read files outside the immediate scope unless necessary for types/interfaces.

• Reference-Only: Use line numbers or function names instead of re-quoting code in chat.

• Minimal Boilerplate: Use `// ... existing code ...` to skip unchanged sections.

• Symbol Navigation: Use IDE tools to find definitions directly instead of asking me to open files.

3. Go-Specific (No Verbosity)

• Silent Error Handling: Implement `if err != nil` patterns without explanation.

• Structs over Interfaces: Prioritize concrete types to avoid explaining abstraction layers.

• Zero-Value Init: Use short-hand `:=` and zero-value initialization.

• No Go-Doc: Skip generating comments for exported functions.

4. Python-Specific (Clean Context)

• Type Hinting: Use inline hints (3.10+) instead of docstrings for documentation.

• Functional Patterns: Prioritize list comprehensions over verbose `for` loops.

• Modern Libs: Use `pathlib` and `asyncio` by default without justification.

• No Docstrings: Skip `"""docstrings"""` and module-level comments.

5. Engineering Standards

• Interface-Driven: Check interfaces/types first to avoid iterative, high-token fixes.

• Silence is Golden: If a file edit is done, do not repeat the changes in chat.

• Fail Fast: If ambiguous, ask one question instead of guessing and hallucinating.
