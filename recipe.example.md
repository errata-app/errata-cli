# My Project Recipe

## Metadata
name: My Project Recipe
description: Full-featured example recipe demonstrating every configuration option
tags: go, example, reference
author: yourname
version: 1.0
extends: ~/.errata/recipes/base.md
contribute: false
project_root: /path/to/project

## Models
- claude-sonnet-4-6
- gpt-4o
- gemini-2.0-flash

## System Prompt
You are working on a Go monorepo. Always run `go vet ./...` and `go test ./...` after
making any changes. Use conventional commits (feat:, fix:, chore:). Keep functions
small and focused; prefer table-driven tests.

## Tools
- read_file
- list_directory
- search_files
- search_code
- edit_file
- write_file
- web_fetch
- web_search
- bash(go test *, go build *, go vet *, gofmt *, make *)

## MCP Servers
- exa: npx @exa-ai/exa-mcp-server
- brave: npx @modelcontextprotocol/server-brave-search

## Model Parameters
seed: 42

## Constraints
timeout: 10m
max_steps: 50

## Context
max_history_turns: 30
strategy: auto_compact
compact_threshold: 0.75
task_mode: independent

## Sub-Agent
model: claude-sonnet-4-6
max_depth: 2
tools: inherit

## Sandbox
filesystem: project_only
network: full

## Tasks
- Audit all Go files for unused error return values and fix them
- Add table-driven tests for any function with fewer than two test cases
- Run `go vet ./...` and resolve every reported issue

## Success Criteria
- no_errors
- has_writes
