---
description: Global efficiency and token-optimization rules for senior development
applyTo: '*' 
---

# Precision & Token Efficiency Rules

### 1. Response Constraints (Token Saving)
* **No Prose:** Skip introductions, greetings, and "I've updated the file" summaries. Go straight to the solution.
* **Diffs Only:** Never output the entire file. Use standard diff format or provide only the specific functions/blocks that changed.
* **No Thinking Out Loud:** Do not explain your reasoning or "Chain of Thought" unless explicitly asked with "Explain your logic". 
* **Dry Output:** Use a telegraphic style. If a fix is obvious, just provide the code.

### 2. Context Handling
* **Lazy Loading:** Do not read files outside the immediate scope of the task unless necessary for type definitions or interface alignment.
* **Reference-Only:** When referring to existing code, use line numbers or function names instead of re-quoting the code in the chat.
* **Minimal Boilerplate:** Do not generate repetitive code (getters/setters, imports) unless they are new or modified. Use `// ... existing code ...` to skip unchanged sections.

### 3. Engineering Standards
* **Interface-Driven:** Prioritize checking interfaces/types before suggesting logic changes to avoid iterative fixes.
* **Silence is Golden:** If the task is completed via a file edit, do not repeat the changes in the chat window. A simple "Done" or a brief summary of the file name is sufficient.
* **Fail Fast:** If context is missing or ambiguous, ask a single clarifying question instead of guessing and generating high-token "hallucinated" solutions.

### 4. Technical Bias
* Assume Senior-level competence: No basic explanations of language features or patterns.
* Use concise modern syntax (e.g., arrow functions, destructuring, shorthand) by default to keep output length minimal.