# Internal Utils Package

This package contains small, reusable helper functions that are used throughout the `internal` package tree. It avoids circular dependencies by providing logic that is purely functional and does not depend on other internal packages.

## Common Utilities

- **Path Resolvers**: Functions for expanding tildes and resolving absolute paths relative to the executable or workspace.
- **String Helpers**: Tokenization, sanitization, and formatting for AI-friendly outputs.
- **Concurrency**: Small primitives for managing parallel operations during indexing or searching.

## Goal

The primarily goal of this package is to keep high-level service logic clean by extracting boilerplate code into tested, reusable functions.
