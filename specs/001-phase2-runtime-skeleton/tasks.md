# Tasks: OpenMelon Phase 2 — Runtime Skeleton

**Branch**: `001-phase2-runtime-skeleton`  
**Generated**: 2026-05-04  
**Spec**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md)

## Summary

- **Total tasks**: 20
- **Foundational**: 2 | **US2**: 2 | **US3**: 2 | **US4**: 3 | **US5**: 4 | **US1**: 5 | **Polish**: 2
- **Parallelizable**: 12 tasks marked [P]
- **MVP scope**: Phase 3 (US2) + Phase 4 (US3) — project loading + skill compile, independently demoed

---

## Phase 2: Foundational (必须在 User Story 阶段前完成)

> 这两个任务阻塞 US5 (T011) 和 US1 (T014/T015)。可并行执行。

**Independent Test**: 完成后可执行 `./openmelon run --project examples/food-exploration/project.json ...` 并看到项目文件被正确加载（即使 engine 尚未实现）。

- [x] T001 [P] 创建 examples/food-exploration/project.json，按 CLI contract 定义的 JSON schema，包含 food_exploration workflow 定义（从 beef-noodles.json 中拆分 project + workflow 字段）
- [x] T002 [P] 在 internal/provenance/provenance.go 的 Record struct 中增加 `ArtifactID string`、`WorkflowID string` 和 `Timestamp string` 字段（RFC3339）

---

## Phase 3: User Story 2 — Project 从文件加载 (P1)

> **Story Goal**: 系统能从独立 JSON 文件加载 Project，校验必填字段，返回明确错误。  
> **Independent Test**: 调用 `project.Load("examples/food-exploration/project.json")` 返回正确 Project 对象；缺少 id 字段时返回含字段名的错误；文件不存在时返回 os.ErrNotExist 包装错误。

- [x] T003 [US2] 创建 internal/project/load.go，实现 `Load(path string) (*Project, error)` 函数：读取 JSON、反序列化到 Project struct、校验 ID/Name/Platform 必填字段
- [x] T004 [P] [US2] 创建 internal/project/load_test.go，包含三个表驱动测试用例：合法 JSON / 缺少必填字段 id / 文件不存在

---

## Phase 4: User Story 3 — Skill-Plus Adapter 正式化 (P1)

> **Story Goal**: `internal/skillplus` 包提供可测试的 `Compiler.Compile(ctx, req)` 方法，封装 Python subprocess 调用。  
> **Independent Test**: 构造 `Compiler{CompilerPath: "..."}` 并调用 `Compile(ctx, req)` — 单元测试中用 mock exec 路径（TestCompiler_pythonNotFound, TestCompiler_successMockExec）。

- [x] T005 [US3] 创建 internal/skillplus/compiler.go，定义 `CompileRequest` struct、`Compiler` struct（含 `CompilerPath string`）和 `Compile(ctx context.Context, req *CompileRequest) (*CompiledSkill, error)` 方法：验证 python3 可用（exec.LookPath）、设置 PYTHONPATH、分离捕获 stdout/stderr、反序列化 JSON 输出到 CompiledSkill
- [x] T006 [P] [US3] 创建 internal/skillplus/compiler_test.go，测试：python3 不可用时返回含安装提示的错误（mock LookPath）、stdout 输出合法 JSON 时正确反序列化（用 `echo` 作为 fake python3）

---

## Phase 5: User Story 4 — Model Provider 接口与 ShellProvider (P2)

> **Story Goal**: `internal/generation` 包定义 `Provider` 接口，`ShellProvider` 实现通过 shell command 调用外部工具。  
> **Independent Test**: `ShellProvider{Command: "echo"}` 调用 `Generate(ctx, req)` 返回 prompt 文本；非零退出码返回含 stderr 的错误；context 超时时进程被终止。

- [x] T007 [US4] 创建 internal/generation/provider.go，定义 `Provider` 接口（`Generate(ctx context.Context, req *Request) (content string, trace *Trace, err error)`）、`Trace` struct（ProviderType/Model/Command/DurationSec）、`ProviderError` struct（Code/Message/Wrapped）
- [x] T008 [US4] 创建 internal/generation/shell_provider.go，实现 `ShellProvider`（含 `Command string`、`Env map[string]string`）：构建 exec.CommandContext、设置环境变量、分离 stdout/stderr、计时、返回 Trace
- [x] T009 [P] [US4] 创建 internal/generation/shell_provider_test.go，三个测试：echo 命令成功返回内容 / 非零退出码包含 stderr / context 超时触发 ProviderError{Code:"timeout"}

---

## Phase 6: User Story 5 — Artifact 与 Provenance 落盘 (P2)

> **Story Goal**: artifact 内容写入磁盘文件，provenance 记录追加到 provenance.jsonl。  
> **Independent Test**: 调用 `artifact.Write(dir)` 后目录下出现 `{id}.image_prompt.txt`；调用 `provenance.AppendRecord` 两次后 provenance.jsonl 有两行 JSON；目录不存在时自动创建。

- [x] T010 [P] [US5] 创建 internal/artifacts/writer.go，实现：`StableID(parts ...string) string`（SHA256 hex[:16]）和 `Write(dir string, a *Artifact) error`（创建目录、写 `{id}.{type}.txt`、写 `{id}.provenance.json` 快照）
- [x] T011 [P] [US5] 创建 internal/provenance/writer.go，实现 `AppendRecord(path string, rec *Record) error`：`os.OpenFile(O_APPEND|O_WRONLY|O_CREATE, 0o644)`、`json.Marshal` + 追加 `\n`、`f.Sync()`；路径目录不存在时先 `os.MkdirAll`
- [x] T012 [P] [US5] 创建 internal/artifacts/writer_test.go：TestStableID_deterministic（相同输入输出相同 ID）、TestWrite_createsFiles（验证两个输出文件存在且内容正确）、TestWrite_mkdirIfNotExists
- [x] T013 [P] [US5] 创建 internal/provenance/writer_test.go：TestAppendRecord_createsFile、TestAppendRecord_appendsMultiple（两次调用 → 两行 JSON）、TestAppendRecord_mkdirIfNotExists

---

## Phase 7: User Story 1 — 完整工作流端到端执行 (P1)

> **Story Goal**: `openmelon run` 命令从 project.json 加载项目、执行 workflow 所有 stage（compile → generate → write artifact + provenance），输出 artifact 文件。  
> **Independent Test**: 执行 `./openmelon run --project examples/food-exploration/project.json --workflow food_exploration --intent "..." --generate=false` 产出 `.image_prompt.txt` 和 `provenance.jsonl`，exit code 0。

- [x] T014 [P] [US1] 创建 internal/workflow/definition.go，定义 `WorkflowDefinition`（ID/Name/Vertical/Stages []StageDefinition）和 `StageDefinition`（Stage/SkillPlusPackage/CompileTarget/ModelProfile/Vars/Locale）带 JSON tags；实现 `LoadWorkflows(path string) (map[string]*WorkflowDefinition, error)` 从 project.json 读取 workflows 字段
- [x] T015 [US1] 创建 internal/workflow/engine.go，定义 `RunRequest`/`RunResult` struct 和 `Engine.Run(ctx, req)` 方法：foreach stage → Compiler.Compile → Provider.Generate（仅当 req.Generate==true）→ artifacts.Write → provenance.AppendRecord；任意步骤失败返回含 stage 名称的 fmt.Errorf 包装错误
- [x] T016 [P] [US1] 更新 config/openmelon.example.json，加入 models map（gpt-image-family shell provider 示例）和 routing 字段，格式匹配 CLI contract 的 model config schema
- [x] T017 [US1] 重构 cmd/openmelon/main.go：删除内联业务逻辑结构体和函数，改为 `flag.NewFlagSet("run")` 解析 --project/--workflow/--intent/--artifact-dir/--compiler/--generate/--timeout flags，调用 `project.Load`、`workflow.LoadWorkflows`、`engine.Run`，打印 `[openmelon]` 前缀进度日志
- [x] T018 [US1] 创建 internal/workflow/engine_integration_test.go（`//go:build integration`），使用 food-exploration/project.json + ShellProvider{Command:"echo"} 端到端测试：断言产出 `.image_prompt.txt` 存在 + provenance.jsonl 可解析

---

## Phase 8: Polish & Cross-cutting

- [x] T019 执行 `go build ./...`，修复所有编译错误，确保零 warning
- [x] T020 执行 `go test ./...`，确认全部单元测试通过；执行 `go test ./... -tags integration` 确认集成测试通过

---

## Dependencies

```
T001 ──► T003 ──► T015
T002 ──► T011 ──► T015
T005 ──► T015
T007 ──► T008 ──► T015
T010 ──► T015
T014 ──► T015 ──► T017 ──► T018 ──► T020

T003 ──► T004
T005 ──► T006
T008 ──► T009
T010 ──► T012
T011 ──► T013

T017 ──► T019
T018 ──► T020
```

**Story completion order**:

| 顺序 | Story | 前置条件 | 可独立测试 |
|------|-------|----------|-----------|
| 1 | US2 (T003-T004) | T001 | ✅ 独立单元测试 |
| 2 | US3 (T005-T006) | 无 | ✅ 独立单元测试 |
| 3 | US4 (T007-T009) | 无 | ✅ 独立单元测试 |
| 4 | US5 (T010-T013) | T002 | ✅ 独立单元测试 |
| 5 | US1 (T014-T018) | US2+US3+US4+US5 完成 | ✅ 集成测试 |

---

## Parallel Execution Examples

### US2 + US3 + US4 可同时进行（无交叉依赖）

```
Worker A: T001 → T003 → T004   (examples/ + project loading)
Worker B: T002 → T011 → T013   (provenance struct + writer)
Worker C: T005 → T006           (skillplus compiler)
Worker D: T007 → T008 → T009   (generation provider)
Worker E: T010 → T012           (artifact writer)
```

### US1 只能在所有前置完成后开始

```
[US2+US3+US4+US5 全部完成] → T014 → T015 → T017 → T018
                              T016 ─────────────────────────▲  (parallel with T014)
```

---

## Implementation Strategy

**MVP（最小可验证交付）**: Phase 3 + Phase 4（T001–T006）— 完成后可展示"从 project.json 加载项目并编译 Skill-Plus 包"。

**完整 Phase 2**: 全部 T001–T020 — 完成后可用一条 `openmelon run` 命令从意图到 artifact 文件落盘。

**推荐顺序**: Foundational（T001/T002 并行）→ US2/US3 并行 → US4/US5 并行 → US1 → Polish。
