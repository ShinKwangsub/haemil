# claw-code 상세 분석

> 분석일: 2026-04-10
> 분석 범위: rust/ 디렉토리 전체 (78개 .rs 파일, 75,019줄)

---

## 1. 개요

| 항목 | 내용 |
|------|------|
| 한줄 설명 | Claude Code의 Rust 재구현체 — CLI 기반 AI 코딩 에이전트 런타임 |
| 언어 | Rust (Edition 2021) |
| 라이선스 | MIT |
| 바이너리명 | `claw` |
| Workspace | 9개 크레이트 (monorepo) |
| 총 코드량 | 75,019줄 (Rust만), Python/기타 별도 존재 |
| unsafe 정책 | `unsafe_code = "forbid"` (전면 금지) |

---

## 2. 아키텍처

### 2.1 크레이트 의존성 구조

```
rusty-claude-cli (바이너리 "claw", ~13,706줄)
├── api (7,164줄)        → runtime, telemetry
├── commands (5,549줄)   → runtime, plugins
├── compat-harness (357줄) → commands, tools, runtime
├── runtime (28,712줄)   → plugins, telemetry  ← 핵심
├── plugins (4,020줄)    → serde (독립)
├── tools (9,182줄)      → api, commands, plugins, runtime
└── telemetry (526줄)    → serde (독립)
```

### 2.2 크레이트별 역할

| 크레이트 | 줄 수 | 역할 |
|----------|-------|------|
| **runtime** | 28,712 | 핵심 엔진 — 대화 루프, 세션, 권한, MCP, 복구 |
| **rusty-claude-cli** | 13,706 | CLI 진입점 — REPL, Markdown 렌더링, 초기화 |
| **tools** | 9,182 | 도구 레지스트리 — 파일/bash/MCP 도구 실행 |
| **api** | 7,164 | API 클라이언트 — Anthropic/OpenAI/Xai, SSE, 캐시 |
| **commands** | 5,549 | 슬래시 명령 — 50+ 명령 정의 및 실행 |
| **plugins** | 4,020 | 플러그인 시스템 — 매니페스트, Hook, 마켓플레이스 |
| **telemetry** | 526 | 텔레메트리 — 이벤트 추적, Session Tracer |
| **compat-harness** | 357 | 호환성 — TypeScript 원본 소스와 패리티 유지 |

### 2.3 데이터 흐름 (한 턴의 생명주기)

```
① Bootstrap (12단계)
   CLI Entry → 설정 로드 → MCP 초기화 → Worker 준비 → MainRuntime
       ↓
② User Input → Session에 기록
       ↓
③ Prompt Assembly (SystemPromptBuilder)
   기본 프롬프트 + OS 정보 + Git 상태 + CLAUDE.md + 동적 경계
       ↓
④ API Stream (ConversationRuntime::run_turn)
   ApiClient::stream(request) → Vec<AssistantEvent>
       ↓
⑤ Pre-Tool-Use Hook → Permission 평가 → Bash Validation
       ↓
⑥ Tool Execution (bash / file_ops / MCP)
       ↓
⑦ Post-Tool-Use Hook → Tool Result → Session에 기록
       ↓
⑧ Loop Decision: ToolUse 남아있으면 ④로 (max_iterations까지)
       ↓
⑨ Auto-Compaction (토큰 초과 시 LLM 요약)
       ↓
⑩ TurnSummary 반환
```

### 2.4 보안 아키텍처 (4단계 방어)

```
Layer 1: bash_validation.rs
  └─ 명령어 의도 분류 (ReadOnly/Write/Destructive/Network/...)
  └─ 모드별 Allow/Block/Warn 판정

Layer 2: permissions.rs + permission_enforcer.rs
  └─ PermissionMode (ReadOnly → WorkspaceWrite → DangerFullAccess)
  └─ Tool별 required mode vs active mode 비교
  └─ Allow/Deny/Ask 규칙 평가

Layer 3: policy_engine.rs
  └─ PolicyRule (condition + action + priority)
  └─ 복합 조건 (AND/OR), 체인 액션 지원

Layer 4: sandbox.rs
  └─ FilesystemIsolationMode (Off/WorkspaceOnly/AllowList)
  └─ Container 감지 (Docker/Podman)
  └─ Linux namespace 격리
```

---

## 3. 핵심 소스 분석

### 3.1 runtime 크레이트 (42개 파일)

#### 대화 엔진

| 파일 | 줄 수 | 역할 | 핵심 패턴 |
|------|-------|------|-----------|
| conversation.rs | 1,699 | 대화 루프 엔진 | `ConversationRuntime<C,T>` 제네릭 — ApiClient + ToolExecutor 트레이트 주입 |
| session.rs | 1,515 | 세션 지속성 | JSON 파일 기반, 256KB 로테이션, fork 추적 |
| session_control.rs | 873 | 세션 저장소 | SessionStore — 로드/저장/메모리 관리 |
| compact.rs | 827 | 세션 압축 | 토큰 추정 → LLM 요약 → continuation message 삽입 |
| summary_compression.rs | 300 | 요약 압축 | 라인 선택 (priority), char/line budget 제한 |
| prompt.rs | 905 | 시스템 프롬프트 | SystemPromptBuilder — git 상태, instruction 파일 동적 포함 |

#### 보안/권한

| 파일 | 줄 수 | 역할 | 핵심 패턴 |
|------|-------|------|-----------|
| bash_validation.rs | 1,004 | Bash 명령 검증 | CommandIntent 분류 → 모드별 Allow/Block/Warn |
| permissions.rs | 683 | 권한 정책 | 3단계 모드 + Allow/Deny/Ask 규칙 평가 |
| permission_enforcer.rs | 551 | 권한 집행 | check_file_write(), check_bash() 게이트 |
| policy_engine.rs | 581 | 정책 엔진 | 선언적 규칙 (조건+액션+우선순위) |
| sandbox.rs | 385 | 샌드박스 | 파일시스템 격리, Container 감지 |

#### MCP 통합

| 파일 | 줄 수 | 역할 | 핵심 패턴 |
|------|-------|------|-----------|
| mcp_stdio.rs | 2,928 | MCP stdio transport | JSON-RPC 2.0, 프로세스 spawn, tool discovery |
| mcp_tool_bridge.rs | 920 | MCP 도구 브릿지 | 서버별 도구 레지스트리, 연결 상태 추적 |
| mcp_lifecycle_hardened.rs | 843 | MCP 라이프사이클 | 11단계 (ConfigLoad→Ready→Shutdown), 에러 추적 |
| mcp_server.rs | 440 | MCP 서버 구현 | JSON-RPC dispatcher (initialize/tools/list/call) |
| mcp_client.rs | 248 | MCP 클라이언트 인터페이스 | McpClientTransport trait |
| mcp.rs | 304 | MCP 유틸리티 | tool name 정규화 (`mcp__{server}__{tool}`) |

#### 도구 실행

| 파일 | 줄 수 | 역할 | 핵심 패턴 |
|------|-------|------|-----------|
| file_ops.rs | 839 | 파일 작업 | read/write/edit + glob + grep, 10MB 제한 |
| bash.rs | 336 | Bash 실행 | command execution, stderr/stdout 캡처 |
| hooks.rs | 987 | 훅 시스템 | Pre/Post ToolUse, permission override, input 수정 |

#### 설정/부트스트랩

| 파일 | 줄 수 | 역할 | 핵심 패턴 |
|------|-------|------|-----------|
| config.rs | 2,111 | 설정 시스템 | 다중 소스 병합 (글로벌→프로젝트→로컬), MCP/권한/OAuth 설정 |
| config_validate.rs | 901 | 설정 검증 | diagnostic 시스템, 타입 검사 |
| bootstrap.rs | 111 | 부트스트랩 | BootstrapPlan 12단계 초기화 |

#### 복구/상태관리

| 파일 | 줄 수 | 역할 | 핵심 패턴 |
|------|-------|------|-----------|
| recovery_recipes.rs | 631 | 복구 레시피 | 6개 시나리오, max_attempts, escalation 정책 |
| worker_boot.rs | 1,180 | Worker 상태 머신 | Spawning→TrustRequired→Ready→Running→Finished |
| plugin_lifecycle.rs | 533 | 플러그인 라이프사이클 | healthcheck, degraded mode |
| stale_base.rs | 429 | Stale base 검사 | git base commit freshness |
| stale_branch.rs | 417 | Stale branch 정책 | branch 나이 > 1시간 시 경고 |
| branch_lock.rs | 144 | Branch lock | 병렬 lane 충돌 방지 |

#### 기타

| 파일 | 줄 수 | 역할 |
|------|-------|------|
| git_context.rs | 324 | Git 컨텍스트 (branch, commits, diff) |
| usage.rs | 313 | 토큰 사용량 추적, 모델별 가격 계산 |
| oauth.rs | 603 | OAuth PKCE 흐름 |
| lsp_client.rs | 747 | LSP 클라이언트 |
| remote.rs | 401 | 원격 세션 (proxy, token) |
| task_registry.rs | 503 | Task 할당/상태 추적 |
| team_cron_registry.rs | 509 | 반복 작업 스케줄링 |
| lane_events.rs | 383 | Lane 이벤트 (커밋 provenance) |
| trust_resolver.rs | 299 | 신뢰도 해결 |
| green_contract.rs | 152 | Green contract |
| task_packet.rs | 158 | Task 패킷 validation |
| json.rs | 358 | JSON 유틸리티 |
| sse.rs | 158 | SSE 파서 |
| lib.rs | 179 | 모듈 선언 |

### 3.2 api 크레이트 (10개 파일, 7,164줄)

| 파일 | 줄 수 | 역할 |
|------|-------|------|
| providers/anthropic.rs | 1,770 | Anthropic API — OAuth, retry (exponential backoff 1s~128s) |
| providers/openai_compat.rs | 1,798 | OpenAI/Xai/DashScope 호환 클라이언트 |
| providers/mod.rs | 1,025 | Provider 감지 (모델명 기반), alias 해석 |
| prompt_cache.rs | 736 | FNV1a 해싱, 파일 기반 TTL 캐시, cache break 감지 |
| error.rs | 573 | 에러 분류 (retryable/auth/context_window), 재시도 판단 |
| http_client.rs | 345 | HTTP 클라이언트 빌드, proxy 설정 |
| sse.rs | 331 | SSE 스트림 파싱 (청크 기반 버퍼링) |
| types.rs | 311 | MessageRequest/Response, StreamEvent, Usage |
| client.rs | 242 | ProviderClient enum (Anthropic/Xai/OpenAi) |
| lib.rs | 40 | 모듈 재구성 |

**핵심 설계:**
- **ProviderClient enum** — 모델명으로 자동 라우팅 (claude-* → Anthropic, grok-* → Xai, gpt-* → OpenAI)
- **재시도 전략** — exponential backoff with jitter, retryable 에러만 재시도
- **Prompt Cache** — 요청 해시 → 파일 저장 → TTL 만료 관리

### 3.3 rusty-claude-cli 크레이트 (4개 파일, ~13,706줄)

| 파일 | 역할 |
|------|------|
| main.rs (~1,000줄) | CLI 진입점, CliAction enum (20+ 액션), REPL 루프 |
| render.rs (~1,500줄) | Markdown 렌더링 (pulldown-cmark), 코드 하이라이팅 (syntect), 스피너 |
| input.rs (~500줄) | rustyline 기반 라인 에디터, 슬래시 명령 자동완성 |
| init.rs (~300줄) | 레포 초기화 (.claw/, CLAUDE.md), 스택 감지 (언어/프레임워크/테스트) |

### 3.4 나머지 크레이트

| 크레이트 | 줄 수 | 핵심 |
|----------|-------|------|
| commands | 5,549 | `SLASH_COMMAND_SPECS` 50+ 명령 (help, status, model, compact, cost, mcp, skill, commit, pr...) |
| plugins | 4,020 | PluginManifest, PluginKind (Builtin/Bundled/External), HookRunner (Pre/Post ToolUse) |
| tools | 9,182 | GlobalToolRegistry, mvp_tool_specs(), lane_completion 감지, PDF 추출 |
| telemetry | 526 | TelemetryEvent enum, TelemetrySink trait, SessionTracer |
| compat-harness | 357 | TypeScript 원본에서 명령/도구/부트스트랩 매니페스트 추출 |

---

## 4. 우리가 가져올 것

### 4.1 반드시 가져올 것 (MUST)

| 컴포넌트 | 원본 파일 | 가져올 패턴 | 이유 |
|----------|-----------|-------------|------|
| **대화 루프** | conversation.rs | `ConversationRuntime<C,T>` 제네릭 구조, run_turn() 루프 | 에이전트 핵심 실행 엔진 |
| **세션 관리** | session.rs, session_control.rs | Session 구조체, JSONL 저장, fork 추적, 로테이션 | 대화 지속성의 근간 |
| **세션 압축** | compact.rs, summary_compression.rs | auto-compaction 알고리즘, 토큰 추정, continuation message | 긴 대화 필수 |
| **권한 시스템** | permissions.rs, permission_enforcer.rs | PermissionMode 3단계, Allow/Deny/Ask 규칙 | 보안 핵심 |
| **Bash 검증** | bash_validation.rs | CommandIntent 분류, 모드별 검증 파이프라인 | 도구 실행 안전성 |
| **Hook 시스템** | hooks.rs | Pre/PostToolUse hook, permission override | 확장성 핵심 |
| **MCP 클라이언트** | mcp_stdio.rs, mcp_client.rs, mcp_tool_bridge.rs | JSON-RPC 2.0 transport, tool discovery, tool invocation | 외부 도구 통합 필수 |
| **설정 시스템** | config.rs | 다중 소스 병합 (글로벌→프로젝트→로컬) | 멀티테넌트 설정 관리 |
| **복구 레시피** | recovery_recipes.rs | 6개 시나리오, max_attempts, escalation 정책 | 자동 복구 필수 |
| **에러 처리** | api/error.rs | ApiError 분류, retryable 판단, safe_failure_class() | 견고한 에러 핸들링 |
| **멀티 프로바이더** | api/providers/ | ProviderClient enum, 모델명 기반 라우팅, alias 해석 | 다중 LLM 지원 |

### 4.2 선택적으로 가져올 것 (SHOULD)

| 컴포넌트 | 원본 파일 | 이유 |
|----------|-----------|------|
| **정책 엔진** | policy_engine.rs | 선언적 규칙 시스템 — 거버넌스 레이어에 활용 |
| **Worker 상태 머신** | worker_boot.rs | 멀티 에이전트 관리 시 필요 |
| **SSE 스트림 파싱** | api/sse.rs | 실시간 응답 처리 |
| **Prompt Cache** | api/prompt_cache.rs | FNV1a 해싱 + TTL — 비용 절감 |
| **Git 컨텍스트** | git_context.rs | 프롬프트에 git 상태 자동 포함 |
| **Stale branch 검사** | stale_base.rs, stale_branch.rs | 코드 관리 자동화 |
| **Task Registry** | task_registry.rs | 태스크 할당/추적 — Paperclip 거버넌스와 연계 |
| **Plugin 라이프사이클** | plugin_lifecycle.rs | healthcheck, degraded mode |

### 4.3 구체적 함수/알고리즘

| 함수/패턴 | 위치 | 설명 |
|-----------|------|------|
| `run_turn()` | conversation.rs | 턴 실행 루프 — API 호출 → 도구 실행 → 반복 |
| `should_compact()` | compact.rs | 압축 필요 여부 판정 (토큰 추정 + 최근 N개 보존) |
| `compact_session()` | compact.rs | Tool-use/result 쌍 경계 보존하면서 압축 |
| `validate_read_only()` | bash_validation.rs | 8단계 명령어 검증 파이프라인 |
| `authorize()` | permissions.rs | 3단계 권한 평가 (모드→요구사항→규칙) |
| `initialize()` | mcp_stdio.rs | MCP 서버 초기화 + tool discovery |
| `detect_provider_kind()` | api/providers/mod.rs | 모델명 → 프로바이더 자동 매핑 |
| `exponential_backoff()` | api/providers/anthropic.rs | 재시도 (1s~128s, jitter) |
| `compress_summary()` | summary_compression.rs | priority 기반 라인 선택, budget 제한 |
| `execute_recovery()` | recovery_recipes.rs | 시나리오별 복구 단계 실행 |

---

## 5. 우리가 안 가져올 것

| 컴포넌트 | 파일 | 이유 |
|----------|------|------|
| **CLI REPL** | rusty-claude-cli/main.rs | 우리는 React 웹/Tauri 데스크탑 UI 사용 — CLI 불필요 |
| **Markdown 렌더링** | rusty-claude-cli/render.rs | 터미널 전용 — 웹 UI에서 별도 렌더링 |
| **rustyline 입력** | rusty-claude-cli/input.rs | 터미널 전용 입력 시스템 |
| **스택 감지 초기화** | rusty-claude-cli/init.rs | 우리 프로젝트 초기화 방식이 다름 |
| **compat-harness** | compat-harness/ | TypeScript 원본과의 호환성 유지용 — 우리는 Go 신규 구현 |
| **PDF 추출** | tools/pdf_extract.rs | 7,000줄 — 별도 라이브러리로 대체 가능 |
| **Lane Completion** | tools/lane_completion.rs | claw-code 특화 기능 — 우리 워크플로우와 무관 |
| **mock-anthropic-service** | 테스트 전용 | 우리 자체 테스트 인프라 구축 |
| **OAuth PKCE** | runtime/oauth.rs | Anthropic 전용 OAuth — 우리는 자체 인증 시스템 |
| **green_contract.rs** | runtime/ | claw-code 내부 CI/CD 전용 |
| **team_cron_registry.rs** | runtime/ | Paperclip 크레이트에서 더 나은 구현 가져올 것 |

---

## 6. Go 포팅 난이도

### 6.1 모듈별 평가

| 모듈 | 난이도 | 근거 |
|------|--------|------|
| conversation.rs | **MED** | 제네릭 `<C,T>`는 Go 인터페이스로 직역 가능. run_turn() 루프는 단순. 단 async stream 처리가 Go channel 패턴으로 변환 필요 |
| session.rs | **LOW** | JSON 직렬화/파일 I/O — Go 표준 라이브러리로 충분 |
| compact.rs | **LOW** | 알고리즘 단순 (토큰 추정 → 요약 → 교체). LLM 호출 부분만 연결하면 됨 |
| bash_validation.rs | **LOW** | 문자열 패턴 매칭 + 분류 — 언어 무관한 로직 |
| permissions.rs | **LOW** | enum + 규칙 평가 — Go에서 자연스러움 |
| policy_engine.rs | **MED** | 선언적 규칙 엔진 — 조건 결합(AND/OR)과 체인 액션 구현 필요 |
| mcp_stdio.rs | **HIGH** | 2,928줄, 프로세스 spawn + JSON-RPC + 비동기 I/O. Go의 exec.Cmd + goroutine으로 가능하나 에러 핸들링 복잡 |
| hooks.rs | **MED** | subprocess 실행 + JSON stdin/stdout — Go exec.Cmd로 가능. permission override 로직 주의 |
| config.rs | **MED** | 2,111줄, 다중 소스 병합 — Go의 map merge로 가능하나 MCP 설정 타입이 많음 |
| api/providers/ | **MED** | HTTP 클라이언트 + SSE 파싱 — Go net/http로 가능. 재시도 로직과 에러 분류 세심하게 포팅 필요 |
| recovery_recipes.rs | **LOW** | 단순한 상태 머신 — Go switch-case로 직역 |
| worker_boot.rs | **MED** | 상태 머신 + 신뢰 게이트 — goroutine + channel 패턴 활용 |
| sandbox.rs | **LOW** | 파일시스템 체크 + 환경 감지 — OS 호출만 |
| file_ops.rs | **LOW** | 파일 읽기/쓰기/편집 — Go 표준 I/O |

### 6.2 종합 평가

| 구분 | 비율 | 설명 |
|------|------|------|
| **LOW** | 45% | 직역 수준 — 1:1 대응 가능 |
| **MED** | 40% | 설계 변환 필요 — Rust 제네릭/트레이트 → Go 인터페이스, async → goroutine |
| **HIGH** | 15% | 재설계 필요 — MCP stdio (프로세스 관리 + 비동기 RPC) |

**종합 난이도: MED**

핵심 이유:
- Rust의 `trait` → Go의 `interface`로 자연스럽게 매핑
- `async/await` → `goroutine + channel`로 변환 가능
- `enum + match` → Go의 `type switch` 또는 상수 + switch
- 소유권/수명 → Go에서 불필요 (GC 있음) → 오히려 단순해짐
- **주의점**: Rust의 `Result<T,E>` 에러 체인 → Go의 `error` wrapping (`fmt.Errorf("%w")`)으로 정보 손실 주의

---

## 7. 다른 플랫폼과의 접점

### 7.1 레이어별 접점 매핑

```
claw-code가 제공하는 것        →  통합 시 만나는 플랫폼
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

대화 루프 (conversation.rs)   →  Hermes (자기 학습 루프가 여기에 붙음)
                               →  AutoAgent (자동 최적화가 턴 루프에 개입)

세션 관리 (session.rs)        →  Hermes (영속 메모리가 세션과 연동)
                               →  GoClaw (RLS 멀티테넌트 세션 저장소)

권한 시스템 (permissions.rs)  →  GoClaw (5계층 보안과 통합)
                               →  Paperclip (거버넌스 정책이 권한 규칙에 반영)

정책 엔진 (policy_engine.rs)  →  Paperclip (Atomic Checkout, 하트비트가 정책으로)
                               →  GoClaw (보안 정책 통합)

MCP 통합 (mcp_*.rs)           →  Goose (MCP 네이티브 — 도구 확장의 핵심)
                               →  OpenClaw (MCP를 통한 외부 채널 연결)

Hook 시스템 (hooks.rs)        →  Paperclip (거버넌스 훅)
                               →  AutoAgent (자동 개선 훅)

복구 시스템 (recovery.rs)     →  AutoAgent (실패에서 학습 → 복구 레시피 자동 생성)
                               →  Hermes (복구 패턴 기억)

설정 시스템 (config.rs)       →  GoClaw (멀티테넌트 설정 격리)
                               →  Paperclip (조직별 설정 관리)

API 클라이언트 (api/)         →  GoClaw (Go 네이티브 HTTP 클라이언트로 교체)

도구 실행 (file_ops, bash)    →  Goose (MCP 도구로 확장)
                               →  OpenClaw (채널별 도구 권한 차별화)
```

### 7.2 통합 우선순위

| 순서 | 접점 | 이유 |
|------|------|------|
| 1 | claw-code ↔ GoClaw | 코어 엔진을 Go로 포팅하면서 GoClaw의 성능/보안 패턴 직접 적용 |
| 2 | claw-code ↔ Hermes | 대화 루프에 자기 학습/메모리 레이어 추가 |
| 3 | claw-code ↔ Goose | MCP 통합 강화 — Goose의 네이티브 MCP 패턴 활용 |
| 4 | claw-code ↔ Paperclip | 거버넌스/조직 레이어 연결 |
| 5 | claw-code ↔ OpenClaw | 멀티채널 게이트웨이 연결 |
| 6 | claw-code ↔ AutoAgent | 자동 최적화 레이어 (마지막 — 기본 엔진 안정화 후) |

---

## 부록 A: 주요 구조체/트레이트 정리

### Traits (Go interface로 변환 대상)

```rust
// conversation.rs
trait ApiClient {
    fn stream(&mut self, request: ApiRequest) -> Result<Vec<AssistantEvent>, RuntimeError>;
}

trait ToolExecutor {
    fn execute(&mut self, tool_name: &str, input: &str) -> Result<String, ToolError>;
}

trait PermissionPrompter {
    fn decide(&mut self, request: &PermissionRequest) -> PermissionPromptDecision;
}

// telemetry/lib.rs
trait TelemetrySink: Send + Sync {
    fn record(&self, event: TelemetryEvent);
}

// mcp_client.rs
trait McpClientTransport {
    // MCP 서버와의 통신 추상화
}
```

### 핵심 Enums (Go const/type으로 변환 대상)

```rust
// session.rs
enum MessageRole { System, User, Assistant, Tool }
enum ContentBlock { Text, ToolUse, ToolResult }

// permissions.rs  
enum PermissionMode { ReadOnly, WorkspaceWrite, DangerFullAccess, Prompt, Allow }

// bash_validation.rs
enum CommandIntent { ReadOnly, Write, Destructive, Network, ProcessManagement, ... }
enum ValidationResult { Allow, Block { reason }, Warn { message } }

// api/client.rs
enum ProviderClient { Anthropic, Xai, OpenAi }

// recovery_recipes.rs
enum FailureScenario { TrustPromptUnresolved, PromptMisdelivery, StaleBranch, ... }
enum RecoveryStep { AcceptTrustPrompt, RebaseBranch, CleanBuild, RestartWorker, ... }
```

---

## 부록 B: 테스트 현황

| 크레이트 | 테스트 줄 수 | 테스트 종류 |
|----------|-------------|-------------|
| api | 928 | HTTP 요청/응답, SSE 파싱, Proxy, OAuth, 캐시 토큰 |
| rusty-claude-cli | 2,324 | JSON 출력 계약, 플래그 파싱, 콤팩트 모드, TypeScript 패리티 (883줄) |
| runtime | 통합 테스트 | integration_tests.rs — 대화 루프 E2E |

---

## 부록 C: 파일 크기 분포

```
2,000줄 이상 (4개): mcp_stdio(2928), config(2111), conversation(1699→반올림), providers/anthropic(1770)
1,000~2,000줄 (6개): session(1515), worker_boot(1180), bash_validation(1004), providers/openai_compat(1798), providers/mod(1025), hooks(987)
500~1,000줄 (12개): prompt(905), config_validate(901), session_control(873), mcp_lifecycle(843), file_ops(839), compact(827), lsp_client(747), prompt_cache(736), permissions(683), recovery(631), oauth(603), policy_engine(581)
500줄 이하 (20개): 나머지 모듈들
```
