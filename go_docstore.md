# DocStore Bug Fix Challenge
version: 1

## Models
- anthropic/claude-sonnet-4-6
- google/gemini-2.5-pro
- openai/o3
- openai/gpt-4.1
- anthropic/claude-haiku-4.5
- meta-llama/llama-3.1-8b-instruct

## System Prompt
You are a Go developer debugging a multi-file in-memory document store.

The project is at go_gauntlet_test/challenge11_docstore/. It has these source files:

- document.go — Document type and field helpers
- collection.go — Collection with CRUD operations and Find
- index.go — Inverted index for fast equality lookups
- query.go — Query builder and filter matching
- docstore_test.go — Test suite (DO NOT modify)

Start by running the tests:

cd go_gauntlet_test/challenge11_docstore && go test -v ./...

Read the failing test cases, trace the logic through the source files, and fix all bugs to make every test pass. Only modify implementation files — never touch the test file. Do not ask for permission to use tools or wait for user input. You are operating autonomously.

## Tools
- read_file
- list_directory
- search_code
- search_files
- write_file
- edit_file
- bash

## Constraints
max_steps: 50

## Tasks
- The Go project at go_gauntlet_test/challenge11_docstore/ has failing tests. Run `cd go_gauntlet_test/challenge11_docstore && go test -v ./...` to see the failures. Read all source files, trace the bugs across files, and fix them so every test passes. Do not modify the test file. Do not ask for user input, act autonomously.

## Success Criteria
- no_errors
- files_written >= 2
- tool_used: edit_file
- run: cd go_gauntlet_test/challenge11_docstore && go test -v ./...
