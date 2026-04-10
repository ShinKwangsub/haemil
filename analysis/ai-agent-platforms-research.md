# AI 에이전트 오픈소스 플랫폼 종합 리서치 보고서

> 조사일: 2026-04-10
> 목적: 통합 상용 AI 비즈니스 파트너 시스템 구축을 위한 오픈소스 에이전트 플랫폼 기능 벤치마킹
> 조사 대상: 24개 플랫폼

---

## 0. 코어 엔진 기반 — claw-code (Claude Code Rust 재구현)

### 0.1 개요
- **한줄:** Claude Code의 핵심 런타임을 Rust로 재구현한 오픈소스 프로젝트
- **런타임:** Rust
- **라이선스:** MIT ✅ 상용 가능
- **소스:** github.com/ultraworkers/claw-code
- **분석 완료:** reference/ 디렉토리에 핵심 소스 5개 보유

### 0.2 모듈별 분석 (총 ~8,000줄, 12개 모듈)

| 모듈 | 줄 수 | 핵심 패턴 | 코어 엔진 적용 |
|---|---|---|---|
| **bash_validation.rs** | 1,005 | 6단계 명령어 검증 파이프라인 (ReadOnly→Destructive→Mode→Sed→Path→Semantics) | 보안 레이어 — 에이전트 명령어 실행 전 검증 |
| **conversation.rs** | 900+ | 턴 루프 + max_iterations + 자동 압축 트리거 + 훅 시스템 | 코어 런타임 — 에이전트 실행 루프의 뼈대 |
| **compact.rs** | 500+ | 구조화된 세션 압축 (Scope/Tools/Pending/KeyFiles/CurrentWork) + 재귀 압축 | 메모리 관리 — 장기 세션 안정성 |
| **recovery_recipes.rs** | 632 | 7개 시나리오별 자동 복구 + 1회 시도 정책 + 에스컬레이션 | 자가 치유 — 장애 자동 복구 |
| **permissions.rs** | 552 | 5단계 권한 모드 (ReadOnly→WorkspaceWrite→DangerFullAccess→Prompt→Allow) + 훅 오버라이드 | 보안 레이어 — 에이전트 권한 관리 |
| **policy_engine.rs** | 582 | 선언적 규칙 평가 엔진 + 우선순위 + And/Or 조건 + 체이닝 액션 | 정책 엔진 — 하드코딩 대체 |
| **worker_boot.rs** | 900+ | 이벤트 기반 상태 머신 (Spawning→TrustRequired→Ready→Running→Finished/Failed) | 워커 관리 — 에이전트 라이프사이클 |
| **session.rs** | 800+ | JSONL 스트리밍 저장 + append-only + 로그 로테이션 (256KB, 최대 3 히스토리) | 데이터 레이어 — 크래시 복구 |
| **prompt.rs** | 800+ | 동적 시스템 프롬프트 조립 (CLAUDE.md + 도구 목록 + 메모리 + 컨텍스트) | 에이전트 런타임 — 프롬프트 빌더 |
| **sandbox.rs** | 386 | 컨테이너 감지 + 파일시스템 격리 모드 | 보안 레이어 — 실행 격리 |
| **mcp_tool_bridge.rs** | 500+ | MCP 서버 레지스트리 + 도구 자동 발견 | 도구 레이어 — MCP 통합 |
| **tools/lib.rs** | 400+ | 도구 매니페스트 + 등록 시스템 | 도구 레이어 — 확장 가능한 도구 관리 |

### 0.3 핵심 아키텍처 패턴

**1. 6단계 명령어 검증 파이프라인 (bash_validation.rs)**
```
명령어 입력 → commandSemantics(분류) → readOnlyValidation(읽기전용 차단)
  → destructiveCommandWarning(위험 경고) → modeValidation(권한 모드 검증)
  → sedValidation(sed -i 차단) → pathValidation(경로 탈출 감지)
  → ValidationResult: Allow | Block | Warn
```
- CommandIntent: ReadOnly, Write, Destructive, Network, ProcessManagement, PackageManagement, SystemAdmin, Unknown
- 상용 제품에서 에이전트의 모든 시스템 명령어를 사전 검증하는 핵심 보안 레이어

**2. ConversationRuntime 턴 루프 (conversation.rs)**
```
loop {
    iterations += 1;
    if iterations > max_iterations → RuntimeError("exceeded max iterations")
    
    API 호출 → 응답 파싱 → tool_uses 추출
    if no tool_uses → break (턴 종료)
    
    for each tool_use:
        PreToolUse 훅 실행 → 권한 검증 → 도구 실행 → PostToolUse 훅
    
    자동 압축 트리거 (100K 토큰 초과 시)
}
TurnSummary 반환 (iterations, usage, compaction 이벤트)
```
- 에이전트 런타임의 핵심 실행 루프
- max_iterations로 무한 루프 방지
- 훅 시스템으로 도구 실행 전후에 커스텀 로직 삽입 가능

**3. 구조화된 세션 압축 (compact.rs)**
```xml
<summary>
Conversation summary:
- Scope: 45 messages compacted (user=12, assistant=18, tool=15)
- Tools mentioned: Read, Write, Bash, Grep
- Recent user requests: [최근 3개]
- Pending work: [todo, next 키워드 추출]
- Key files referenced: [최대 8개 파일]
- Current work: [현재 작업]
- Key timeline: [역할:내용 축약]
</summary>
```
- 단순 truncation이 아닌 의미 기반 압축
- 재귀 압축 지원 (이전 summary + 새 summary 병합)
- 압축 후 "Resume directly — do not recap" 지시로 에이전트 혼란 방지

**4. 1회 복구 정책 (recovery_recipes.rs)**
```
FailureScenario → RecoveryRecipe(steps, maxAttempts=1, escalation)
  → RecoveryResult: Recovered | PartialRecovery | EscalationRequired
```
- 7개 시나리오별 구조화된 복구 단계
- 항상 1회만 시도 → 실패 시 즉시 에스컬레이션 (AlertHuman / Abort / LogAndContinue)
- 복구 이벤트 구조화 (Attempted / Succeeded / Failed / Escalated)

**5. 5단계 권한 모드 (permissions.rs)**
```
ReadOnly → WorkspaceWrite → DangerFullAccess → Prompt → Allow
```
- 도구별 필요 권한과 현재 모드 비교
- 훅에서 PermissionOverride (Allow/Deny/Ask) 가능
- PermissionRequest → PermissionPrompter → 사용자 승인/거부/세션 허용

### 0.4 코어 엔진에 녹일 핵심 기술

| # | 기술 | claw-code 출처 | 통합 시스템 적용 | 우선순위 |
|---|---|---|---|---|
| 1 | 6단계 명령어 검증 | bash_validation.rs | 보안 레이어 — 모든 에이전트 명령어 사전 검증 | **P0** |
| 2 | ConversationRuntime 턴 루프 | conversation.rs | 코어 — 에이전트 실행 루프 + max_iterations + 훅 | **P0** |
| 3 | 5단계 권한 모드 | permissions.rs | 보안 — 에이전트 권한 계층화 | **P0** |
| 4 | 구조화된 세션 압축 | compact.rs | 메모리 — 장기 세션 토큰 효율 | **P1** |
| 5 | 1회 복구 정책 | recovery_recipes.rs | 안정성 — 장애 자동 복구 + 에스컬레이션 | **P1** |
| 6 | 선언적 정책 엔진 | policy_engine.rs | 유연성 — if/else 하드코딩 대체 | **P1** |
| 7 | Worker Boot 상태 머신 | worker_boot.rs | 워커 관리 — 라이프사이클 제어 | **P2** |
| 8 | JSONL 세션 저장 | session.rs | 데이터 — append-only 크래시 복구 | **P2** |
| 9 | 동적 프롬프트 조립 | prompt.rs | 런타임 — 컨텍스트 기반 프롬프트 빌더 | **P2** |
| 10 | 샌드박스 격리 | sandbox.rs | 보안 — 컨테이너 감지 + 격리 | **P3** |
| 11 | MCP 도구 브릿지 | mcp_tool_bridge.rs | 도구 — 외부 도구 자동 발견/연결 | **P3** |
| 12 | 도구 매니페스트 | tools/lib.rs | 도구 — 확장 가능한 도구 등록 | **P3** |

### 0.5 왜 claw-code가 코어 엔진의 기반인가

1. **Rust로 작성** — Go로 포팅하기에 가장 자연스러운 시스템 언어 (GoClaw 코어와 동일 계열)
2. **Claude Code의 검증된 패턴** — 수백만 사용자가 검증한 아키텍처의 재구현
3. **MIT 라이선스** — 상용 제품에 자유롭게 사용 가능
4. **모듈화 수준 높음** — 각 모듈이 독립적으로 포팅/적용 가능
5. **보안 설계 우선** — 명령어 검증, 권한 모드, 샌드박스가 기본 내장

---

## 1. 기존 5개 플랫폼 (이미 보유/분석 완료)

### 1.1 Paperclip — AI 에이전트 오케스트레이션 플랫폼
- **런타임:** Node.js + 내장 PostgreSQL
- **라이선스:** 오픈소스 (셀프호스팅)
- **Stars:** 38,000+
- **독보적 강점:** 조직 구조(CEO→매니저→IC) + 거버넌스(승인/예산/롤백) + Atomic Checkout
- **추출할 것:** 하트비트 패턴, 조직 기반 오케스트레이션, 예산 관리

### 1.2 Hermes — 자기 학습형 AI 에이전트 프레임워크
- **런타임:** Python
- **라이선스:** 오픈소스 (Nous Research)
- **독보적 강점:** 자기 학습 루프 (경험→스킬 자동 생성→개선) — 23개 플랫폼 중 유일
- **추출할 것:** 영속 메모리(FTS5 + LLM 요약), MCP 지원, Skills Hub 표준

### 1.3 OpenClaw — AI 에이전트 게이트웨이
- **런타임:** Node.js
- **라이선스:** 오픈소스
- **Stars:** 68,000+
- **독보적 강점:** 24+ 메시징 채널 통합 — 대부분 1-2개만 지원하는 생태계에서 유일
- **추출할 것:** 멀티채널 라우팅, 자율 스킬 생성, 프로액티브 자동화

### 1.4 GoClaw — 엔터프라이즈급 AI 에이전트 플랫폼
- **런타임:** Go (단일 바이너리 ~25MB, 1초 미만 시작)
- **라이선스:** 오픈소스
- **독보적 강점:** Go 네이티브 동시성 + 5계층 보안 + RLS 멀티테넌트 — Python 편중 생태계에서 유일한 Go 코어
- **추출할 것:** 고성능 아키텍처, 엔터프라이즈 보안, 20+ LLM 프로바이더

### 1.5 SuperPowers — 에이전트 스킬 프레임워크
- **런타임:** 플러그인 (Claude Code/Cursor 등에 설치)
- **라이선스:** 오픈소스
- **Stars:** 121,000+
- **독보적 강점:** 사양 주도 개발(Spec-Driven) + 강제 TDD + 서브에이전트 주도 개발
- **추출할 것:** Brainstorm→Plan→Implement→Review 워크플로우, 87% 커버리지 품질 보증

---

## 2. 신규 조사 플랫폼 (18개)

### 2.1 Goose (by Block/Square)
- **한줄:** Block이 만든 로컬 AI 코딩 에이전트
- **런타임:** Rust (코어) + Python (확장)
- **라이선스:** Apache 2.0 ✅ 상용 가능
- **Stars:** ~15,000+
- **핵심 강점:**
  1. MCP 네이티브 지원 — 외부 도구를 플러그인처럼 연결
  2. 로컬 퍼스트 + 다중 LLM 지원
- **추출할 것:** MCP 기반 도구 통합 아키텍처

### 2.2 Bolt.new / Bolt.diy
- **한줄:** 브라우저에서 프롬프트로 풀스택 웹앱 즉시 생성/배포
- **런타임:** TypeScript / Node.js (WebContainers)
- **라이선스:** Bolt.diy MIT ✅ 상용 가능
- **Stars:** ~15,000+
- **핵심 강점:**
  1. WebContainers로 브라우저 내 풀스택 실행 — 서버 불필요
  2. 제로 설정 배포 (Netlify/Vercel 원클릭)
- **추출할 것:** 브라우저 내 런타임 + 실시간 프리뷰

### 2.3 SWE-Agent / Devon / Devika (Devin 대안)
- **한줄:** 오픈소스 자율 코딩 에이전트들
- **런타임:** Python
- **라이선스:** MIT / Apache 2.0 ✅ 상용 가능
- **Stars:** SWE-Agent ~15,000+, Devika ~18,000+
- **핵심 강점:**
  1. SWE-bench 벤치마크로 정량 측정/개선
  2. Agent-Computer Interface (ACI) — LLM이 코드를 효율적으로 탐색/편집
- **추출할 것:** ACI 패턴

### 2.4 AutoGPT / AutoGen
- **한줄:** 자율 AI 에이전트 원조 + Microsoft 다중 에이전트 대화 프레임워크
- **런타임:** Python
- **라이선스:** MIT ✅ 상용 가능
- **Stars:** AutoGPT ~170,000+ / AutoGen ~40,000+
- **핵심 강점:**
  1. AutoGPT — Forge + Benchmark 생태계
  2. AutoGen — 다중 에이전트 대화 패턴 (라운드로빈, 그룹챗, 계층적)
- **추출할 것:** AutoGen의 대화 패턴 엔진 — 에이전트 간 소통 아키텍처

### 2.5 CrewAI
- **한줄:** 역할 기반 다중 AI 에이전트 오케스트레이션
- **런타임:** Python
- **라이선스:** MIT ✅ 상용 가능
- **Stars:** ~25,000+
- **핵심 강점:**
  1. 역할/목표/배경 3요소 에이전트 정의 — 직관적 분업
  2. Sequential, Hierarchical, Consensus 프로세스 패턴
- **추출할 것:** 역할 기반 에이전트 정의 패턴

### 2.6 LangGraph
- **한줄:** 에이전트 워크플로우를 상태 기계(그래프)로 구축
- **런타임:** Python / TypeScript
- **라이선스:** MIT ✅ 상용 가능
- **Stars:** ~10,000+
- **핵심 강점:**
  1. 상태 기계 기반 워크플로우 — 노드+엣지 그래프로 조건부 분기/루프/병렬 실행
  2. 체크포인팅 + 시간 여행 — 어느 지점이든 상태 저장/복원
- **추출할 것:** 상태 그래프 + 체크포인팅 — Worker Boot 상태 머신에 직접 활용

### 2.7 Mastra
- **한줄:** TypeScript 네이티브 AI 에이전트 프레임워크
- **런타임:** TypeScript / Node.js
- **라이선스:** Apache 2.0 (확인 필요) ⚠️
- **Stars:** ~10,000+
- **핵심 강점:**
  1. TypeScript 퍼스트 — Python 중심 생태계에서 유일한 TS 1급 지원
  2. 에이전트 + 워크플로우 + RAG + 평가를 단일 프레임워크에서 제공
- **추출할 것:** TypeScript 에이전트 런타임 아키텍처

### 2.8 Cline
- **한줄:** VS Code 확장 자율 AI 코딩 에이전트
- **런타임:** TypeScript (VS Code Extension)
- **라이선스:** Apache 2.0 ✅ 상용 가능
- **Stars:** ~20,000+
- **핵심 강점:**
  1. Human-in-the-loop — 모든 파일 수정/명령 전 사용자 승인
  2. 내장 브라우저 제어 — 스크린샷 캡처, UI 테스트 자동화
- **추출할 것:** 단계별 승인 UX 패턴

### 2.9 Aider
- **한줄:** 터미널 AI 페어 프로그래밍 — Git 통합 핵심
- **런타임:** Python
- **라이선스:** Apache 2.0 ✅ 상용 가능
- **Stars:** ~25,000+
- **핵심 강점:**
  1. Git 네이티브 통합 — 모든 수정 자동 커밋, diff 기반 토큰 효율
  2. Repo Map — tree-sitter로 코드베이스 구조를 LLM에 효율적 전달
- **추출할 것:** Repo Map 기법 — 대규모 코드베이스 이해 핵심 기술

### 2.10 OpenHands (구 OpenDevin)
- **한줄:** AI 소프트웨어 개발자 — 샌드박스 내 자율 코딩/실행/디버깅
- **런타임:** Python + Docker
- **라이선스:** MIT ✅ 상용 가능
- **Stars:** ~45,000+
- **핵심 강점:**
  1. Docker 기반 샌드박스 — 모든 행동을 격리 컨테이너에서 실행
  2. 이벤트 스트림 아키텍처 — 모든 행동을 이벤트로 기록, 완전한 재현성
- **추출할 것:** 이벤트 스트림 + 샌드박스 격리

### 2.11 Manus / OpenManus
- **한줄:** 범용 자율 AI 에이전트 — 코딩/리서치/데이터 분석 자율 수행
- **런타임:** Python (추정)
- **라이선스:** Manus 비공개 ❌ / OpenManus MIT ✅
- **Stars:** OpenManus ~10,000+
- **핵심 강점:**
  1. 범용 태스크 자율 실행 — 코딩 한정 아님
  2. 가상 컴퓨터 환경에서 브라우저/터미널 직접 제어
- **추출할 것:** 범용 태스크 에이전트 패턴

### 2.12 AgentGPT
- **한줄:** 웹에서 목표 입력 → 하위 태스크 자동 분해/실행
- **런타임:** TypeScript (Next.js) + Python
- **라이선스:** GPL-3.0 ❌ 소스 공개 의무
- **Stars:** ~32,000+
- **핵심 강점:**
  1. 웹 UI 기반 에이전트 실행 — 비개발자도 사용 가능
  2. 자율 목표 분해
- **추출할 것:** 웹 UI 패턴 참고만 (GPL 주의, 코드 직접 사용 불가)

### 2.13 MetaGPT
- **한줄:** 소프트웨어 회사 시뮬레이션 — PM/아키텍트/엔지니어/QA 역할 분업
- **런타임:** Python
- **라이선스:** MIT ✅ 상용 가능
- **Stars:** ~48,000+
- **핵심 강점:**
  1. SOP 기반 에이전트 협업 — 실제 회사 워크플로우(PRD→설계→구현→테스트) 재현
  2. 구조화된 산출물 체인 — 각 에이전트가 다음 에이전트를 위한 표준 문서 생산
- **추출할 것:** SOP 기반 에이전트 파이프라인 — Paperclip 조직에 업무 흐름 부여

### 2.14 Composio
- **한줄:** AI 에이전트용 도구 통합 — 250개+ 외부 서비스 연결
- **런타임:** Python / TypeScript
- **라이선스:** ELv2 ⚠️ SaaS 재판매 불가, 자체 제품 내장은 가능
- **Stars:** ~15,000+
- **핵심 강점:**
  1. OAuth/인증 관리 자동화 — 250개+ 서비스 인증 자동 처리
  2. 프레임워크 무관 통합
- **추출할 것:** OAuth/인증 관리 레이어

### 2.15 Phidata (현 Agno)
- **한줄:** 멀티모달 AI 에이전트 빠른 구축 — 메모리/도구/팀 내장
- **런타임:** Python
- **라이선스:** MPL-2.0 ⚠️ 수정 파일만 공개 의무
- **Stars:** ~18,000+
- **핵심 강점:**
  1. 에이전트 팀 — 리더가 태스크 자동 위임
  2. Pydantic 기반 구조화된 출력
- **추출할 것:** 에이전트 팀 위임 패턴 + 구조화된 출력

### 2.16 AutoAgent
- **한줄:** AI가 에이전트를 스스로 설계하고 최적화하는 메타 에이전트
- **런타임:** Python
- **라이선스:** MIT ✅ 상용 가능
- **Stars:** ~2,400+
- **핵심 강점:**
  1. 자동 최적화 루프 — 메타 에이전트가 프롬프트/도구/로직을 자율 수정
  2. program.md 하나로 에이전트 생성 — 마크다운이 프로그래밍 언어
- **추출할 것:** 에이전트 자동 최적화(hill-climbing) + 단일 파일 아키텍처

### 2.17 Smolagents (by HuggingFace)
- **한줄:** HuggingFace 경량 에이전트 — 코드 실행 기반 도구 호출
- **런타임:** Python
- **라이선스:** Apache 2.0 ✅ 상용 가능
- **Stars:** ~15,000+
- **핵심 강점:** "코드 에이전트" 패턴 — JSON 대신 Python 코드로 도구 호출, 토큰 30% 절감
- **추출할 것:** 코드 기반 도구 호출 패턴

### 2.18 Browser Use
- **한줄:** AI 에이전트용 브라우저 자동화 라이브러리
- **런타임:** Python
- **라이선스:** MIT ✅ 상용 가능
- **Stars:** ~55,000+ (2025 최고 성장)
- **핵심 강점:** 어떤 에이전트 프레임워크와도 결합 가능한 브라우저 제어
- **추출할 것:** 범용 브라우저 자동화 레이어

### 추가 참고 플랫폼
- **Dify** — Apache 2.0, 60K+ stars, 비주얼 워크플로우 편집기 + RAG
- **n8n** — Fair-code (조건부), 55K+ stars, 400+ 통합 노드
- **Haystack** — Apache 2.0, RAG 특화 프로덕션 파이프라인
- **Taskweaver** — MIT, Microsoft, 데이터 분석 코드 자동 변환
- **PydanticAI** — MIT, 타입 안전 에이전트 프레임워크

---

## 3. 라이선스 분류 (상용화 관점)

### ✅ 자유롭게 사용 가능 (MIT / Apache 2.0)
AutoGPT, AutoGen, CrewAI, LangGraph, OpenHands, MetaGPT, Goose, Cline, Aider, Smolagents, Dify, Haystack, Bolt.diy, PydanticAI, AutoAgent, Browser Use, OpenManus, Taskweaver, SWE-Agent

### ⚠️ 조건부 사용 가능
- **Composio (ELv2)** — SaaS로 재판매 불가, 자체 제품 내장은 가능
- **Phidata/Agno (MPL-2.0)** — 수정한 파일만 공개 의무
- **n8n (Fair-code)** — 상용 사용 조건 있음
- **Mastra** — 라이선스 세부 확인 필요

### ❌ 사용 불가/주의
- **AgentGPT (GPL-3.0)** — 전체 소스 공개 의무
- **Manus** — 비공개

---

## 4. 통합 시스템에 적용할 핵심 기능 Top 12

| # | 기능 | 출처 | 적용 레이어 | 우선순위 |
|---|---|---|---|---|
| 1 | 이벤트 스트림 아키텍처 | OpenHands | 코어 | **P0** |
| 2 | SOP 기반 에이전트 파이프라인 | MetaGPT | 오케스트레이션 | **P0** |
| 3 | 상태 그래프 + 체크포인팅 | LangGraph | Worker 상태 머신 | **P1** |
| 4 | Repo Map (코드베이스 이해) | Aider | 에이전트 런타임 | **P1** |
| 5 | 역할/목표/배경 에이전트 정의 | CrewAI | 에이전트 설계 | **P1** |
| 6 | OAuth/인증 관리 자동화 | Composio | 게이트웨이 | **P1** |
| 7 | Docker 샌드박스 격리 | OpenHands | 보안 | **P2** |
| 8 | 다중 에이전트 대화 패턴 | AutoGen | 에이전트 소통 | **P2** |
| 9 | 브라우저 자동화 | Browser Use | 도구 레이어 | **P2** |
| 10 | 에이전트 자동 최적화 | AutoAgent | 메타 레이어 | **P2** |
| 11 | 비주얼 워크플로우 편집기 | Dify | 관리 대시보드 | **P3** |
| 12 | WebContainers 브라우저 런타임 | Bolt.diy | 프리뷰/데모 | **P3** |

---

## 5. 핵심 인사이트

### 5.1 우리가 이미 가진 독보적 강점
- **Hermes 자기 학습** — 23개 중 유일. 최대 차별화 요소
- **OpenClaw 24+ 채널** — 대부분 CLI/웹 1-2개만 지원
- **Paperclip 거버넌스** — 예산/승인/감사까지 하는 건 유일
- **GoClaw Go 코어** — Python 편중(23개 중 15개 Python) 생태계에서 성능 차별화

### 5.2 우리에게 빠진 것
- **이벤트 스트림** (OpenHands) — 행동 기록/재현 인프라
- **SOP 파이프라인** (MetaGPT) — 조직에 실제 업무 흐름
- **상태 그래프** (LangGraph) — 복잡한 워크플로우 관리
- **OAuth 통합** (Composio) — 외부 서비스 접근
- **브라우저 자동화** (Browser Use) — 웹 작업 자동화
- **에이전트 자동 최적화** (AutoAgent) — AI가 AI를 개선

### 5.3 시장 트렌드
- "조직 시뮬레이션" 패턴이 대세 (MetaGPT, CrewAI, Phidata)
- MIT/Apache 2.0만으로 충분한 생태계 구축 가능
- Python 편중이 심함 → Go 코어가 차별화 포인트
- 자기 학습 + 멀티채널 + 거버넌스 조합은 시장에 없음

---

## 6. 제안 통합 아키텍처

```
+---------------------------------------------------+
|           데스크탑 앱 (Go 바이너리)                 |
+---------------------------------------------------+
|  UI Layer                                          |
|  - React 대시보드 (Dify식 비주얼 편집기)             |
|  - 멀티채널 메시징 허브 (OpenClaw 24+채널)           |
|  - 모바일 앱 (React Native / Flutter)               |
+---------------------------------------------------+
|  Orchestration Layer                               |
|  - Paperclip식 조직 구조 + 거버넌스                  |
|  - MetaGPT식 SOP 파이프라인 (PRD→설계→구현→테스트)    |
|  - CrewAI식 역할/목표/배경 에이전트 정의              |
|  - AutoGen식 다중 에이전트 대화 패턴                  |
|  - 하트비트 스케줄러 + Atomic Checkout               |
+---------------------------------------------------+
|  Agent Runtime Layer (★ claw-code 코어)             |
|  - claw-code ConversationRuntime 턴 루프            |
|  - claw-code 선언적 정책 엔진 (policy_engine)        |
|  - claw-code Worker Boot 상태 머신                  |
|  - claw-code 구조화된 세션 압축 (compact)            |
|  - claw-code 1회 복구 정책 (recovery_recipes)        |
|  - claw-code 동적 프롬프트 조립 (prompt)             |
|  - Hermes식 자기 학습 엔진 (경험→스킬→개선)           |
|  - AutoAgent식 에이전트 자동 최적화                   |
|  - Aider식 Repo Map (코드베이스 이해)                |
|  - LangGraph식 상태 그래프 + 체크포인팅               |
|  - 영속 메모리 (FTS5 + 다중 프로바이더)               |
|  - MCP 프로토콜 지원 (claw-code mcp_tool_bridge)     |
+---------------------------------------------------+
|  Security Layer (★ claw-code 보안)                  |
|  - claw-code 6단계 명령어 검증 (bash_validation)     |
|  - claw-code 5단계 권한 모드 (permissions)           |
|  - claw-code 샌드박스 격리 (sandbox)                 |
|  - GoClaw 5계층 보안 + RLS 격리                      |
|  - Cline식 Human-in-the-loop 승인 UX                |
|  - Docker 샌드박스 격리 (OpenHands)                  |
+---------------------------------------------------+
|  Tool Layer                                        |
|  - claw-code 도구 매니페스트 + 등록 (tools/lib)      |
|  - Browser Use 브라우저 자동화                       |
|  - Composio식 OAuth/인증 관리 (250+ 서비스)           |
|  - SuperPowers식 사양 주도 개발 워크플로우             |
+---------------------------------------------------+
|  Gateway Layer                                     |
|  - GoClaw식 고성능 멀티테넌트 게이트웨이               |
|  - 20+ LLM 프로바이더 + 프롬프트 캐싱                |
|  - OpenClaw식 24+ 메시징 채널                        |
+---------------------------------------------------+
|  Data Layer                                        |
|  - 내장 PostgreSQL + RLS                            |
|  - claw-code JSONL 세션 저장 (session.rs)            |
|  - 이벤트 스트림 (OpenHands식)                       |
|  - 로컬 파일 시스템                                  |
+---------------------------------------------------+
|  Distribution                                      |
|  - 데스크탑: Go 바이너리 (macOS/Windows/Linux)       |
|  - 웹: SaaS 구독제                                  |
|  - 모바일: iOS/Android 앱                            |
+---------------------------------------------------+
```

### claw-code가 아키텍처에서 차지하는 위치
claw-code는 **Agent Runtime + Security + Data 3개 레이어의 핵심 기반**.
Claude Code라는 수백만 사용자 검증 제품의 아키텍처를 Rust로 재구현한 것이므로,
이를 Go로 포팅하여 코어 엔진의 뼈대로 사용하는 것이 가장 효율적.

---

## 7. 참고

- GitHub Stars 수치는 2025-2026 근사치
- 라이선스는 변경될 수 있으므로 실제 GitHub 레포에서 재확인 필요
- 기존 5개 플랫폼 분석: analysis/claw-code-*.md 참조
- 기술 조사 원본: 광섭 제공 (5개 플랫폼 비교 보고서)
