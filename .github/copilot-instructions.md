# openmelon Development Guidelines

Auto-generated from all feature plans. Last updated: 2026-05-05

## Active Technologies
- Go 1.22 + 标准库（encoding/json, os/exec, crypto/sha256, flag, context）— 零外部依赖 (001-phase2-runtime-skeleton)
- 本地文件系统（.txt artifacts + .jsonl provenance 追加日志） (001-phase2-runtime-skeleton)

- (001-phase2-runtime-skeleton)

## Project Structure

```text
src/
tests/
```

## Commands

# Add commands for 

## Code Style

: Follow standard conventions

## Recent Changes
- 002-openrouter-llm-pipeline: Added [if applicable, e.g., PostgreSQL, CoreData, files or N/A]
- 002-openrouter-llm-pipeline: Added [if applicable, e.g., PostgreSQL, CoreData, files or N/A]
- 001-phase2-runtime-skeleton: Added Go 1.22 + 标准库（encoding/json, os/exec, crypto/sha256, flag, context）— 零外部依赖


<!-- MANUAL ADDITIONS START -->
## 002-openrouter-llm-pipeline: LLM Provider 接入

**新增模块**:
- `internal/generation/llm_provider.go` — 实现 `generation.Provider`，内部使用 `internal/llm.Client`（OpenRouter / OpenAI / Anthropic）
- `internal/generation/provider.go` — `Request` 新增 `Intent string` 字段（System/User 分割）

**CLI 新增 flags**: `--llm <provider>`, `--llm-model <model_id>`（与 `--generate-cmd` 互斥）

**API Key**: 环境变量 `OPENROUTER_API_KEY` / `OPENAI_API_KEY` / `ANTHROPIC_API_KEY`，不经 CLI

**TTY 检测**: `os.Stdout.Stat()` + `ModeCharDevice`，stdlib only

**Provenance**: `Record.Model` 改为记录 trace 中的实际 model ID（如 `google/gemini-2.0-flash-001`）

**新 Skills** (skillplus 仓库):
- `travel-street-realism` — kind `visual_prompt_concretization`, locale `zh-CN`
- `post-caption-xiaohongshu` — kind `copy_draft`, stage `copywriting`, locale `zh-CN`
<!-- MANUAL ADDITIONS END -->
