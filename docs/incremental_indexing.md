# Incremental Indexing Architecture

This document explains the architecture and operational principles of the incremental indexing system in RagCode.

## Overview

Incremental indexing allows RagCode to update the search index efficiently by processing only the files that have changed since the last indexing run. This significantly reduces the time and computational resources required to keep the knowledge base up-to-date.

## Core Principles

The system relies on three main concepts:
1.  **State Tracking**: Remembering the state (modification time, size) of files from the previous run.
2.  **Change Detection**: Comparing the current file system state against the saved state to identify added, modified, or deleted files.
3.  **Selective Updates**: Updating the vector database (Qdrant) only for the affected files.

## Architecture Components

### 1. Workspace State (`state.json`)
The state of the workspace is persisted in a JSON file located at `.ragcode/state.json` within the workspace root.

**Structure:**
```json
{
  "files": {
    "/path/to/file.go": {
      "mod_time": "2023-10-27T10:00:00Z",
      "size": 1024
    }
  },
  "last_indexed": "2023-10-27T10:05:00Z"
}
```

### 2. The Indexing Workflow

When `index_workspace` is called, the following process occurs:

```mermaid
graph TD
    A[Start Indexing] --> B{Collection Exists?}
    B -- No --> C[Full Indexing]
    B -- Yes --> D[Load State (.ragcode/state.json)]
    D --> E[Scan Current Files]
    E --> F{Compare with State}
    F -->|New/Modified| G[Add to Index List]
    F -->|Deleted/Modified| H[Add to Delete List]
    F -->|Unchanged| I[Ignore]
    
    H --> J[Delete Old Chunks from Qdrant]
    G --> K[Index New Content]
    
    J --> L[Update State]
    K --> L
    L --> M[Save State]
    M --> N[Finish]
```

### 3. Detailed Steps

#### Step 1: Detection & Loading
The `WorkspaceManager` detects the workspace and attempts to load `.ragcode/state.json`. If the file doesn't exist, it assumes a fresh state.

#### Step 2: Change Detection (Diffing)
The system iterates through all currently detected source files for the target language:
- **Modified**: If a file exists in the state but has a different `mod_time` or `size`, it is marked for re-indexing.
- **New**: If a file is not in the state, it is marked for indexing.
- **Deleted**: If a file is in the state but no longer exists on disk, it is marked for deletion.

#### Step 3: Cleaning Stale Data
For every file marked as **Modified** or **Deleted**, the system performs a cleanup in the vector database.
- It calls `DeleteByMetadata(ctx, "file", filePath)`.
- This removes all code chunks associated with that specific file path, ensuring no duplicate or phantom results remain.

#### Step 4: Indexing
The system runs the standard indexing pipeline (Analyzer -> Chunker -> Embedder -> Vector DB) **only** for the list of new or modified files.

#### Step 5: State Persistence
Finally, the in-memory state is updated with the new file information, and `state.json` is rewritten to disk.

## Benefits

- **Speed**: Re-indexing a project with thousands of files takes seconds if only a few files changed.
- **Efficiency**: Reduces LLM embedding costs by not re-embedding unchanged code.
- **Consistency**: Ensures the search index accurately reflects the current code, including deletions.
