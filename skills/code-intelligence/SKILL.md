---
name: code-intelligence
description: Use the Atomic knowledge graph, tree-sitter, and content search to explore code structure and relationships. You ARE the agent — use these tools directly instead of grep and find.
---

# Code Intelligence

You have direct access to the Atomic knowledge graph and content search index through CLI commands. Use these instead of grep and find.

## Tools

### Search Source Code Content

```
atomic vault query code "replication"
atomic vault query code "replication" -g "src/db/repl/"
atomic vault query code "fn parse_query" -t rs
atomic vault query code "TODO" -t cpp --json
```

Searches the actual source code — function bodies, comments, string literals, everything. Supports Rust-style regex, path filtering (`-g`), and file type filtering (`-t`). This is your replacement for grep.

**Regex syntax:** Use Rust/modern regex syntax, not basic `grep` syntax. For alternation, use plain `|` inside the quoted pattern:

```
atomic vault query code "frame_pointer|frame pointer|fp_unwind|fp unwind" -t rs
atomic vault query code "resolve|symbolize|Backtrace::from" -t rs -g "tokio/src/runtime"
```

Do **not** escape alternation as `\|`. A pattern like `foo\|bar` searches for a literal pipe in this command, so it will usually return no matches.

### Search the Knowledge Graph

```
atomic vault query search "keyword"
atomic vault query search "keyword" --json
```

Returns structural nodes — files, entities (functions/classes/types), changes, views, goals, intents. Use short, specific terms. This searches names and metadata, not source content.

**Good:** `"record"`, `"ViewScope"`, `"authentication"`, `"materialize"`
**Bad:** `"how does the record workflow create changes"` — that's a question for you to answer, not a search query.

### Explore Neighbors

```
atomic vault query neighbors <node_id>
atomic vault query neighbors <node_id> --json
```

Shows all nodes directly connected to the given node. This is how you follow relationships:
- File → entities it defines (via DEFINES edges)
- Change → files it modified (via MODIFIES edges)
- Change → who authored it (via AUTHORED_BY edges)
- Entity → changes that touched it, files that contain it

**Never construct node IDs yourself.** Copy them verbatim from search results.

### List Entities in a File

```
atomic vault query entities src/main.rs
atomic vault query entities src/main.rs --json
```

Lists every function, class, struct, trait, type, and constant in a file using tree-sitter AST extraction. Returns name, kind, line range, exported flag, and signature.

Use this instead of reading a whole file to understand its structure. It's a table of contents.

### Read a Vault Entry

```
atomic vault show <path>
```

Reads goals, intents, memories, and skills from the vault. Use this to check project context, architectural decisions, and work history.

## Node ID Format

| Prefix | Format | Example |
|--------|--------|---------|
| `entity:` | `entity:file:name:line` | `entity:src/apply/mod.rs:write_change_to_graph:42` |
| `file:` | `file:path` | `file:atomic-core/src/pristine/traits.rs` |
| `change:` | `change:HASH` | `change:R4YQUAS2UZV5` |
| `view:` | `view:name` | `view:main` |
| `intent:` | `intent:ID` | `intent:ATOM-42` |
| `goal:` | `goal:name` | `goal:ambient-graph-phase-9` |

## When to Use What

| Goal | Tool | NOT this |
|------|------|----------|
| Find text in source code | `code "pattern"` | ~~grep~~ |
| Find where something is defined | `search` or `entities` | ~~grep for function names~~ |
| See what a file contains | `entities` | ~~read the whole file~~ |
| Find files containing a term | `code "term" -t rs` | ~~find_path with glob~~ |
| Understand what depends on what | `neighbors` on a node | ~~grep for import statements~~ |
| See what a change affected | `neighbors` on a `change:` node | ~~git log / git diff~~ |
| See all entities in a file | `entities` | ~~read_file then scan visually~~ |
| Read actual implementation | `read_file` (your built-in tool) | Only after `code` or `entities` tells you the line range |

## Workflow

1. **Search content** for the concept: `atomic vault query code "replication" -t cpp`
2. **Search structure** if you need relationships: `atomic vault query search "replication"`
3. **Explore** a result: `atomic vault query neighbors file:src/db/repl/replication_coordinator.cpp`
4. **List entities** for the file structure: `atomic vault query entities src/db/repl/replication_coordinator.cpp`
5. **Read** the specific lines you need with your built-in `read_file` tool

`code` searches source content (like grep but indexed). `search` searches the KG structure (names, metadata, relationships). Use both.

You are the reasoning loop. You don't need `atomic vault query ask` — that command exists for humans on the terminal who don't have you in the loop.

## Enriching the Knowledge Graph

If searches return sparse results, the KG may need populating:

```
atomic vault query enrich
```

This extracts file nodes, change history, and tree-sitter entities into the KG. Run it once after importing a repo or when results seem thin.

## Tips

- `code` replaces grep — use it for any text search across the codebase
- `search` + `entities` finds structure (definitions, relationships) not raw text
- `neighbors` on a `file:` node is a table of contents — every entity and every change that touched the file
- Chain: code → search → neighbors → entities → read_file. Each step narrows your focus.
- The content index and KG are built by `atomic vault query enrich` — run it if results seem sparse
- Node IDs are exact strings — a single wrong character means "not found"
- Use `--json` on any command when you need structured output for further processing