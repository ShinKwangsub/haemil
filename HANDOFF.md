# 프로젝트 핸드오프 문서

> 작성일: 2026-04-10
> 작성자: Claude Opus 4.6 + 광섭 (암제)
> 목적: 다음 에이전트가 작업을 연속성 있게 이어받기 위한 전체 맥락 문서

---

## 1. 프로젝트 개요

### 1.1 무엇을 하고 있는가

**OpenClaw Harness Project** — AI 에이전트 하네스(행동 규칙 시스템) 개선 프로젝트.
claw-code(Claude Code의 Rust 재구현체)의 검증된 패턴을 분석하여 OpenClaw에 적용하고,
궁극적으로 5개 오픈소스 플랫폼(Paperclip, Hermes, OpenClaw, GoClaw, SuperPowers)을
통합한 **AI 비즈니스 파트너 시스템**을 구축하는 것이 최종 목표.

### 1.2 사용자 정보

- **이름:** 광섭 (디스코드: 암제)
- **목표:** AI가 비즈니스 파트너 역할을 하는 사업 구축
- **스타일:** 반말 OK, 실행 중심, 빠른 진행 선호
- **환경:** macOS (Mac Studio), 로컬 LLM (oMLX + gemma4), Claude Code (Opus 4.6)

### 1.3 현재 인프라 구성

```
광섭 (사용자)
├── Claude Code (Opus 4.6) — 주력 개발 도구, 이 프로젝트의 메인
├── OpenClaw (윤슬) — 게이트웨이, 디스코드/텔레그램 봇
│   ├── 디스코드: OnAI 서버 / 모두의광장 채널
│   ├── 텔레그램: @Yunseul_MacStudio_bot
│   ├── 모델: gemma4 (oMLX 로컬)
│   └── 설정: ~/.openclaw/openclaw.json
├── Hermes — 자기 학습형 에이전트
│   ├── 디스코드: OnAI 서버 / 모두의광장 채널
│   ├── 모델: Claude Opus 4.6 (Anthropic API)
│   ├── 설정: ~/.hermes/config.yaml + ~/.hermes/.env
│   └── auto_thread: false (변경됨)
├── Paperclip — 에이전트 오케스트레이션
│   ├── API: http://127.0.0.1:3100
│   ├── CEO, CTO 에이전트 구성됨
│   ├── Hermes가 에이전트로 고용됨
│   └── 설정: ~/.openclaw/workspace/paperclip-*
├── oMLX — 로컬 LLM 서버
│   ├── API: http://127.0.0.1:8080
│   ├── 모델: gemma4
│   ├── SSD 캐시: ~100GB (한도 100GB, 자동 관리)
│   └── 설정: ~/.omlx/settings.json
└── 주요 프로젝트
    ├── ~/openclaw-harness-project/ (이 프로젝트)
    ├── ~/PJ/openclaw/ (OpenClaw 소스)
    └── ~/PJ/ (기타 프로젝트, Uahan 등)
```

---

## 2. 완료된 작업

### 2.1 Phase 0: claw-code 분석 (2026-04-06)

| 작업 | 산출물 | 위치 |
|---|---|---|
| claw-code 레포 클론 & 분석 | 12개 모듈 ~8,000줄 분석 | 분석 결과는 analysis/ |
| bash_validation.rs 분석 | 6단계 명령어 검증 파이프라인 | analysis/claw-code-harness-patterns.md |
| compact.rs 분석 | 세션 압축 패턴 | analysis/claw-code-compaction-patterns.md |
| recovery_recipes.rs 분석 | 7개 시나리오 복구 레시피 | analysis/claw-code-recovery-analysis.md |
| conversation.rs 분석 | 턴 루프, max_iterations | reference/conversation.rs |
| permissions.rs 분석 | 권한 모드 5단계 | reference/permissions.rs |
| AGENTS.md 업데이트 제안 | 검증/루프/정책 패턴 | analysis/agents-md-update-proposal.md |
| 압축 인식 제안 | compaction awareness | analysis/agents-md-compaction-proposal.md |

### 2.2 Phase 1: 하네스 텍스트 강화 (2026-04-08, 완료)

**커밋:** `d9d0480` — `feat(harness): enhance Phase 1 text rules for gemma4 compliance`

| 파일 | 추가된 내용 | 줄 수 |
|---|---|---|
| execution-rules.md | Forbidden Actions 4개, Anti-pattern 3개, File Operation Safety 섹션 | +29 |
| recording-rules.md | Append 실패 시나리오, 덮어쓰기 자가진단, 품질 기준 | +28 |
| failure-recovery.md | 트러블슈팅 레시피 3개 (raw 복구, tool loop, compaction) | +29 |
| worker-operations.md | Stuck detection 5패턴 테이블, 조기 경고 | +12 |

### 2.3 프로젝트 인프라 (2026-04-08)

| 작업 | 상태 |
|---|---|
| GitHub 레포 생성 (ShinKwangsub/openclaw-harness-project) | 완료 (public) |
| CLAUDE.md 작성 | 완료 |
| 상세 구현 계획서 v2 작성 | 완료 (plan/openclaw-detailed-implementation-plan.md) |
| Ultraplan 시도 | 레포 인식 문제로 실패 → 로컬에서 직접 실행 |

### 2.4 디스코드 설정 (2026-04-08)

| 작업 | 상태 | 비고 |
|---|---|---|
| OpenClaw(윤슬) 디스코드 연결 | 완료 | 서버:1491232585135816895, 채널:1491232898534215942 |
| Hermes 디스코드 설정 수정 | 완료 | auto_thread: false, require_mention: false |
| 양쪽 봇 ID 상호 허용 | 완료 | 서로의 allowed_users에 추가 |
| requireMention 양쪽 false | 완료 | |
| groupPolicy open | 완료 | OpenClaw 쪽 |

### 2.5 알려진 제한사항 (미해결)

| 문제 | 원인 | 상태 |
|---|---|---|
| 에이전트끼리 디스코드 대화 불가 | 봇 프레임워크가 `author.bot` 메시지 무시 (하드코딩) | 미해결 |
| Ultraplan 레포 자동 선택 | 마지막 사용 레포가 기본값으로 고정 | 웹에서 수동 선택으로 우회 |
| oMLX SSD 캐시 100GB 초과 | 자동 관리되나 간헐적 WARNING | 무시해도 됨 |

---

## 3. 미완료 작업 (다음 에이전트가 이어서 할 것)

### 3.1 Phase 2: 시스템 레벨 구현 (최우선)

상세 사양은 `plan/openclaw-detailed-implementation-plan.md` 참조.

| 태스크 | 우선순위 | 상태 | 예상 작업량 |
|---|---|---|---|
| Task 2.1: File Write Guard 훅 | **P0** | 미착수 | 150줄 + 100줄 테스트 |
| Task 2.2: Bash Validation 파이프라인 | **P1** | 미착수 | 400줄 + 250줄 테스트 |
| Task 2.3: Max Iterations 강제 | **P1** | 미착수 | 50줄 |
| Task 2.4: 세션 압축 구조화 | P2 | 미착수 | 300줄 + 150줄 테스트 |
| Task 2.5: Recovery Recipe 엔진 | P2 | 미착수 | 200줄 + 100줄 테스트 |

### 3.2 Phase 3: OpenClaw 오픈소스 기여

| PR | 제목 | 상태 |
|---|---|---|
| PR1 | docs: add structured AGENTS.md template | 미착수 |
| PR2 | feat(agents): add bash command validation | 미착수 |
| PR3 | feat(hooks): add file write guard hook | 미착수 |
| PR4 | feat(agents): enforce max iterations | 미착수 |
| PR5 | feat(sessions): structured session compaction | 미착수 |

### 3.3 통합 시스템 구축 (신규 — 최종 목표)

5개 플랫폼 통합 AI 비즈니스 파트너 시스템 구축:

```
취해야 할 핵심 장점:
├── Paperclip: 조직 구조 + 거버넌스 + 하트비트 + Atomic Checkout
├── Hermes: 자기 학습 루프 + 영속 메모리 + MCP + Skills Hub
├── OpenClaw: 24+ 메시징 채널 + 자율 스킬 생성
├── GoClaw: Go 고성능 + 5계층 보안 + RLS 멀티테넌트
└── SuperPowers: 사양 주도 개발 + 서브에이전트 + 강제 TDD
```

**통합 아키텍처 (제안됨):**
- 코어: Go 바이너리 (GoClaw 기반)
- 오케스트레이션: Paperclip식 조직 구조
- 에이전트 런타임: Hermes식 자기 학습
- 게이트웨이: OpenClaw식 멀티채널
- 개발 워크플로우: SuperPowers식 사양 주도

**사업 목적:** AI가 비즈니스 파트너 역할을 수행하는 서비스

### 3.4 에이전트 간 대화 문제 해결

디스코드에서 봇끼리 직접 대화가 안 되는 문제의 대안:
- Webhook 방식 (bot 플래그 우회)
- Paperclip 태스크 기반 협업 (이미 세팅됨)
- API 직접 연결
- 턴 제한 + 종료 토큰 방식의 제어된 대화

---

## 4. 주요 설정 파일 위치

| 파일 | 용도 |
|---|---|
| `~/.openclaw/openclaw.json` | OpenClaw 메인 설정 (게이트웨이, 채널, 모델) |
| `~/.openclaw/openclaw.json.bak` | 설정 백업 (2026-04-08) |
| `~/.hermes/config.yaml` | Hermes 메인 설정 |
| `~/.hermes/.env` | Hermes 환경변수 (토큰, API 키) |
| `~/.omlx/settings.json` | oMLX 서버 설정 |
| `~/.claude/settings.json` | Claude Code 설정 |
| `~/openclaw-harness-project/CLAUDE.md` | 이 프로젝트 컨텍스트 |
| `~/openclaw-harness-project/plan/` | 구현 계획서 |

---

## 5. 디스코드 ID 정리

| 항목 | ID |
|---|---|
| 서버 (OnAI) | `1491232585135816895` |
| 채널 (모두의광장) | `1491232898534215942` |
| 유저 (암제/광섭) | `664067553949253632` |
| 봇 (윤슬/OpenClaw) | `1491227951222620190` |
| 봇 (Hermes) | `1491231988701462718` |

---

## 6. 커밋 히스토리

```
d9d0480 feat(harness): enhance Phase 1 text rules for gemma4 compliance
8eaa682 Add CLAUDE.md and detailed implementation plan
9a5408a Initial commit: OpenClaw harness project
```

---

## 7. 다음 에이전트에게

### 즉시 해야 할 것
1. 이 문서와 `CLAUDE.md`를 먼저 읽어라
2. `plan/openclaw-detailed-implementation-plan.md`에서 Phase 2 태스크 사양 확인
3. Task 2.1 (File Write Guard)부터 착수 — raw log 덮어쓰기가 가장 시급

### 주의사항
- 광섭은 반말로 대화한다
- 실행 중심 — 계획만 세우지 말고 바로 실행해라
- 파일 수정 시 반드시 Read 먼저, Edit 사용 (Write로 덮어쓰지 마)
- git push 전 반드시 확인 받아라
- oMLX 재시작: `brew services restart omlx`
- OpenClaw 재시작: `openclaw gateway restart`
- Hermes 재시작: `hermes gateway restart`

### 장기 방향 → 해밀(Haemil) 프로젝트로 이관됨

통합 AI 비즈니스 파트너 시스템은 **`~/haemil/`** 프로젝트에서 별도 진행 중.
이 프로젝트(openclaw-harness-project)는 하네스 개선에 집중.

---

## 8. 해밀(Haemil) 프로젝트 — 2026-04-10 신설

**경로:** `~/haemil/`
**목적:** 7개 오픈소스 AI 에이전트 플랫폼 통합 상용 제품
**기술 스택:** Go + React + Tauri + React Native + PostgreSQL

### 7개 소스 플랫폼 (reference/에 전부 클론됨)
| 플랫폼 | 역할 | 소스 상태 |
|---|---|---|
| claw-code | 코어 엔진 뼈대 (78개 Rust 파일, 75,000줄) | ✅ 클론 완료 |
| Hermes | 자기 학습 + 메모리 | ✅ 클론 완료 |
| OpenClaw | 24+ 멀티채널 게이트웨이 | ✅ 클론 완료 |
| Paperclip | 조직 관리 + 거버넌스 | ✅ 클론 완료 |
| GoClaw | Go 고성능 + 보안 | ✅ 클론 완료 |
| Goose | MCP 네이티브 도구 통합 | ✅ 클론 완료 |
| AutoAgent | 에이전트 자동 최적화 | ✅ 클론 완료 |

### 현재 진행 상태
- ✅ 프로젝트 구조 생성, git init
- ✅ README.md, CLAUDE.md 작성
- ✅ 7개 소스 전부 reference/에 클론
- ✅ 기존 분석/설계 파일 이관
- ✅ 분석 방법론 확정 (crate→모듈→구조체→함수 + Claude Code 원본 대조)
- ⬜ Phase 1: 플랫폼별 상세 분석 (claw-code부터 시작)
- ⬜ Phase 2: 컴포넌트 추출 확정
- ⬜ Phase 3: 조립 설계도
- ⬜ Phase 4: 구현 로드맵

### 다음 작업
claw-code 78개 Rust 파일 한땀한땀 분석 시작.
Claude Code TypeScript 원본 분석서(wikidocs.net/338204)를 대조하면서 진행.

### 주요 파일
- `~/haemil/CLAUDE.md` — 해밀 프로젝트 컨텍스트
- `~/haemil/plan/platform-design.md` — 통합 설계서
- `~/haemil/analysis/ai-agent-platforms-research.md` — 24개 플랫폼 조사
- `~/haemil/analysis/platforms/` — 플랫폼별 상세 분석 (작성 예정)
