# Paperclip 상세 분석

> 분석일: 2026-04-10
> 분석 범위: TypeScript 전체 (842개 TS 파일)

---

## 1. 개요

| 항목 | 내용 |
|------|------|
| 한줄 설명 | AI 에이전트 오케스트레이션 — 계층적 조직, 거버넌스, 예산 통제 |
| 언어 | TypeScript (Express.js + React) |
| 라이선스 | MIT |
| 역할 | 관리자 — 조직 구조, 거버넌스, 예산, Atomic Checkout, 하트비트 |
| 규모 | 842 TS 파일, PostgreSQL (Drizzle ORM) |
| 핵심 철학 | "OpenClaw는 직원, Paperclip은 회사다" |

---

## 2. 아키텍처

### 2.1 전체 구조

```
React UI (Org Chart, Task Board, Budgets, Dashboard)
    ↓
Express REST API (에이전트/태스크/비용/승인 — 모든 에이전트가 동일 API 사용)
    ↓
PostgreSQL (회사, 에이전트, 태스크, 비용, 예산, 승인)
    ↓
에이전트 어댑터 (Claude Code, Codex, Cursor, OpenClaw...)
```

### 2.2 핵심 서비스

| Service | 역할 | 크기 |
|---------|------|------|
| heartbeat.ts | Heartbeat 실행, 컨텍스트, 실행 관리 | 4,533줄 |
| issues.ts | 태스크 CRUD, 체크아웃, 상태 전이 | 2,900줄 |
| routines.ts | 정기 실행 작업 (Cron) | 2,500줄 |
| company-portability.ts | 회사 내보내기/가져오기 | 5,900줄 |
| budgets.ts | 예산 정책, 평가, 자동 정지 | 958줄 |
| agents.ts | 에이전트 CRUD, 역할 관리 | 709줄 |
| approvals.ts | 승인 워크플로우 | ~450줄 |
| costs.ts | 비용 이벤트, 집계 | ~450줄 |

---

## 3. 핵심 소스 분석

### 3.1 계층적 조직 구조

```
Company: "AI Note-Taking Startup"
├─ CEO (reportsTo: null) [월 $5,000]
│  ├─ CTO (reportsTo: CEO) [월 $2,000]
│  │  ├─ Engineer-1 [월 $500]
│  │  └─ Engineer-2 [월 $500]
│  ├─ Marketing Manager [월 $1,000]
│  │  └─ Social Media Agent
│  └─ Product Manager [월 $1,500]
```

```typescript
// agents 테이블
{
  id: uuid,
  companyId: uuid,
  name: string,
  role: "ceo" | "cto" | "manager" | "general",
  reportsTo: uuid | null,     // 상급자 (null = CEO)
  status: "active" | "idle" | "paused" | "running" | "error",
  adapterType: string,        // claude_local, process, http...
  budgetMonthlyCents: number,
  spentMonthlyCents: number,
}
```

### 3.2 Atomic Task Checkout

**태스크 상태 전이:**
```
backlog → todo → in_progress → in_review → done/cancelled
                      ↕
                   blocked
```

**원자적 체크아웃:**
```sql
UPDATE issues SET
  status = 'in_progress',
  assigneeAgentId = :requestingAgentId,
  checkoutRunId = :currentRunId,
  executionLockedAt = NOW()
WHERE
  id = :issueId AND
  (assigneeAgentId IS NULL OR assigneeAgentId = :requestingAgentId) AND
  (checkoutRunId IS NULL OR checkoutRunId는 stale)
```

- 동시 체크아웃 불가 — DB 원자성 보장
- Stale 체크아웃 자동 복구 (Heartbeat 완료 후 잔류 lock adopt)
- 멱등성: 같은 에이전트 재체크아웃 허용

### 3.3 Heartbeat 시스템

```
1. 트리거 (스케줄/이벤트/온디맨드)
2. heartbeatService.startHeartbeat(agentId)
   ├─ 작업 환경 생성 (격리)
   ├─ RuntimeServices 생성
   ├─ 예산 확인
   └─ heartbeat_runs 레코드 (status='queued')
3. 어댑터 실행 (Process/HTTP/Local)
4. 에이전트 실행
   ├─ 할당된 태스크 목록 조회
   ├─ 태스크 체크아웃 (in_progress)
   ├─ 작업 수행
   └─ 태스크 상태 업데이트 (done/blocked)
5. 정리
   ├─ Runtime 종료
   ├─ 결과 저장
   ├─ heartbeat_runs.status = 'succeeded/failed'
   └─ costEvents 생성
```

**Context Snapshot (에이전트에 전달):**
```typescript
{
  agentId, companyId,
  issueId, taskKey: "CMP-123",
  wakeReason: "scheduled" | "issue_assigned" | "issue_commented",
  paperclipWake: {
    reason, issue: { id, title, status, priority },
    comments: [{ body, author, createdAt }],
  },
  sessionIdBefore: string,  // 이전 세션 복원
}
```

### 3.4 3단계 예산 통제

```
Stage 1: Visibility — 대시보드에 지출 표시
Stage 2: Soft Alert — 경고 (80%) → budgetIncidents (warning)
Stage 3: Hard Stop — 한도 초과 → 자동 정지 + Board 승인 필요
```

**예산 정책:**
```typescript
{
  scopeType: "company" | "agent" | "project",
  metric: "billed_cents",
  windowKind: "monthly" | "lifetime",
  amount: number,          // 한도 (센트)
  warnPercent: 80,         // 경고 임계값
  hardStopEnabled: true,
  notifyEnabled: true,
}
```

**Hard Stop 흐름:**
```
비용 초과 감지 → budgetIncidents(hard) 생성
  → approvals(budget_override_required) 생성
  → agents.status = 'paused', pauseReason = 'budget'
  → Board 승인 대기
  → 승인 시 resumeScopeFromBudget() → agents.status = 'idle'
```

### 3.5 승인 시스템

```typescript
// approvals 테이블
{
  type: "hire_agent" | "budget_override_required",
  status: "pending" | "approved" | "rejected" | "revision_requested",
  requestedByAgentId | requestedByUserId,
  decidedByUserId,
  payload: {},
  decisionNote: string,
}
```

**승인 유형:**
1. **에이전트 채용** — CEO 제안 → Board 승인 → 에이전트 활성화
2. **예산 초과** — Hard Stop → Board 예산 증액/거부
3. **전략 변경** — CEO 전략 → Board 검토

### 3.6 다중 에이전트 조율

**태스크 기반 통신:**
```
CEO → 태스크 생성 (할당: CTO) → CTO wake
CTO → 서브 태스크 생성 (할당: Engineer) → Engineer wake
Engineer → blocked → 댓글로 설명 → CTO wake → 해결
```

**위임 프로토콜:**
- requestDepth 추적 (CEO→CTO: 0, CTO→Engineer: 1)
- 부적절한 태스크 → 상급자에게 재할당
- Escalation: Engineer → Manager → CEO → Board

### 3.7 Session Persistence

```typescript
// agentTaskSessions 테이블
{
  agentId, issueId,
  sessionId: string,          // 에이전트 내부 세션 ID
  sessionData: {},            // 마지막 세션 상태
  checkpointedAt: timestamp,
}
```

Heartbeat 간 세션 유지 → 이전 작업 상태에서 재개 가능

---

## 4. 우리가 가져올 것

### 4.1 반드시 가져올 것 (MUST)

| 컴포넌트 | 가져올 패턴 | 이유 |
|----------|-------------|------|
| **계층적 조직** | reportsTo 기반 트리 + role 분류 | 멀티 에이전트 관리 핵심 |
| **Atomic Checkout** | DB 원자성 기반 태스크 잠금 + stale 복구 | 중복 작업 방지 |
| **Heartbeat** | 주기적 에이전트 깨우기 + Context Snapshot | 에이전트 실행 생명주기 |
| **3단계 예산 통제** | Visibility → Soft Alert → Hard Stop | 비용 폭주 방지 |
| **승인 시스템** | Board 승인 게이트 (채용, 예산, 전략) | 인간 감시 |
| **태스크 기반 통신** | 태스크+댓글로 에이전트 간 소통 | 추적 가능한 협업 |
| **Session Persistence** | Heartbeat 간 세션 유지 | 작업 연속성 |

### 4.2 선택적으로 가져올 것 (SHOULD)

| 컴포넌트 | 이유 |
|----------|------|
| **requestDepth** | 위임 깊이 추적 — 무한 위임 방지 |
| **budgetIncidents** | 예산 사건 이력 관리 |
| **회사 내보내기/가져오기** | 템플릿 기반 조직 복제 |
| **Activity Log** | 모든 결정 감사 추적 |
| **Escalation Path** | Engineer → Manager → CEO → Board |

### 4.3 구체적 패턴

| 패턴 | 설명 |
|------|------|
| `executionLockedAt` | 체크아웃 시간 기록 → stale 판정 기준 |
| `pauseReason: 'budget'` | 예산 정지 vs 수동 정지 구분 |
| `paperclipWake` | 컨텍스트에 태스크/댓글 인라인 포함 |
| `sessionIdBefore/After` | Heartbeat 간 세션 ID 체인 |
| `evaluateCostEvent()` | 비용 기록 시 즉시 예산 평가 |

---

## 5. 우리가 안 가져올 것

| 컴포넌트 | 이유 |
|----------|------|
| **Express.js 서버** | GoClaw의 Go HTTP 서버 사용 |
| **React UI 전체** | 우리 자체 React UI |
| **Drizzle ORM** | Go에서 sqlx/pgx 사용 |
| **어댑터 시스템** | 우리 자체 에이전트 실행 레이어 |
| **Docker 설정** | 우리 자체 배포 전략 |
| **evals/** | Paperclip 전용 평가 |

---

## 6. Go 포팅 난이도

| 모듈 | 난이도 | 근거 |
|------|--------|------|
| 조직 구조 | **LOW** | DB 스키마 + CRUD — 언어 무관 |
| Atomic Checkout | **LOW** | SQL UPDATE WHERE — 직역 |
| Heartbeat | **MED** | 스케줄러 + 컨텍스트 생성 + 어댑터 실행. GoClaw 스케줄러와 통합 필요 |
| 예산 통제 | **LOW** | 비용 집계 + 임계값 비교 — 단순 로직 |
| 승인 시스템 | **LOW** | CRUD + 상태 전이 |
| 세션 지속성 | **LOW** | DB 저장/로드 |
| 회사 내보내기 | **MED** | 5,900줄 — 복잡한 직렬화 로직 |

**종합: LOW-MED** — 핵심 거버넌스 패턴은 단순하나 Heartbeat 통합이 관건

---

## 7. 다른 플랫폼과의 접점

```
Paperclip이 제공하는 것     →  통합 시 만나는 플랫폼
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

계층적 조직                →  GoClaw (멀티테넌트 에이전트 관리와 통합)

Atomic Checkout            →  GoClaw (DB 저장소 계층에서 구현)
                            →  claw-code (턴 루프에서 태스크 체크아웃)

Heartbeat                  →  GoClaw (레인 스케줄러와 통합)
                            →  Hermes (Heartbeat 사이에 메모리 리뷰)

예산 통제                  →  GoClaw (비용 추적 저장소)
                            →  claw-code (usage 추적과 연동)

승인 시스템                →  OpenClaw (채널로 승인 알림 전달)
                            →  GoClaw (WebSocket으로 Board에 실시간 알림)

태스크 기반 통신            →  Hermes (태스크 댓글 → 기술 학습 트리거)
                            →  AutoAgent (태스크 완료율 → 최적화 메트릭)

Session Persistence        →  claw-code (세션 관리와 통합)
                            →  Hermes (세션 메모리와 연동)
```

### 통합 순서

1. **Paperclip ↔ GoClaw** — 조직/태스크/예산 DB 스키마 + 스케줄러 통합 (핵심)
2. **Paperclip ↔ claw-code** — 턴 루프에서 태스크 체크아웃 + 비용 추적
3. **Paperclip ↔ OpenClaw** — 승인 알림을 채널로 전달
4. **Paperclip ↔ Hermes** — Heartbeat 기반 메모리/기술 동기화
5. **Paperclip ↔ AutoAgent** — 태스크 완료율 → 에이전트 최적화
