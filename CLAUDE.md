# 해밀 (Haemil) — AI 비즈니스 파트너 플랫폼

## 프로젝트 개요
7개 오픈소스 AI 에이전트 플랫폼(claw-code, Hermes, OpenClaw, Paperclip, GoClaw, Goose, AutoAgent)의
핵심 장점을 통합하여 상용 AI 비즈니스 파트너 제품을 만드는 프로젝트.

## 현재 단계
- **Phase 1**: 각 플랫폼 상세 소스 분석 ✅ 완료 (2026-04-10)
- **Phase 2**: 통합 엔진 뼈대 ✅ 완료 (2026-04-22)
  - **Phase 2a**: Go 스텁 (컴파일 + 테스트 + 설계 문서) ✅ 완료 (2026-04-11)
  - **Phase 2b**: 본문 구현 (SSE 파싱, bash 실행, 턴 루프, REPL) ✅ 완료 (2026-04-22)
- **Graphify 통합** ✅ 완료 (2026-04-22)
- **Phase 3 진행 중** — 컴포넌트 추출 (7개 플랫폼, 사이클 C1~C16)
  - **C1 file_ops** ✅ 완료 (2026-04-22) — read/write/edit/glob/grep 5개 도구
  - **C4 멀티 프로바이더** ✅ 완료 (2026-04-22, 앞당김) — OpenAI-compat (oMLX/OpenAI/로컬)
  - **C2 권한 모드** ✅ 완료 (2026-04-22) — readonly / workspace-write / danger-full (기본)
  - **C3 bash 검증** ✅ 완료 (2026-04-22) — CommandIntent 분류 + 4단계 파이프라인 (Mode/Sed/Destructive/Paths)
  - **C5 세션 압축** ✅ 완료 (2026-04-22) — 템플릿 기반 요약 (LLM 호출 없음), tool 쌍 경계 보호, `/compact` 명령, JSONL compaction marker
  - **C7 MCP 클라이언트** ✅ 완료 (2026-04-22) — stdio JSON-RPC 2.0, initialize/tools/list/tools/call, `~/.haemil/mcp.json` 설정
  - **C8 메모리** ✅ 완료 (2026-04-22) — USER.md (~/.haemil/) + 프로젝트 MEMORY.md (.haemil/), `/memory` 조회, `/remember` 추가, 시작 시 시스템 프롬프트 주입
  - **C6 Hook** ✅ 완료 (2026-04-22) — Pre/PostToolUse subprocess hook, `.haemil/hooks.json` 설정, deny/modify/append 플로우
  - **Phase 3 완료** ✅ (2026-04-22) — C1~C8 전부 완료. C9~C16 은 Phase 4 로 이동 (UI / 멀티테넌트 / 에이전트간 통신)
- **Phase 4 진행 중** — UI / 멀티테넌트 / 에이전트간 통신
  - **C9 멀티테넌트 컨텍스트** ✅ 완료 (2026-04-22) — `runtime.TenantContext` (Workspace + HomeDir) 도입, `memory`/`hooks`/`mcp` 의 `Default*Path` 를 tenant 헬퍼로 centralize, `cli.Config` 에 `Workspace`/`HomeDir`/`TenantID` 필드 추가. 두 tenant 가 동일 프로세스에서 서로 섞이지 않음을 `TestTwoTenantsDoNotCrosstalk` 로 보장.
  - **C10 Runtime Supervisor** ✅ 완료 (2026-04-22) — `runtime.Supervisor` 가 N 개 tenant Runtime 을 tenant-당-goroutine 으로 오케스트레이트. 크로스-tenant 병렬, 인트라-tenant 직렬. `quit` signal-only 셧다운 (senders 와 close 레이스 없음). `TestSupervisorTwoTenantsNoCrosstalkRace` + `TestSupervisorIntraTenantSerialization` 가 `-race -count=5` 에서 clean. C12 이벤트 버스 / C16 serve 모드의 전제 dispatch 레이어.
  - **C12 DomainEventBus** ✅ 완료 (2026-04-22) — `runtime.EventBus` 인메모리 pub/sub (fire-and-forget, per-subscriber FIFO, slow-consumer drop). Supervisor 가 `RegisterOpts.EventBus` 받으면 각 RunTurn 완료마다 `turn.completed` 이벤트 자동 발행. Filter 는 subscriber 쪽, panic-safe. 에이전트 A 완료를 B 가 구독으로 관찰 가능 → multi-agent 협업의 얇은 뼈대.
  - **7개 reference 플랫폼 Knowledge Graph** ✅ 완료 (2026-04-22) — 각 플랫폼 핵심 서브디렉터리에 대해 graphify 실행. AST + semantic extraction 으로 god-node / community / edges 네비게이션 가능. 산출물: `reference/<platform>/graphify-out/graph.html` (브라우저 열기) + `graph.json` (쿼리) + `GRAPH_REPORT.md`. 사이클 구현 중 "이 플랫폼의 X 패턴 어디 있지?" 에 즉시 답 가능.

**다음 세션 시작 시 읽을 것**:
1. `CLAUDE.md` (이 파일) — 전체 맥락
2. `analysis/integration/skeleton.md` — Phase 2 뼈대 스펙 (여전히 유효)
3. `graphify-out/GRAPH_REPORT.md` — 해밀 프로젝트 자체의 god nodes + 커뮤니티
4. 해당 사이클 포팅 대상 플랫폼의 `reference/<platform>/graphify-out/GRAPH_REPORT.md` (로컬 전용, gitignored)
5. `git log --oneline -10` — 최근 사이클 요약

## 현재 기능 (실제로 돌아감)
- `./haemil -provider omlx` — 로컬 oMLX (gemma-4) 로 대화 + 도구 사용
- `ANTHROPIC_API_KEY=... ./haemil` — Anthropic 클라이언트
- `OPENAI_API_KEY=... ./haemil -provider openai` — OpenAI
- 도구 6개: **bash**, **read_file**, **write_file**, **edit_file**, **glob_search**, **grep_search**
- 권한 모드 (C2): `-permission-mode readonly | workspace-write | danger-full` (기본 `danger-full`)
  - readonly → CapRead 만 (read/glob/grep)
  - workspace-write → CapRead+CapWrite (read/glob/grep/write/edit), bash 차단
  - danger-full → 전부 허용 (현재 동작)
- JSONL 세션 저장 `~/.haemil/sessions/<id>.jsonl` (0700 dir / 0600 file)
- `-session <id>` 플래그로 이전 세션 replay
- 슬래시 명령: `/exit`, `/help`, `/compact` (C5), `/memory` + `/remember [-user]` (C8)
- 멀티테넌트 (C9): `cli.Config.Workspace` + `cli.Config.HomeDir` 로 같은 프로세스에서 tenant 격리 가능 (CLI flag 는 Phase 4 후속)

## 검증 상태
- `go build ./...` / `go vet ./...` / `go test ./...` — **166 테스트 PASS / 0 FAIL** (C12 에서 +9), `go test -race -count=5` clean
- E2E C1~C5 완료: oMLX/gemma4 + 6개 도구 + 권한 모드 + bash 검증 + `/compact` 슬래시
- E2E C5 완료 (2026-04-22): REPL `/compact` → 임계값 아래일 때 "below threshold" skip 메시지. JSONL marker 라인 replay 는 `TestSessionApplyCompactionRoundtrip` 가 검증
- 커밋: `79d96fc` (C3), `d28d98b` (C2), `c0dea5d` (C1 file_ops), `8cff014` (OpenAI provider), `7190178` (Phase 2b), `5eec0dd` (docs), `cb7fb66` (Phase 2a), `120f67e` (Graphify), `a1e42d4` (initial)

## 기술 스택 (확정)
- 코어 엔진: Go
- 웹 UI: React
- 데스크탑: Tauri (Go + React)
- 모바일: React Native
- DB: PostgreSQL (RLS 멀티테넌트)
- 세션 저장: JSONL (append-only)

## 디렉토리 구조

### Go 코드 (Phase 2~3 C2 완료 기준)
- `cmd/haemil/main.go` — CLI 엔트리포인트, flag 파싱 (`-provider`, `-model`, `-endpoint`, `-session`, `-permission-mode`, ...)
- `internal/runtime/` — 도메인 타입 + Provider/Tool 인터페이스 (consumer defines interface)
  - `message.go` — Role, ContentBlock, Message, ChatRequest/Response, Provider, Tool
  - `session.go` — JSONL append-only + replay + compaction marker (ApplyCompaction)
  - `conversation.go` — Runtime, Options, TurnSummary, RunTurn (Policy 게이트 내장)
  - `permissions.go` — Capability / PermissionMode / Policy / Authorize (C2)
  - `compact.go` — CompactionConfig / ShouldCompact / Compact + 템플릿 요약 + 쌍 경계 보호 (C5)
  - `tenant.go` — TenantContext / ResolveTenant / 5개 경로 헬퍼 (C9) — `memory`/`hooks`/`mcp` 의 공통 경로 루트
  - `supervisor.go` — Supervisor / supervisedAgent, 테넌트별 goroutine + signal-only 셧다운 (C10) + `turn.completed` 이벤트 발행 (C12)
  - `eventbus.go` — EventBus / Event / Subscription / Filter, 인메모리 fire-and-forget pub/sub (C12)
- `internal/hooks/` — Pre/Post ToolUse 훅 (C6)
  - `hooks.go` — Config / Runner / HookSpec, subprocess stdin/stdout JSON 계약, deny/modify/append
- `internal/memory/` — 메모리 (C8)
  - `memory.go` — Store / Context, USER.md + 프로젝트 MEMORY.md, `<memory-context>` 렌더링
- `internal/mcp/` — MCP 클라이언트 (C7)
  - `protocol.go` — JSON-RPC 2.0 + MCP 메시지 타입 (Initialize/ListTools/CallTool)
  - `stdio_client.go` — subprocess 파이프 + newline-delimited JSON + read loop
  - `server.go` — Server lifecycle (Initialize → ListTools → CallTool → Close)
  - `tool.go` — runtime.Tool 어댑터, `mcp__<server>__<tool>` 네임스페이스
  - `registry.go` — LoadConfig / BootstrapFromConfig (degraded mode: 서버 하나 실패해도 나머지 유지)
- `internal/provider/` — LLM 백엔드 구현
  - `provider.go` — New(name, apiKey, model, Options) 팩토리 + RedactAPIKey
  - `anthropic.go` — Anthropic Messages API (Bearer `x-api-key`, 13 함정 준수)
  - `openai.go` — OpenAI-compat (Bearer auth, 로컬 서버는 apiKey="" 로 Authorization 생략)
- `internal/tools/` — 도구 구현 (6개 등록됨)
  - `tool.go` — Default(mode, workspace) 레지스트리
  - `bash.go` — BashTool(mode, workspace) + 좁은 BLOCKED_PATTERNS (literal 루트만) + 프로세스 그룹 kill
  - `bash_validation.go` — C3 검증 파이프라인 (Mode→Sed→Destructive→Paths), ClassifyCommand, 명령 분류 리스트
  - `fileutil.go` — 공용 (10MiB cap, binary 감지, 경로 해석)
  - `read_file.go`, `write_file.go`, `edit_file.go` — 파일 R/W/편집
  - `glob_search.go` — `**` 재귀 매칭, noise dir 자동 제외
  - `grep_search.go` — RE2 정규식 + include 필터 + context 라인
- `internal/cli/` — REPL 조립 + 입력 루프
  - `repl.go` — Run(ctx, cfg), isSlashCommand 게이트 (`/tmp/foo` 같은 경로는 메시지로 통과)

**임포트 그래프**: `main → cli → runtime/provider/tools`. provider, tools 는 둘 다 runtime 을 쓰지만 **서로는 모른다**.

### 분석/설계 문서
- `analysis/platforms/` — 7개 플랫폼 상세 분석 (동일 템플릿, Phase 1)
  - claw-code.md (451줄), goclaw.md (323줄), hermes.md (282줄)
  - goose.md (298줄), paperclip.md (319줄), openclaw.md (351줄), autoagent.md (256줄)
- `analysis/integration/` — 통합 설계 문서
  - `skeleton.md` — Phase 2 코어 엔진 뼈대 설계서 (다음 세션 입력)
  - `multi-agent-communication.md` — Phase 3 멀티 에이전트 통신 (3계층: 태스크/이벤트/Advisor)
- `analysis/` — 기존 패턴 분석 (하네스, 압축, 복구)
- `plan/` — 설계서 + 구현 로드맵
- `reference/` — 7개 플랫폼 소스 전부 클론됨 (`.gitignore` 처리, ~1.6GB 로컬 전용)
  - claw-code/ (78 Rust 파일, 75K줄), goclaw/ (1,232 Go 파일)
  - hermes/ (777 Python 파일), goose/ (562 Rust 파일)
  - paperclip/ (842 TS 파일), openclaw/ (10,894 TS 파일)
  - autoagent/ (101 Python 파일)
  - **각 플랫폼 지식 그래프**: `reference/<platform>/graphify-out/graph.html` (브라우저) + `graph.json` (쿼리) + `GRAPH_REPORT.md` (요약). AST 기반이라 코드는 거의 무료, docs 만 semantic 처리. 스코프:
    - claw-code: 전체 (4,102 nodes / 66 communities) — Rust 엔진 뼈대
    - autoagent: `autoagent/` 코어 (864 nodes / 27 communities)
    - paperclip: `server/src/` (1,525 nodes / 64 communities) — **Atomic Checkout / Heartbeat** 검색 가능
    - hermes: `agent/` 코어 (876 nodes / 30 communities) — `MemoryProvider`, `InsightsEngine` 중심
    - goose: `crates/goose/` (4,295 nodes / 77 communities) — `Agent` / `ExtensionManager` 중심
    - goclaw: `internal/store/` (2,020 nodes / 86 communities) — **`TenantIDFromContext` god-node** (C9+C11 레퍼런스 핵심)
    - openclaw: `src/channels/` (902 nodes / 93 communities) — ChannelPlugin 계약

## 분석 템플릿 (각 플랫폼 공통)
1. 개요 (한줄 설명, 언어, 라이선스, Stars)
2. 아키텍처 (핵심 모듈 구조, 데이터 흐름)
3. 핵심 소스 분석 (주요 파일, 각 모듈 역할/패턴)
4. 우리가 가져올 것 (구체적 함수/패턴/알고리즘)
5. 우리가 안 가져올 것 (불필요한 부분 + 이유)
6. Go 포팅 난이도 (LOW/MED/HIGH + 근거)
7. 다른 플랫폼과의 접점 (어떤 레이어와 만나는지)

## 7개 소스 플랫폼 역할
- claw-code → 엔진의 뼈대 (실행 루프, 보안, 데이터)
- Hermes → 두뇌 (학습, 기억)
- Paperclip → 관리자 (조직, 거버넌스, 예산)
- OpenClaw → 입과 귀 (24+ 채널)
- GoClaw → 근육 (성능, 보안, 멀티테넌트)
- Goose → 손 (MCP로 외부 도구 연결)
- AutoAgent → 진화 (에이전트가 스스로 개선)

## Phase 1 분석 결과 요약

### 각 플랫폼 Go 포팅 난이도
| 플랫폼 | 난이도 | 핵심 이유 |
|--------|--------|-----------|
| GoClaw | LOW | 이미 Go — 직접 차용 |
| claw-code | MED | Rust trait→Go interface, async→goroutine |
| Hermes | LOW-MED | 학습 패턴 단순, 외부 플러그인 연동 시 증가 |
| Goose | MED | MCP 라이브러리 선택 + in-process 서빙 |
| Paperclip | LOW-MED | 거버넌스 패턴 단순, Heartbeat 통합이 관건 |
| OpenClaw | LOW-MED | 핵심 패턴 단순, 채널 SDK별 학습 필요 |
| AutoAgent | MED | 동적 에이전트 생성을 Go 방식으로 재설계 필요 |

### 통합 우선순위 (접점 기반)
1. GoClaw ↔ claw-code — 코어 엔진 (파이프라인 + 권한 + 프로바이더)
2. Hermes ↔ claw-code — 학습 레이어 (대화 루프에 학습 트리거)
3. Goose ↔ GoClaw — MCP 통합 (세션 컨텍스트 + 도구 네임스페이스)
4. Paperclip ↔ GoClaw — 거버넌스 (조직/태스크/예산 + 스케줄러)
5. OpenClaw ↔ GoClaw — 멀티채널 (게이트웨이 + WebSocket)
6. AutoAgent ↔ Hermes — 진화 (실패 → 학습 → 최적화)

## 관련 프로젝트
- `~/openclaw-harness-project/` — 하네스 개선 프로젝트 (별도 진행)

## 사용자 정보
- 이름: 광섭 (디스코드: 암제)
- 스타일: 반말, 실행 중심
- 목표: AI 비즈니스 파트너 서비스 사업
