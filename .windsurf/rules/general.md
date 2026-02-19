---
trigger: always_on
---

AI Governance Rules – Project Control Mode (Strict)
1. General Behavior Rules

AI must never modify existing code unless:

A bug is proven.

A security issue is identified.

A performance bottleneck is demonstrated with measurable data.

The user explicitly approves the change.

AI must never:

Refactor for style preference.

Rename variables for aesthetics.

Reorganize project structure.

“Improve readability” unless requested.

All suggested changes must:

Be shown as a diff.

Include explanation.

Include impact analysis.

Include rollback instructions.

No change is considered successful unless:

Tests pass.

Build passes.

Lint passes.

Manual verification steps are described.

AI must ask for clarification if:

Business logic is unclear.

There is ambiguity in naming.

Multiple valid architectural choices exist.

🧠 Decision Gatekeeping Rule

Before proposing ANY code change, AI must answer internally:

Is this required?

Is this measurable?

Is this aligned with existing architecture?

Is this backward compatible?

If one answer is "no" → ASK FIRST.

🧪 Testing Requirements (Mandatory)
Golang

Every backend change must include:

Unit test

Table-driven test when applicable

Error case coverage

Benchmark if performance-related

go vet clean

golangci-lint clean

No “looks correct”. Only tested code.

JavaScript

If JS is touched:

No global scope pollution

No magic numbers

Defensive null checks

No breaking of existing event bindings

Must verify browser compatibility assumptions

If DOM is modified:

Ensure no layout shift unless intended

Verify no performance regression (reflow-heavy operations)

CSS

AI must:

Not change global styles without explicit approval

Not introduce !important unless justified

Not break mobile layout

Not modify spacing system

Follow existing naming convention (BEM or whatever you use)

HTML

AI must:

Not restructure markup unless required

Preserve accessibility attributes

Not remove ARIA roles

Not remove semantic tags

Keep forms backward compatible

🚫 Optimization Control Rules

AI must NOT:

Optimize prematurely

Replace loops with concurrency without justification

Introduce goroutines unless concurrency is proven necessary

Add caching without defining invalidation strategy

Change data structures without complexity analysis

🔐 Backend Specific (Golang)

AI must:

Not introduce global state

Not introduce hidden side effects

Respect context propagation (context.Context)

Not swallow errors

Always wrap errors properly

Not introduce reflection unless necessary

Not introduce generics unless justified

If touching DB:

Must preserve transaction boundaries

Must describe migration impact

Must describe rollback strategy

📦 Dependency Rules

AI must:

Not introduce new dependencies without approval

Justify every dependency

Prefer standard library

Check license compatibility

Evaluate maintenance status

📊 Reporting Rules

AI must never say:

“It should work.”

“This is fixed.”

“Problem solved.”

Instead must report:

What was changed

What was tested

What was verified

What was not verified

Potential side effects

🔍 Change Proposal Format (Mandatory)

Every change must follow this structure:

Problem Description

Root Cause Analysis

Proposed Change (diff)

Impact Scope

Risk Assessment

Test Plan

Rollback Plan

No exceptions.

🧱 Architecture Preservation Rule

AI must:

Preserve layering (handler → service → repository)

Not bypass service layer

Not mix transport and business logic

Not leak DB models into API layer

Not couple JS tightly to backend implementation details

⚠️ Failure Handling Rules

AI must:

Never ignore error return values

Never panic in production paths

Not suppress logs

Use structured logging

📈 Performance Rules

Before suggesting performance optimization, AI must provide:

Current complexity (Big O)

Proposed complexity

Expected gain

Trade-offs

Benchmark example

🧭 When AI Must Stop and Ask

When modifying authentication

When modifying authorization

When touching financial logic

When changing concurrency model

When modifying public API

💬 Strict Interaction Rule

AI must:

Treat all existing code as intentional

Assume developer competence

Not override architectural decisions

Provide alternatives, not unilateral decisions