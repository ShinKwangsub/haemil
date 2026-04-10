# GoClaw 상세 분석

> 분석일: 2026-04-10
> 분석 범위: Go 전체 (1,232개 .go 파일, 44개 internal 패키지)

---

## 1. 개요

| 항목 | 내용 |
|------|------|
| 한줄 설명 | PostgreSQL 기반 멀티테넌트 AI 에이전트 게이트웨이 — 단일 Go 바이너리 (~25MB) |
| 언어 | Go 1.26 |
| 라이선스 | MIT |
| 역할 | 근육 — Go 고성능, 5계층 보안, RLS 멀티테넌트 |
| 규모 | 1,232 Go 파일, 44개 internal 패키지, 13,302줄 cmd 코드 |
| 지원 채널 | Telegram, Discord, Slack, Zalo, Feishu/Lark, WhatsApp, WebSocket |
| LLM 제공자 | 20+ (Anthropic, OpenAI, OpenRouter, Groq, DeepSeek, Gemini, MiniMax, DashScope 등) |

---

## 2. 아키텍처

### 2.1 프로젝트 구조

```
cmd/                          CLI, 게이트웨이, 마이그레이션, 온보딩
internal/
  ├─ agent/       (83파일)    Loop, Router, Input Guard, Intent Classify
  ├─ pipeline/    (17파일)    8단계 파이프라인 (context→finalize)
  ├─ store/       (50파일)    멀티DB 저장소 (PG + SQLite)
  │  ├─ pg/                   PostgreSQL 구현
  │  ├─ sqlitestore/          SQLite 구현 (Lite 에디션)
  │  └─ base/                 Dialect 패턴 (DB 추상화)
  ├─ gateway/     (7파일)     WebSocket 서버, RPC 라우터
  ├─ providers/   (90파일)    20+ LLM 제공자 어댑터
  ├─ scheduler/   (6파일)     레인 기반 동시성 (main/subagent/cron)
  ├─ consolidation/ (15파일)  에피소드/의미론/드리밍 워커
  ├─ eventbus/    (5파일)     도메인 이벤트 버스
  ├─ tools/       (155파일)   30+ 도구, RBAC, MCP 브릿지
  ├─ channels/    (31파일)    채널 어댑터
  ├─ permissions/ (2파일)     5계층 권한 시스템
  ├─ memory/      (6파일)     임베딩, 자동 인젝터, 회상
  ├─ vault/       (18파일)    Knowledge Vault, wikilinks, 하이브리드 검색
  ├─ http/        (124파일)   REST API, OpenAI 호환
  ├─ mcp/         (16파일)    MCP 브릿지
  ├─ sandbox/     (5파일)     Docker 기반 코드 샌드박스
  ├─ cache/       (9파일)     다층 캐싱 (Redis, 인메모리)
  ├─ crypto/      (3파일)     AES-256-GCM 암호화
  └─ i18n/        (8파일)     메시지 카탈로그 (en/vi/zh)
pkg/protocol/                 Wire 타입 (프레임, 메서드, 에러)
ui/web/                       React 19 + Vite 6 + Tailwind CSS 4
ui/desktop/                   Wails v2 (SQLite, Lite 에디션)
```

### 2.2 8단계 에이전트 파이프라인

```
Pipeline {
  setup:     [ContextStage]     — 에이전트/사용자/워크스페이스 주입
  iteration: [Think → Prune → Tool → Observe → Checkpoint]  — 반복 루프
  finalize:  [FinalizeStage]    — 메모리 저장
}

RunState {
  Ctx, Iteration, History[]Message,
  ThinkContent, ToolCalls[], Observation[],
  FinalContent, ExitCode (BreakLoop|AbortRun)
}
```

특징: 플러그 가능한 콜백, 예산 초과 시 정상 종료, 병렬 도구 실행

### 2.3 멀티테넌트 컨텍스트

```go
store.WithUserID(ctx, "user_id")      // 외부 사용자 ID
store.WithAgentID(ctx, uuid.UUID)     // 에이전트 UUID
store.WithTenantID(ctx, uuid.UUID)    // 멀티테넌트 격리
store.WithLocale(ctx, "en"|"vi"|"zh") // i18n
store.WithRoleFromAPI(ctx, Role)      // RBAC
```

Context 값이 SQL WHERE 절에 자동 추가 → 앱 레벨 멀티테넌트 격리

### 2.4 3계층 메모리

```
L0: Working Memory (대화 이력) — 매 턴 자동 로드
L1: Episodic Memory (세션 요약) — DomainEventBus → EpisodicWorker
L2: Semantic Memory (KG + pgvector) — 의미론 워커 + 하이브리드 검색
```

---

## 3. 핵심 소스 분석

### 3.1 에이전트 라우터

```go
type Router struct {
    agents        map[string]*agentEntry       // 테넌트 스코프 캐시
    activeRuns    sync.Map                     // runID → *ActiveRun
    sessionRuns   sync.Map                     // sessionKey → runID
    resolver      ResolverFunc                 // 지연 생성 (DB → Agent)
    ttl           time.Duration                // 캐시 TTL
}
```

캐시 키: `tenant:agent-id`, TTL 기반 만료, Cold Start 시 리졸버로 DB 로드

### 3.2 레인 기반 스케줄러

```go
type Scheduler struct {
    lanes    *LaneManager      // 3 lanes: main/subagent/cron
    sessions map[string]*SessionQueue
    draining atomic.Bool       // 우아한 종료
}

type LaneConfig struct {
    Name          string   // "main" | "subagent" | "cron"
    MaxConcurrent int      // 동시 실행 최대값
    Weight        int      // 상대적 대역폭
}
```

에이전트별 세션 큐 (FIFO + 우선순위), 적응형 스로틀, 우아한 종료

### 3.3 5계층 보안

```
Layer 1: Gateway Auth — Token scopes (admin/operator.read/write/approvals)
Layer 2: Global Tool Policy — tools.allow[], tools.deny[], Rate Limiting
Layer 3: Per-Agent Tool Policy — agents[].tools.allow/deny, Tool Prefix
Layer 4: Per-Channel/Group Policy — channels.*.groups.*.tools.policy
Layer 5: Owner-Only Tools — senderIsOwner 체크
```

### 3.4 Input Guard (프롬프트 주입 감지)

```go
type InputGuard struct {
    patterns []guardPattern  // 6가지 정규식
}
// ignore_instructions, role_override, system_tags,
// instruction_injection, null_bytes, delimiter_escape

// 설정: gateway.injection_action = "log"|"warn"|"block"|"off"
```

### 3.5 암호화

```go
// AES-256-GCM
Encrypt(plaintext, key) → "aes-gcm:" + base64(nonce + ciphertext + tag)
// API 키, 프로바이더 토큰 저장에 사용
```

### 3.6 저장소 계층

```go
type Stores struct {
    Sessions, Memory, Episodic, KnowledgeGraph,
    Agents, Providers, Tools, Teams, Activity, ...  // 20+
}
```

Dialect 인터페이스로 PG(`$1,$2`) vs SQLite(`?`) 추상화

### 3.7 프로바이더 어댑터

```go
type ProviderAdapter interface {
    Name() string
    Capabilities() ProviderCapabilities
    ToRequest(req ChatRequest) ([]byte, http.Header, error)
    FromResponse(data []byte) (*ChatResponse, error)
    FromStreamChunk(data []byte) (*StreamChunk, error)
}
// 20+ 제공자 모두 동일 인터페이스
```

### 3.8 WebSocket 프로토콜

```go
// 3가지 프레임 (ProtocolVersion = 3)
RequestFrame  { Type:"req",   ID, Method, Params }
ResponseFrame { Type:"res",   ID, OK, Payload, Error }
EventFrame    { Type:"event", Event, Payload, Seq, StateVersion }
```

### 3.9 도메인 이벤트 버스

```go
type DomainEventBus interface {
    Publish(event DomainEvent)
    Subscribe(eventType, handler) func()
    Start(ctx context.Context)
    Drain(timeout) error
}
// QueueSize=1000, WorkerCount=2, RetryAttempts=3, Dedup 5분 TTL
```

### 3.10 Self-Evolution

```go
type EvolutionGuardrail struct {
    AllowedChanges map[string]bool  // CAPABILITIES.md만 변경
    ProtectedFiles map[string]bool  // IDENTITY.md, SOUL.md 보호
}
// 메트릭 수집 → 제안 분석 → 가드레일 보호된 적용 + 롤백
```

---

## 4. 우리가 가져올 것

### 4.1 반드시 가져올 것 (MUST)

| 컴포넌트 | 가져올 패턴 | 이유 |
|----------|-------------|------|
| **멀티테넌트 컨텍스트** | store.WithTenantID() → SQL WHERE 자동 추가 | 멀티테넌트 핵심 |
| **8단계 파이프라인** | Pipeline{setup, iteration[], finalize} + RunState | 에이전트 실행 엔진 |
| **5계층 보안** | Gateway→Global→Agent→Channel→Owner 계층 | 보안 핵심 |
| **저장소 추상화** | Dialect 인터페이스 + Stores 컨테이너 | DB 교체 가능성 |
| **레인 스케줄러** | LaneManager + SessionQueue + 적응형 스로틀 | 동시성 관리 |
| **프로바이더 어댑터** | ProviderAdapter interface + 20+ 구현 | LLM 통합 |
| **WebSocket 프로토콜** | req/res/event 3프레임 + StateVersion | 실시간 통신 |
| **도메인 이벤트 버스** | Publish/Subscribe + 워커 풀 + 재시도 | 비동기 처리 |
| **Input Guard** | 6가지 주입 패턴 정규식 | 프롬프트 보안 |
| **AES-256-GCM 암호화** | crypto 패키지 | API 키 보호 |

### 4.2 선택적으로 가져올 것 (SHOULD)

| 컴포넌트 | 이유 |
|----------|------|
| **Knowledge Vault** | wikilinks + 하이브리드 검색 (BM25 + pgvector) |
| **Self-Evolution** | 가드레일 보호된 자기 개선 |
| **Rate Limiter** | 토큰 버킷 기반 사용자별 속도 제한 |
| **i18n** | 다국어 지원 (en/vi/zh) |
| **Lite Edition** | SQLite 기반 데스크탑 배포 |

### 4.3 구체적 함수/패턴

| 패턴 | 설명 |
|------|------|
| `BuildScopeClause()` | 테넌트 WHERE 자동 추가 |
| `BuildMapUpdate()` | 동적 UPDATE 쿼리 생성 |
| `Router.resolver` | 지연 생성 — DB에서 에이전트 로드 |
| `Scheduler.MarkDraining()` | 우아한 종료 — 새 요청 거부, 활성 완료 대기 |
| `SessionCompletedEvent → EpisodicWorker` | 세션 완료 → 에피소드 메모리 자동 생성 |

---

## 5. 우리가 안 가져올 것

| 컴포넌트 | 이유 |
|----------|------|
| **React 웹 UI** | 우리 자체 React UI 구축 |
| **Wails 데스크탑** | 우리는 Tauri 사용 |
| **채널 어댑터 전체** | OpenClaw에서 더 완성된 채널 시스템 가져올 것 |
| **cmd/ 전체** | CLI 구조 재설계 |
| **Docker Compose 구성** | 우리 자체 배포 전략 |
| **Lite Edition 제한** | 5 agents, 1 team 제한 불필요 |

---

## 6. Go 포팅 난이도

**이미 Go이므로 포팅 아닌 직접 차용.**

| 모듈 | 난이도 | 근거 |
|------|--------|------|
| 멀티테넌트 컨텍스트 | **LOW** | context.Value 패턴 그대로 사용 |
| 8단계 파이프라인 | **LOW** | 인터페이스 기반 — 직접 사용 가능 |
| 저장소 추상화 | **LOW** | Dialect 패턴 그대로 |
| 스케줄러 | **MED** | 우리 요구사항에 맞게 레인 구성 조정 필요 |
| 프로바이더 어댑터 | **LOW** | 인터페이스 확정, 새 프로바이더만 추가 |
| 보안 5계층 | **MED** | Paperclip 거버넌스와 통합 시 조정 필요 |
| 이벤트 버스 | **LOW** | 범용 패턴 |
| MCP 브릿지 | **MED** | Goose의 MCP 패턴과 병합 필요 |

**종합: LOW** — Go 코드를 직접 가져다 쓸 수 있음. 핵심은 다른 플랫폼 패턴과의 통합 설계.

---

## 7. 다른 플랫폼과의 접점

```
GoClaw가 제공하는 것        →  통합 시 만나는 플랫폼
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

8단계 파이프라인            →  claw-code (ConversationRuntime 턴 루프 통합)
                             →  Hermes (학습 루프가 파이프라인 스테이지로)

멀티테넌트 저장소            →  Paperclip (조직/에이전트/태스크 DB)
                             →  Hermes (테넌트별 메모리 격리)

5계층 보안                  →  claw-code (bash_validation + permissions 통합)
                             →  Paperclip (거버넌스 정책이 보안 레이어로)

프로바이더 어댑터            →  claw-code (ProviderClient → Go ProviderAdapter)

레인 스케줄러               →  Paperclip (Heartbeat 스케줄링과 연계)

이벤트 버스                 →  Paperclip (승인/예산 이벤트)
                             →  AutoAgent (성능 메트릭 이벤트)

WebSocket 프로토콜          →  OpenClaw (게이트웨이 프로토콜과 통합)

MCP 브릿지                  →  Goose (MCP 네이티브 패턴 적용)

Knowledge Vault             →  Hermes (영속 메모리와 통합)
```

### 통합 우선순위

1. **GoClaw ↔ claw-code** — 파이프라인 + 권한 + 프로바이더 통합 (코어)
2. **GoClaw ↔ Paperclip** — 저장소 + 이벤트 버스 + 스케줄러 (관리)
3. **GoClaw ↔ Hermes** — 메모리 계층 통합 (학습)
4. **GoClaw ↔ Goose** — MCP 브릿지 강화 (도구)
5. **GoClaw ↔ OpenClaw** — 채널 + WebSocket 통합 (통신)
