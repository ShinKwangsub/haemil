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
  - **C5 세션 압축** 🔜 다음 사이클 후보
  - C6~C16 — 대기

**다음 세션 시작 시 읽을 것**:
1. `CLAUDE.md` (이 파일) — 전체 맥락
2. `analysis/integration/skeleton.md` — Phase 2 뼈대 스펙 (여전히 유효)
3. `graphify-out/GRAPH_REPORT.md` — 프로젝트 god nodes + 커뮤니티
4. `git log --oneline -10` — 최근 사이클 요약

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
- 슬래시 명령: `/exit`, `/help`

## 검증 상태
- `go build ./...` / `go vet ./...` / `go test ./...` — **94 테스트 PASS / 0 FAIL** (C3 에서 +12)
- E2E C1~C4 완료: oMLX/gemma4 + 6개 도구 전부 실호출 + 권한 모드 + bash 검증
- E2E C3 완료 (2026-04-22): danger-full + `mkdir x && rm -rf x` → `[warning] Recursive forced deletion...` prefix + 실행 성공. `cat ../../etc/hosts` → traversal warn
- 커밋: `d28d98b` (C2), `c0dea5d` (C1 file_ops), `8cff014` (OpenAI provider), `7190178` (Phase 2b), `5eec0dd` (docs), `cb7fb66` (Phase 2a), `120f67e` (Graphify), `a1e42d4` (initial)

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
  - `session.go` — JSONL append-only 세션 저장 + replay (bufio.Scanner, 손상 줄 skip)
  - `conversation.go` — Runtime, Options, TurnSummary, RunTurn (Policy 게이트 내장)
  - `permissions.go` — Capability / PermissionMode / Policy / Authorize (C2)
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
