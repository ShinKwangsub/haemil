# 다중 에이전트 통신 설계서

> 작성일: 2026-04-11
> 상태: Draft v1
> 참고: Paperclip (거버넌스), GoClaw (이벤트 버스), Anthropic Advisor Strategy

---

## 1. 문제 정의

Haemil은 여러 AI 에이전트가 협업하는 조직형 시스템이다. 에이전트 간 통신을 어떻게 설계하느냐에 따라:

- **느려서 못 씀** (태스크만 쓰면 DB 왕복 지옥)
- **통제 불능** (실시간만 쓰면 감사/거버넌스 없음)
- **비용 폭주** (모든 에이전트가 최상위 모델 쓰면 파산)

이 셋을 동시에 해결해야 한다.

---

## 2. 핵심 원칙: 3계층 분리

**"무엇을 저장"(What)과 "언제 알림"(When)과 "얼마나 똑똑하게"(How smart)를 분리한다.**

```
┌─────────────────────────────────────────────────────┐
│ Layer 1: 태스크 (What) — 영속, 감사, 원자적         │
│   Paperclip 패턴                                     │
│   용도: 거버넌스, 위임, 작업 기록                   │
│   저장: PostgreSQL                                   │
│   지연: 수십~수백ms (DB 왕복)                        │
└─────────────────────────────────────────────────────┘
┌─────────────────────────────────────────────────────┐
│ Layer 2: 이벤트 (When) — 실시간 신호, 휘발성         │
│   GoClaw DomainEventBus 패턴                         │
│   용도: 알림, 상태 변화, 브로드캐스트               │
│   저장: 메모리 큐 + 워커 풀                         │
│   지연: 수ms (in-process) / 수십ms (분산)           │
└─────────────────────────────────────────────────────┘
┌─────────────────────────────────────────────────────┐
│ Layer 3: Advisor (How smart) — 지능 부스트           │
│   Anthropic 내장 `advisor_20260301` 도구            │
│   용도: 막힌 문제 조언, 아키텍처 결정               │
│   저장: 단일 API 요청 내 (영속 아님)                │
│   지연: 단일 API 왕복 내 포함                       │
└─────────────────────────────────────────────────────┘
```

### 각 계층 선택 기준

| 상황 | 사용 계층 | 근거 |
|------|----------|------|
| 작업 위임 (CEO→CTO→Engineer) | Layer 1 (태스크) | 감사, 원자적 잠금 |
| 작업 완료 알림 | Layer 2 (이벤트) | 즉시성, 감사 필요 없음 |
| 이슈 일괄 등록 통지 | Layer 2 (이벤트) | 영속은 Layer 1이 이미 처리 |
| 엔지니어가 CTO에게 질문 | Layer 2 (이벤트) + Layer 1 요약 | 실시간 + 결론만 감사 |
| 에이전트 내부 막힘 해결 | Layer 3 (Advisor) | 조직적 의미 없음, 순수 추론 |
| 예산 초과 알림 | Layer 2 (이벤트) | 즉시 전파 필요 |
| 승인 요청 | Layer 1 (태스크, approvals 테이블) | 감사 필수 |
| 실시간 채팅 (사용자↔에이전트) | 별도 채널 (OpenClaw) | 사용자 대면 |

---

## 3. 태스크 시스템 (Layer 1) — 양방향 이슈 흐름

### 3.1 이슈 등록 주체

**Paperclip 원본은 하향식 위주**였지만, Haemil은 **양방향**으로 확장한다:

```
하향식 (기존 Paperclip 패턴)
  CEO → CTO: "이 프로젝트 시작해"
  CTO → Engineer: "이 기능 구현해"
  (상급자가 이슈 등록 → 하급자가 실행)

상향식 (추가 필요)
  Engineer → CTO: "이 버그 발견했어"
  Engineer → CTO: "이 부분 리팩터 필요해"
  Engineer → CTO: "이 기능 추가하면 좋을 듯"
  (하급자가 이슈 제안 → 상급자가 검토)
```

### 3.2 상향식 이슈 등록 흐름

```
1. Engineer 작업 중 발견
   └─ createIssue(proposedBy: EngineerID, assignee: null, status: "proposed")
                                                              ↑
                                        새 상태 "proposed" 추가

2. event.published("issue.proposed", {issueId, proposerId, urgency})
   ↓
3. CTO 에이전트 실시간 알림 수신
   └─ Heartbeat wake 또는 이벤트 구독으로 즉시 깨어남

4. CTO가 검토 — 3가지 결과
   ├─ 승인: status = "todo", assignee 결정 (본인 할당 or 재위임)
   ├─ 거부: status = "cancelled", decisionNote에 이유 기록
   └─ 보류: status = "backlog", 나중에 재검토
   ↓
5. event.published("issue.reviewed", {issueId, decision})
   ↓
6. Engineer 알림 수신 → 결과에 따라 행동
   ├─ 승인된 경우: assignee가 본인이면 체크아웃 후 작업
   └─ 거부/보류: 원래 작업으로 복귀
```

### 3.3 태스크 상태 확장

```
기존 Paperclip:
  backlog → todo → in_progress → in_review → done/cancelled
                          ↕
                       blocked

Haemil 확장:
  proposed ────┐
               ↓ (CTO 승인)
  backlog → todo → in_progress → in_review → done
               │           ↕                    │
               │        blocked                 │
               └────[거부]────▶ cancelled ◀─────┘
```

**새 상태**: `proposed` — 엔지니어가 제안했으나 아직 검토 전

### 3.4 자동 승인 임계값 (거버넌스 유연성)

모든 이슈를 CTO가 수동 승인하면 병목이 됨. **규모 기반 자동 승인**:

```typescript
interface AutoApprovalPolicy {
  scope: "company" | "department" | "agent";
  scopeId: string;
  criteria: {
    maxEstimatedTokens: number;    // 예: 10,000 토큰 이하 자동 승인
    maxEstimatedHours: number;     // 예: 1시간 이하 자동 승인
    allowedCategories: string[];   // 예: ["bugfix", "typo", "refactor-small"]
    disallowedCategories: string[];// 예: ["new-feature", "architecture"]
  };
}
```

**분류 예시:**

| 분류 | 처리 |
|------|------|
| 오타 수정, 주석 개선 | 자동 승인 (CTO 통지만) |
| 작은 버그 수정 (<1시간) | 자동 승인 |
| 리팩터 (중간 규모) | CTO 승인 필요 |
| 새 기능 추가 | CTO + CEO 승인 |
| 아키텍처 변경 | Board 승인 필수 |
| 예산 초과 작업 | Board 승인 필수 |

**로깅은 모두 기록** — 자동 승인이어도 audit log에 남음.

### 3.5 이슈 스키마 (확장)

```sql
CREATE TABLE issues (
  id UUID PRIMARY KEY,
  companyId UUID NOT NULL,
  projectId UUID,

  -- 등록 정보
  createdByAgentId UUID,           -- 등록한 에이전트 (새 필드)
  createdByUserId VARCHAR,         -- 또는 사용자
  proposerRole VARCHAR,            -- "engineer" | "cto" | "ceo" | "user"

  -- 할당
  assigneeAgentId UUID,
  checkoutRunId UUID,              -- Atomic Checkout
  executionLockedAt TIMESTAMP,

  -- 상태
  status VARCHAR NOT NULL,         -- proposed/backlog/todo/in_progress/...

  -- 분류 및 자동 승인
  category VARCHAR,                -- bugfix/feature/refactor/...
  estimatedTokens INT,
  estimatedHours DECIMAL,
  autoApproved BOOLEAN DEFAULT false,
  approvalId UUID,                 -- 수동 승인 시 연결

  -- 내용
  title VARCHAR NOT NULL,
  description TEXT,
  priority VARCHAR,                -- low/medium/high/critical

  -- 계층 관계
  parentIssueId UUID,              -- 서브 이슈
  requestDepth INT DEFAULT 0,      -- 위임 깊이

  -- 메타
  createdAt TIMESTAMP,
  updatedAt TIMESTAMP
);
```

---

## 4. 이벤트 시스템 (Layer 2)

### 4.1 이벤트 타입

```go
type DomainEvent interface {
    EventType() string
    TenantID() uuid.UUID
    Timestamp() time.Time
}

// 이슈 관련 이벤트
type IssueProposedEvent struct {
    IssueID, ProposerID uuid.UUID
    Category, Priority string
    EstimatedTokens int
}

type IssueBatchCreatedEvent struct {
    IssueIDs []uuid.UUID
    CreatedBy, AssigneeCandidate uuid.UUID
}

type IssueReviewedEvent struct {
    IssueID uuid.UUID
    Decision string  // "approved" | "rejected" | "deferred"
    ReviewerID uuid.UUID
    Reason string
}

type IssueCompletedEvent struct {
    IssueID, CompletedBy uuid.UUID
    ReportSummary string
}

type IssueBlockedEvent struct {
    IssueID, BlockedBy uuid.UUID
    Reason string
    NeedsAdvice bool  // CTO에게 조언 요청 플래그
}

// 예산 관련
type BudgetThresholdCrossedEvent struct {
    PolicyID uuid.UUID
    ThresholdType string  // "warning" | "hard"
    ScopeType, ScopeID string
}

// 승인 관련
type ApprovalRequestedEvent struct {
    ApprovalID uuid.UUID
    Type string  // "hire_agent" | "issue_escalation" | "budget_override"
    RequesterID uuid.UUID
}
```

### 4.2 이벤트 버스 구성

GoClaw의 DomainEventBus 직접 차용:

```go
type DomainEventBus interface {
    Publish(event DomainEvent)
    Subscribe(eventType EventType, handler HandlerFunc) UnsubscribeFunc
    Start(ctx context.Context)
    Drain(timeout time.Duration) error
}

// 기본 설정
QueueSize:      1000
WorkerCount:    2 (CPU당 1개까지 확장)
RetryAttempts:  3
DedupTTL:       5 * time.Minute
```

### 4.3 Transactional Outbox 패턴 (필수)

**문제**: Layer 1(DB 저장) 성공 후 Layer 2(이벤트 발행) 실패하면
에이전트는 이슈가 있는지 영원히 모름 → 작업 좀비화.

**해결**: 이벤트도 같은 DB 트랜잭션 안에서 `outbox` 테이블에 기록.

```sql
CREATE TABLE outbox (
  id UUID PRIMARY KEY,
  aggregateType VARCHAR NOT NULL,   -- 'issue', 'approval', 'budget'
  aggregateId UUID NOT NULL,
  eventType VARCHAR NOT NULL,       -- 'issue.proposed', 'issue.completed'
  payload JSONB NOT NULL,
  tenantId UUID NOT NULL,

  -- 발행 상태
  createdAt TIMESTAMP NOT NULL DEFAULT NOW(),
  publishedAt TIMESTAMP,            -- NULL이면 미발행
  publishAttempts INT DEFAULT 0,
  lastError TEXT
);

CREATE INDEX idx_outbox_pending
  ON outbox(createdAt)
  WHERE publishedAt IS NULL;
```

**기록 (트랜잭션 내):**
```go
func (s *IssueService) ProposeIssue(ctx context.Context, issue *Issue) error {
    tx, _ := s.db.BeginTx(ctx, nil)
    defer tx.Rollback()

    // 1. 이슈 저장
    tx.Exec("INSERT INTO issues ...")

    // 2. 같은 트랜잭션에서 outbox 기록 ← 핵심
    tx.Exec(`
        INSERT INTO outbox (id, aggregateType, aggregateId, eventType, payload, tenantId)
        VALUES ($1, 'issue', $2, 'issue.proposed', $3, $4)
    `, uuid.New(), issue.ID, toJSON(payload), issue.TenantID)

    return tx.Commit()
}
```

**발행 (별도 워커):**
```go
type OutboxRelay struct {
    db       *sql.DB
    eventBus DomainEventBus
    interval time.Duration  // 기본 500ms
}

func (r *OutboxRelay) Run(ctx context.Context) {
    ticker := time.NewTicker(r.interval)
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            r.flushPending(ctx)
        }
    }
}

func (r *OutboxRelay) flushPending(ctx context.Context) {
    rows, _ := r.db.QueryContext(ctx, `
        SELECT id, eventType, payload, tenantId
        FROM outbox
        WHERE publishedAt IS NULL AND publishAttempts < 5
        ORDER BY createdAt
        LIMIT 100
        FOR UPDATE SKIP LOCKED
    `)

    for rows.Next() {
        var row OutboxRow
        // ... scan
        err := r.eventBus.Publish(row.ToEvent())
        if err != nil {
            r.db.Exec(`
                UPDATE outbox
                SET publishAttempts = publishAttempts + 1, lastError = $1
                WHERE id = $2
            `, err.Error(), row.ID)
            continue
        }
        r.db.Exec("UPDATE outbox SET publishedAt = NOW() WHERE id = $1", row.ID)
    }
}
```

**핵심 보장**:
- 트랜잭션 성공 = 이벤트 확정 (at-least-once)
- 발행 실패해도 재시도
- `FOR UPDATE SKIP LOCKED` — 멀티 인스턴스 안전
- 멱등성 필요 (구독자는 같은 이벤트 여러 번 받을 수 있음)

**청소**: 발행 완료된 row는 1시간 후 삭제 (별도 크론).

### 4.4 에이전트 웨이크업 연동

이벤트 버스 + Paperclip Heartbeat 통합:

```go
func (a *Agent) Subscribe() {
    // 자신에게 할당된 이슈 관련 이벤트 구독
    bus.Subscribe("issue.proposed", func(e DomainEvent) {
        evt := e.(*IssueProposedEvent)
        // 이 이슈가 나에게 관련 있으면 깨어남
        if a.isRelevant(evt) {
            a.wakeUp(WakeReason{
                Kind: "issue_proposed_for_review",
                IssueID: evt.IssueID,
            })
        }
    })

    bus.Subscribe("issue.reviewed", func(e DomainEvent) {
        evt := e.(*IssueReviewedEvent)
        if evt.Decision == "approved" && a.wasProposer(evt.IssueID) {
            a.wakeUp(WakeReason{
                Kind: "your_proposal_approved",
                IssueID: evt.IssueID,
            })
        }
    })
}
```

---

## 5. Advisor 계층 (Layer 3)

### 5.1 Anthropic 내장 도구 사용

```go
func (e *EngineerAgent) RunTurn(ctx context.Context, issue *Issue) (*Response, error) {
    resp, err := e.anthropic.Messages.Create(ctx, &MessageRequest{
        Model: "claude-sonnet-4-6",  // 실행자
        Tools: []Tool{
            // 조언 도구 — Anthropic 서버 측 핸드오프
            {
                Type:    "advisor_20260301",
                Name:    "advisor",
                Model:   "claude-opus-4-6",
                MaxUses: 3,  // 요청당 최대 3번 조언 요청
            },
            // 일반 도구들
            {Type: "bash"},
            {Type: "file_ops"},
            {Type: "grep"},
        },
        Messages: e.buildMessages(issue),
        SystemPrompt: e.systemPromptWithAdvisorGuidance(),
    })
    // ↑ 이 안에서 Engineer가 막히면 자동으로 Opus한테 조언 요청
    //   단일 API 호출 내에서 완료 — 왕복 없음
    return resp, err
}
```

### 5.2 실행자 시스템 프롬프트 (Advisor 사용 가이드)

```
You are a software engineer agent. You have access to an `advisor` tool
that consults a more capable model (Opus) for difficult decisions.

Use the advisor when:
- You face an architectural decision with long-term consequences
- You're stuck on a problem after 2+ attempts with different approaches
- You need to choose between multiple valid designs
- The problem involves domain knowledge you're uncertain about

DO NOT use the advisor for:
- Simple syntax questions (you know these)
- Routine file edits
- Obvious bug fixes
- Standard refactoring patterns

You have MAX 3 advisor calls per turn. Use them wisely.
```

### 5.3 Layer 1과의 경계

**중요**: Advisor는 Layer 1(태스크)과 혼동하면 안 됨.

| 상황 | 올바른 계층 |
|------|------------|
| "이 버그 어떻게 고치지?" (기술적 막힘) | **Layer 3 (Advisor)** — Opus 호출 |
| "이 리팩터 해도 돼?" (권한 필요) | **Layer 1 (태스크)** — CTO 승인 요청 |
| "이 아키텍처 맞나?" (두뇌 부족) | **Layer 3 (Advisor)** — Opus 조언 |
| "이 작업 우선순위 뭐지?" (조직 결정) | **Layer 1 (태스크)** — CTO 결정 필요 |

**규칙**: 순수한 **추론 능력** 문제는 Advisor, **권한/결정권** 문제는 태스크.

---

## 6. 통합 시나리오 예시

### 시나리오 A: CTO의 정상 작업 배포

```
[CTO 에이전트]
1. 사용자 요구사항 분석
2. 이슈 5개 일괄 등록 (트랜잭션 1번)
   └─ INSERT INTO issues (...) VALUES (...) × 5
3. createIssuesBatch() 완료
4. event.published("issue.batch_created", {...})

[이벤트 버스]
5. Engineer 에이전트에게 실시간 전달 (5ms)

[Engineer 에이전트]
6. 웨이크업 (이벤트 핸들러)
7. 첫 이슈 체크아웃 (atomic)
   └─ UPDATE issues SET status='in_progress' WHERE id=...
8. Anthropic API 호출 시작
   └─ 실행자: Sonnet, 도구: bash/file_ops + advisor
9. 작업 중 복잡한 타입 설계 난관
10. 내부적으로 advisor 도구 호출
    └─ Opus가 조언 반환 (API 호출 1번 내에서)
11. Sonnet이 조언 받아 계속 실행
12. 작업 완료 → 보고서 작성
13. submitReport() — 이슈 코멘트 추가 + status='in_review'
14. event.published("issue.completed", {issueId, summary})

[CTO 에이전트]
15. 웨이크업 → 보고서 검토 → status='done'
```

**DB 왕복**: CTO 2번 + Engineer 3번 = **5번** (태스크만 썼으면 15+)
**API 호출**: Engineer 1번 (advisor는 내장이라 포함)

### 시나리오 B: Engineer가 버그 발견

```
[Engineer 에이전트]
1. 이슈 A 작업 중
2. 관련 없는 파일에서 버그 발견
3. proposeIssue({
     title: "Null pointer in auth.go:123",
     category: "bugfix",
     estimatedHours: 0.5,
     proposerRole: "engineer",
   })
   └─ INSERT INTO issues status='proposed'
4. 자동 승인 정책 확인
   └─ estimatedHours < 1 AND category == "bugfix" → 자동 승인
   └─ UPDATE status='backlog', autoApproved=true
5. event.published("issue.proposed", {autoApproved: true})
6. 원래 작업 이슈 A 계속

[CTO 에이전트]
7. 이벤트 수신 (통지만, 승인 불필요)
8. 관심 없으면 무시 (wake 안 함)
   또는 관심 있으면 로그만 확인

(CTO 개입 없이 처리 완료)
```

### 시나리오 C: Engineer가 큰 기능 제안

```
[Engineer 에이전트]
1. 작업 중 "이 기능 추가하면 시스템 10배 빨라질 듯" 발견
2. proposeIssue({
     title: "Add Redis caching layer",
     category: "new-feature",
     estimatedHours: 8,
     estimatedTokens: 500000,
   })
3. 자동 승인 정책 확인
   └─ category == "new-feature" → 자동 승인 불가
   └─ status='proposed'
4. event.published("issue.proposed", {needsReview: true, urgency: "normal"})

[CTO 에이전트]
5. 웨이크업 (이벤트 핸들러)
6. 제안 내용 검토
7. advisor 도구 호출 — Opus에게 "이 설계 괜찮아?" 조언 (내부)
8. 판단: 좋은 제안이지만 현재 스프린트 아님
9. updateIssue({
     status: 'backlog',
     priority: 'medium',
     decisionNote: "Good idea, defer to Q2"
   })
10. event.published("issue.reviewed", {decision: "deferred"})

[Engineer 에이전트]
11. 웨이크업 (제안 결과)
12. 결과 확인 → 원래 작업 복귀
```

### 시나리오 D: Engineer가 막혀서 CTO 조언 필요

```
[Engineer 에이전트]
1. 이슈 작업 중
2. advisor 도구 3번 호출 완료 (Opus 소진)
3. 여전히 해결 안 됨 — 이건 "지능 문제" 아닌 "도메인 결정"
4. createIssue({
     title: "Need guidance: database schema choice",
     parentIssueId: currentIssue.id,
     category: "advice-request",
     assignee: CTO_ID,
     priority: "high",
   })
5. status='blocked', blockReason='awaiting_advice'
6. event.published("issue.blocked", {needsAdvice: true})

[CTO 에이전트]
7. 웨이크업 (blocked 이벤트)
8. 원본 이슈 + 서브 이슈 컨텍스트 확인
9. CTO도 advisor 도구 사용해서 깊이 분석 (더 많은 컨텍스트)
10. 결론을 댓글로 작성
11. updateIssue(subIssue, status='done')
12. updateIssue(parentIssue, status='in_progress', unblocked=true)
13. event.published("issue.unblocked", {...})

[Engineer 에이전트]
14. 웨이크업 → 조언 읽기 → 작업 재개
```

---

## 7. 데이터 흐름 요약

```
┌──────────────────┐     ┌──────────────────┐
│   Engineer       │     │      CTO         │
│  (Sonnet + adv)  │     │  (Sonnet + adv)  │
└──────────────────┘     └──────────────────┘
         │                        │
         │ ①propose issue         │ ①create issue batch
         ▼                        ▼
    ┌────────────────────────────────┐
    │     PostgreSQL (Layer 1)       │
    │     - issues, approvals,       │
    │       heartbeat_runs, costs    │
    └────────────────────────────────┘
         │                        │
         │ ②publish event         │ ②publish event
         ▼                        ▼
    ┌────────────────────────────────┐
    │   DomainEventBus (Layer 2)     │
    │   - issue.proposed             │
    │   - issue.batch_created        │
    │   - issue.reviewed             │
    │   - issue.completed            │
    └────────────────────────────────┘
         │                        │
         │ ③wake up               │ ③wake up
         ▼                        ▼
    ┌──────────────┐        ┌──────────────┐
    │  Heartbeat   │        │  Heartbeat   │
    │   Service    │        │   Service    │
    └──────────────┘        └──────────────┘
         │                        │
         │ ④execute turn          │ ④execute turn
         ▼                        ▼
    ┌────────────────────────────────┐
    │   Anthropic Messages API       │
    │   - executor: Sonnet           │
    │   - advisor: Opus (Layer 3)    │
    │   - other tools (bash, files)  │
    └────────────────────────────────┘
```

---

## 8. 비용 추적 (중요)

Advisor 사용이 비용에 미치는 영향:

```typescript
interface CostEvent {
  agentId: uuid;
  issueId: uuid;

  // 기존 필드
  provider: "anthropic";
  model: "claude-sonnet-4-6";
  inputTokens: number;
  outputTokens: number;
  costCents: number;

  // 신규: Advisor 사용 추적
  advisorUses: number;              // advisor 호출 횟수
  advisorInputTokens: number;       // Opus 요금으로 계산
  advisorOutputTokens: number;
  advisorCostCents: number;         // 별도 계산
}
```

**예산 정책에 반영:**
- `agents.list[].maxAdvisorUsesPerTurn` — 에이전트별 제한
- `budgetPolicies.advisorRatio` — advisor 비용 비율 모니터링
- 이상 감지: advisor 사용이 갑자기 급증하면 경고

---

## 9. Go 구현 스케치

```go
// internal/agent/communication/layers.go

type CommunicationLayers struct {
    taskStore    TaskStore       // Layer 1 — PostgreSQL
    eventBus     DomainEventBus  // Layer 2 — in-memory queue
    anthropic    AnthropicClient // Layer 3 — API with advisor tool
    approvals    ApprovalService
    autoApproval AutoApprovalPolicy
}

// Layer 1: 이슈 제안 (양방향)
func (c *CommunicationLayers) ProposeIssue(
    ctx context.Context,
    proposer AgentID,
    issue *IssueDraft,
) (*Issue, error) {
    // 1. 자동 승인 정책 확인
    autoApprove := c.autoApproval.Check(issue)

    // 2. DB 저장
    saved := &Issue{
        ...issue,
        Status: ternary(autoApprove, "backlog", "proposed"),
        CreatedByAgentID: proposer,
        AutoApproved: autoApprove,
    }
    if err := c.taskStore.Insert(ctx, saved); err != nil {
        return nil, err
    }

    // 3. Layer 2 이벤트 발행
    c.eventBus.Publish(&IssueProposedEvent{
        IssueID:     saved.ID,
        ProposerID:  proposer,
        AutoApproved: autoApprove,
        NeedsReview: !autoApprove,
    })

    return saved, nil
}

// Layer 1: 이슈 검토 (CTO용)
func (c *CommunicationLayers) ReviewIssue(
    ctx context.Context,
    reviewer AgentID,
    issueID uuid.UUID,
    decision ReviewDecision,
) error {
    // 권한 확인 (5계층 보안)
    if err := c.checkReviewPermission(ctx, reviewer, issueID); err != nil {
        return err
    }

    // DB 업데이트
    updates := map[string]any{
        "status":       decision.NewStatus,  // "backlog"/"cancelled"
        "decisionNote": decision.Note,
        "reviewedBy":   reviewer,
        "reviewedAt":   time.Now(),
    }
    if err := c.taskStore.Update(ctx, issueID, updates); err != nil {
        return err
    }

    // Layer 2 이벤트
    c.eventBus.Publish(&IssueReviewedEvent{
        IssueID:    issueID,
        Decision:   decision.Type,
        ReviewerID: reviewer,
    })

    return nil
}

// Layer 3: Advisor 사용 실행 턴
func (c *CommunicationLayers) RunAgentTurn(
    ctx context.Context,
    agent *Agent,
    issue *Issue,
) (*TurnResult, error) {
    req := &MessageRequest{
        Model: agent.ExecutorModel,
        Tools: append(
            agent.StandardTools,
            Tool{
                Type:    "advisor_20260301",
                Name:    "advisor",
                Model:   agent.AdvisorModel,
                MaxUses: agent.MaxAdvisorCalls,
            },
        ),
        Messages: c.buildMessages(issue),
    }

    resp, err := c.anthropic.Messages.Create(ctx, req)
    if err != nil {
        return nil, err
    }

    // Advisor 사용량 추적 (비용 이벤트)
    c.recordCost(&CostEvent{
        AgentID: agent.ID,
        IssueID: issue.ID,
        InputTokens: resp.Usage.InputTokens,
        OutputTokens: resp.Usage.OutputTokens,
        AdvisorUses: resp.Usage.AdvisorCalls,
        AdvisorInputTokens: resp.Usage.AdvisorInputTokens,
        AdvisorOutputTokens: resp.Usage.AdvisorOutputTokens,
    })

    return &TurnResult{Response: resp}, nil
}
```

---

## 10. 설계 결정 요약

| 결정 사항 | 선택 | 이유 |
|----------|------|------|
| 통신 계층 수 | **3개** | What/When/HowSmart 분리 |
| 감사 추적 | Layer 1 (태스크) | PostgreSQL 영속성 |
| 실시간 신호 | Layer 2 (이벤트 버스 + Outbox) | ms 단위 지연 + 일관성 보장 |
| 지능 부스트 | Layer 3 (Anthropic 내장) | 0 오버헤드, -12% 비용 |
| 이슈 등록 방향 | **양방향** | 엔지니어 발견 반영 필수 |
| 자동 승인 | 임계값 기반 + 누적 감사 | CTO 병목 방지 + 악용 방지 |
| 승인 거부권 | CTO 우선, Board 최종 | 거버넌스 유지 |
| 비용 추적 | Advisor 분리 집계 | 토큰 단가 다름 |
| 에이전트 웨이크업 | 이벤트 구독 + Heartbeat 폴링 fallback | 즉시성 + 안정성 |
| **Outbox 패턴** | **필수** | 태스크-이벤트 일관성 보장 |
| **SLA 타이머** | **단계적 4단계** | 좀비 이슈 방지 + 부드러운 에스컬레이션 |
| SLA 체크 방식 | 크론 1분 (v0) → 타이머 큐 (v2) | 단순하게 시작, 필요 시 확장 |

---

## 11. 리스크 완화 전략

### 확정 완화책 (필수 구현)

| 리스크 | 완화책 | 상태 |
|--------|--------|------|
| **태스크-이벤트 일관성** | Transactional Outbox 패턴 (§4.3) | ✅ 확정 |
| **Proposed 이슈 좀비화** | SLA 타이머 + 단계적 에스컬레이션 (§13) | ✅ 확정 |
| **자동 승인 악용** | 누적 임계값 + 주기적 감사 리뷰 | ✅ 확정 |
| **이벤트 폭풍 (Thundering Herd)** | 배치 이벤트 우선 + 에이전트 wake 디바운싱 (최소 5초) | ✅ 확정 |
| **무한 에스컬레이션** | `requestDepth` 상한 3단계 + 순환 감지 | ✅ 확정 |
| **멀티테넌트 이벤트 누출** | 이벤트 페이로드에 `tenantId` 필수 + 구독 필터 자동 주입 | ✅ 확정 |

### 모니터링 (운영 중 관찰)

| 리스크 | 지표 | 임계값 |
|--------|------|--------|
| Advisor 비용 급증 | `advisorRatio` (전체 비용 중 advisor 비율) | > 30% 경고 |
| 자동 승인 누적 | 에이전트별 일일 auto-approved 개수 | > 20건 경고 |
| SLA 위반율 | 30일 내 stage 3+ 도달 이슈 비율 | > 10% 경고 |

### 수용 가능 (MVP에서 대응 안 함)

- Opus 장애 시 fallback — Advisor 없어도 작동하므로 graceful degradation만
- 디버깅 복잡도 — OpenTelemetry trace_id 전파로 충분

---

## 12. 미결정 항목 (향후 논의)

- [ ] **자동 승인 임계값 구체 수치** — 카테고리별 estimatedHours/tokens 표 필요
- [ ] **이벤트 버스 분산 전환 시점** — 단일 인스턴스 → Redis Streams
- [ ] **Engineer가 다른 Engineer에게 이슈 제안 가능한가** — 허용 범위
- [ ] **proposed 상태 이슈의 예산 예약 여부** — 거부 시 환불
- [ ] **SLA 정책 기본값** — v0에서는 프로젝트당 단일 정책으로 시작
- [ ] **businessHoursOnly 타임존** — 회사별 타임존 설정

---

## 13. SLA 타이머 상세 설계

### 13.1 적용 상태

모든 "대기" 상태에 SLA가 걸림:

| 상태 | 의미 | SLA 필요성 |
|------|------|-----------|
| `proposed` | Engineer가 제안, 리뷰 대기 | ⭐⭐⭐ 가장 중요 |
| `todo` | 할당됐는데 시작 안 함 | ⭐⭐ |
| `in_progress` | 작업 중인데 지연 | ⭐⭐ |
| `blocked` | 막혔는데 해결 안 됨 | ⭐⭐⭐ |
| `in_review` | 완료 보고, 검토 대기 | ⭐⭐ |
| `awaiting_approval` | 승인 대기 | ⭐⭐⭐ |
| `backlog` / `done` / `cancelled` | SLA 없음 | - |

### 13.2 단계적 에스컬레이션 (Graduated Escalation)

한 번에 에스컬레이션하면 공격적. **4단계로 부드럽게 압박**:

```
t=0 (상태 진입)
  │
  ├─ 50% 경과 ─▶ Stage 1: Gentle Reminder
  │              담당자에게 리마인드 알림
  │
  ├─ 75% 경과 ─▶ Stage 2: Warning
  │              상급자에게 경고 (아직 조치 시간 있음)
  │
  ├─ 100% 경과 ─▶ Stage 3: Escalation
  │              상급자에게 자동 재할당
  │
  └─ 200% 경과 ─▶ Stage 4: Board Intervention
                  인간 개입 강제 (사용자 알림)
```

### 13.3 SLA 정책 스키마

```sql
CREATE TABLE sla_policies (
  id UUID PRIMARY KEY,
  companyId UUID NOT NULL,
  name VARCHAR NOT NULL,

  -- 적용 조건 (매칭 기준)
  status VARCHAR NOT NULL,           -- 'proposed', 'blocked', ...
  priority VARCHAR,                  -- 'critical'|'high'|'normal'|'low'|NULL(전체)
  category VARCHAR,                  -- 'bugfix'|'feature'|NULL
  scopeType VARCHAR,                 -- 'company'|'project'|'agent'|NULL
  scopeId UUID,

  -- 단계별 지속 시간
  stage1Duration INTERVAL NOT NULL,  -- Gentle reminder
  stage2Duration INTERVAL NOT NULL,  -- Warning
  stage3Duration INTERVAL NOT NULL,  -- Escalation
  stage4Duration INTERVAL NOT NULL,  -- Board intervention

  -- 단계별 액션
  stage1Action VARCHAR NOT NULL,     -- 'notify_assignee'
  stage2Action VARCHAR NOT NULL,     -- 'warn_manager'
  stage3Action VARCHAR NOT NULL,     -- 'escalate_to_manager' | 'reassign_to_board'
  stage4Action VARCHAR NOT NULL,     -- 'board_intervention' | 'user_alert'

  -- 영업 시간만 카운트? (주말/휴일 제외)
  businessHoursOnly BOOLEAN DEFAULT false,

  isActive BOOLEAN DEFAULT true,
  createdAt TIMESTAMP,
  updatedAt TIMESTAMP
);
```

### 13.4 기본 정책 예시

| 이름 | status | priority | stage1 | stage2 | stage3 | stage4 |
|------|--------|----------|--------|--------|--------|--------|
| proposed-critical | proposed | critical | 30m | 1h | 2h | 4h |
| proposed-high | proposed | high | 2h | 4h | 6h | 12h |
| proposed-normal | proposed | normal | 12h | 18h | 24h | 48h |
| proposed-low | proposed | low | 1d | 2d | 3d | 7d |
| blocked-default | blocked | * | 1h | 2h | 4h | 8h |
| in_review-default | in_review | * | 2h | 4h | 8h | 24h |
| in_progress-stuck | in_progress | * | 4h | 8h | 16h | 48h |

**매칭 규칙**: 가장 구체적인 정책 우선
- `status + priority + category` 매칭 → 최우선
- `status + priority` 매칭 → 차순위
- `status`만 매칭 → 기본값

### 13.5 이슈 스키마 확장

```sql
ALTER TABLE issues ADD COLUMN status_changed_at TIMESTAMP NOT NULL DEFAULT NOW();
ALTER TABLE issues ADD COLUMN sla_policy_id UUID REFERENCES sla_policies(id);
ALTER TABLE issues ADD COLUMN sla_stage INT NOT NULL DEFAULT 0;     -- 0=정상, 1~4
ALTER TABLE issues ADD COLUMN sla_last_action_at TIMESTAMP;
ALTER TABLE issues ADD COLUMN sla_paused BOOLEAN DEFAULT false;     -- 일시 정지

-- 검색 최적화
CREATE INDEX idx_issues_sla_check
  ON issues(status, status_changed_at)
  WHERE status IN ('proposed','blocked','in_review','todo','in_progress')
    AND sla_paused = false;
```

### 13.6 SLA 사건 로그

```sql
CREATE TABLE sla_incidents (
  id UUID PRIMARY KEY,
  issueId UUID NOT NULL REFERENCES issues(id),
  policyId UUID NOT NULL REFERENCES sla_policies(id),

  stage INT NOT NULL,               -- 1~4
  action VARCHAR NOT NULL,
  triggeredAt TIMESTAMP NOT NULL,

  -- 상태 스냅샷
  stateAtTrigger VARCHAR,
  assigneeAtTrigger UUID,
  elapsedSeconds INT,

  -- 해결 추적
  resolvedAt TIMESTAMP,
  resolvedBy VARCHAR,               -- 'assignee_action'|'escalation'|'manual'
  notes TEXT
);

CREATE INDEX idx_sla_incidents_issue ON sla_incidents(issueId);
CREATE INDEX idx_sla_incidents_open
  ON sla_incidents(triggeredAt)
  WHERE resolvedAt IS NULL;
```

### 13.7 체커 구현 (Go)

```go
type SLAChecker struct {
    db       *sql.DB
    eventBus DomainEventBus
    interval time.Duration  // 기본 1분
}

func (s *SLAChecker) Run(ctx context.Context) {
    ticker := time.NewTicker(s.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.checkExpiredIssues(ctx)
        }
    }
}

func (s *SLAChecker) checkExpiredIssues(ctx context.Context) {
    rows, _ := s.db.QueryContext(ctx, `
        SELECT i.id, i.status, i.status_changed_at, i.sla_stage,
               i.assigneeAgentId, i.priority, i.category, p.*
        FROM issues i
        LEFT JOIN sla_policies p ON p.id = i.sla_policy_id
        WHERE i.status IN ('proposed','blocked','in_review','todo','in_progress')
          AND i.sla_paused = false
          AND i.status_changed_at < NOW() - INTERVAL '1 minute'
    `)

    for rows.Next() {
        var issue IssueSLAView
        // ... scan

        elapsed := time.Since(issue.StatusChangedAt)
        if issue.Policy.BusinessHoursOnly {
            elapsed = s.businessHoursBetween(issue.StatusChangedAt, time.Now())
        }

        nextStage := s.determineStage(elapsed, issue.Policy)
        if nextStage > issue.SLAStage {
            s.triggerAction(ctx, issue, nextStage)
        }
    }
}

func (s *SLAChecker) determineStage(elapsed time.Duration, p Policy) int {
    switch {
    case elapsed >= p.Stage4Duration:
        return 4
    case elapsed >= p.Stage3Duration:
        return 3
    case elapsed >= p.Stage2Duration:
        return 2
    case elapsed >= p.Stage1Duration:
        return 1
    }
    return 0
}

func (s *SLAChecker) triggerAction(ctx context.Context, issue IssueSLAView, stage int) {
    tx, _ := s.db.BeginTx(ctx, nil)
    defer tx.Rollback()

    // 1. sla_incidents 기록
    tx.Exec(`
        INSERT INTO sla_incidents (id, issueId, policyId, stage, action,
                                    triggeredAt, stateAtTrigger, assigneeAtTrigger,
                                    elapsedSeconds)
        VALUES ($1, $2, $3, $4, $5, NOW(), $6, $7, $8)
    `, uuid.New(), issue.ID, issue.PolicyID, stage,
       issue.Policy.ActionForStage(stage), issue.Status,
       issue.AssigneeAgentID, int(time.Since(issue.StatusChangedAt).Seconds()))

    // 2. issues.sla_stage 업데이트
    tx.Exec(`
        UPDATE issues SET sla_stage = $1, sla_last_action_at = NOW()
        WHERE id = $2
    `, stage, issue.ID)

    // 3. outbox 이벤트 기록 (같은 트랜잭션)
    tx.Exec(`
        INSERT INTO outbox (id, aggregateType, aggregateId, eventType, payload, tenantId)
        VALUES ($1, 'issue', $2, $3, $4, $5)
    `, uuid.New(), issue.ID, s.eventTypeForStage(stage),
       toJSON(issue), issue.TenantID)

    tx.Commit()

    // 4. 단계별 후속 처리 (자동 재할당 등)
    switch issue.Policy.ActionForStage(stage) {
    case "escalate_to_manager":
        managerID := s.getManagerOf(issue.AssigneeAgentID)
        s.reassignIssue(ctx, issue.ID, managerID, "SLA Stage 3")
    case "board_intervention":
        s.approvalService.CreateBoardIntervention(ctx, issue.ID)
    }
}
```

### 13.8 상태 전이 시 타이머 리셋

```go
func (s *IssueService) UpdateStatus(ctx context.Context, issueID uuid.UUID, newStatus string) error {
    tx, _ := s.db.BeginTx(ctx, nil)
    defer tx.Rollback()

    // 1. 열린 incident 해결 처리
    tx.Exec(`
        UPDATE sla_incidents
        SET resolvedAt = NOW(), resolvedBy = 'status_change'
        WHERE issueId = $1 AND resolvedAt IS NULL
    `, issueID)

    // 2. 새 정책 매칭
    var newPolicyID *uuid.UUID
    if policy := s.matchSLAPolicy(newStatus, priority, category); policy != nil {
        newPolicyID = &policy.ID
    }

    // 3. 이슈 업데이트 — 타이머 리셋
    tx.Exec(`
        UPDATE issues
        SET status = $1,
            status_changed_at = NOW(),
            sla_policy_id = $2,
            sla_stage = 0,
            sla_last_action_at = NULL
        WHERE id = $3
    `, newStatus, newPolicyID, issueID)

    // 4. outbox 이벤트
    tx.Exec("INSERT INTO outbox ...")

    return tx.Commit()
}
```

### 13.9 엣지 케이스

**휴일/주말 배제:**
```go
// businessHoursOnly=true인 정책은 주말 제외 계산
func (s *SLAChecker) businessHoursBetween(from, to time.Time) time.Duration {
    // 주말 시간 제외 + 회사 타임존 고려
    // 구현: 평일 시간만 누적
}
```

**부모-자식 의존:**
```
Parent blocked=true (자식 이슈 대기 중)
  → Parent의 sla_paused = true
  → Child만 SLA 돌아감
  → Child 해결 시 Parent 재개 (status_changed_at 갱신)
```

**예산 Hard Stop:**
```go
func (s *BudgetService) HardStopAgent(agentID uuid.UUID) {
    s.db.Exec(`
        UPDATE issues SET sla_paused = true
        WHERE assigneeAgentId = $1 AND status IN ('todo','in_progress','blocked')
    `, agentID)
}

func (s *BudgetService) ResumeFromBudget(agentID uuid.UUID) {
    // 예산 승인 후 — 타이머 재시작 (status_changed_at 갱신으로 공평하게)
    s.db.Exec(`
        UPDATE issues
        SET sla_paused = false, status_changed_at = NOW()
        WHERE assigneeAgentId = $1 AND sla_paused = true
    `, agentID)
}
```

**SLA 폭탄 방지 (rate limit):**
```go
if s.recentActionsCount(issue.AssigneeAgentID, 1*time.Minute) > 10 {
    // 같은 에이전트에 1분 내 10건 이상 액션 → 보류
    return
}
```

### 13.10 모니터링 쿼리

```sql
-- SLA 위반율 (지난 30일)
SELECT
  status,
  COUNT(*) AS total,
  SUM(CASE WHEN sla_stage >= 3 THEN 1 ELSE 0 END) AS escalated,
  ROUND(100.0 * SUM(CASE WHEN sla_stage >= 3 THEN 1 ELSE 0 END) / COUNT(*), 2) AS escalation_rate_pct
FROM issues
WHERE createdAt > NOW() - INTERVAL '30 days'
GROUP BY status;

-- 상태별 평균 대기 시간
SELECT
  status,
  AVG(EXTRACT(EPOCH FROM (NOW() - status_changed_at)) / 3600) AS avg_hours
FROM issues
WHERE status IN ('proposed','blocked','in_review')
GROUP BY status;

-- 에이전트별 SLA 준수율
SELECT
  assigneeAgentId,
  COUNT(*) AS total,
  SUM(CASE WHEN sla_stage = 0 THEN 1 ELSE 0 END) AS on_time,
  ROUND(100.0 * SUM(CASE WHEN sla_stage = 0 THEN 1 ELSE 0 END) / COUNT(*), 2) AS compliance_pct
FROM issues
WHERE status = 'done' AND createdAt > NOW() - INTERVAL '7 days'
GROUP BY assigneeAgentId;
```

### 13.11 MVP 단계별 구현

**v0 (최소)**
- 크론 1분 간격
- SLA 정책 1~2개 (`proposed-default`, `blocked-default`)
- Stage 1, 3만 구현 (reminder + escalation)
- Board intervention은 사용자에게 직접 알림

**v1 (확장)**
- 모든 상태별 기본 정책
- 4단계 전부 구현
- priority/category 차등
- businessHoursOnly 지원

**v2 (최적화)**
- 크론 → 타이머 큐 전환 (이슈 10K+ 시)
- Redis Sorted Set 또는 PostgreSQL pg_cron
- 머신러닝 기반 동적 SLA (에이전트 특성 반영)

### 13.12 워크플로우 예시 (Engineer 제안 → 24시간 SLA)

```
t=0:00  Engineer 이슈 제안
        status='proposed', status_changed_at=t0
        정책 매칭: 'proposed-normal' (12h/18h/24h/48h)
        sla_stage=0

t=0:01  SLA Checker 크론
        elapsed=1분 → stage 0 유지

...

t=12:00 크론 → stage 1 트리거
        [Stage 1: notify_assignee]
        outbox INSERT: "issue.stale" → Engineer wake
        sla_incidents INSERT (stage=1)
        issues.sla_stage=1

t=18:00 크론 → stage 2 트리거
        [Stage 2: warn_manager]
        outbox INSERT: "sla.warning" → CTO wake
        "⚠️ 6시간 후 에스컬레이션됨"

t=24:00 크론 → stage 3 트리거
        [Stage 3: escalate_to_manager]
        issues.assigneeAgentId = CEO (CTO의 상급자)
        outbox INSERT: "issue.escalated" → CEO wake

t=48:00 크론 → stage 4 트리거
        [Stage 4: board_intervention]
        approvals INSERT (type='sla_timeout')
        사용자(광섭)에게 직접 알림
```

**중간 해결 (t=15:00 CTO가 검토한 경우):**
- `UpdateStatus('backlog')` 호출
- 열린 incident의 `resolvedBy='status_change'`로 기록
- `status_changed_at` 리셋
- 새 정책 매칭 (`backlog`는 SLA 없음)

---

## 14. 다음 단계

1. 이 설계서 검토 및 확정
2. Go 인터페이스 코드로 구현 시작
3. PostgreSQL 스키마 마이그레이션 작성 (issues, outbox, sla_policies, sla_incidents)
4. Outbox Relay 워커 구현 및 테스트
5. SLA Checker 크론 구현 및 테스트
6. 이벤트 버스 통합 테스트
7. Anthropic advisor 도구 실제 호출 PoC

---

## 부록: 참고 자료

- [Anthropic Advisor Strategy 블로그](https://claude.com/blog/the-advisor-strategy)
- `analysis/platforms/paperclip.md` — 태스크 기반 거버넌스
- `analysis/platforms/goclaw.md` — DomainEventBus 구현
- `analysis/platforms/claw-code.md` — ConversationRuntime 턴 루프
