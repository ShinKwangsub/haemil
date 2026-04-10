# Hermes 상세 분석

> 분석일: 2026-04-10
> 분석 범위: Python 전체 (777개 .py 파일)

---

## 1. 개요

| 항목 | 내용 |
|------|------|
| 한줄 설명 | 자율 학습 루프 기반 AI 에이전트 — 경험에서 배우고 기술을 자동 생성 |
| 언어 | Python |
| 라이선스 | MIT |
| 개발 | Nous Research |
| 역할 | 두뇌 — 자기 학습, 영속 메모리, MCP 통합, Skills Hub |
| 규모 | 777 Python 파일, run_agent.py 9,857줄, 53개 도구, 8개 메모리 플러그인 |
| 지원 플랫폼 | CLI, Telegram, Discord, Slack, WhatsApp, Signal, Home Assistant |

---

## 2. 아키텍처

### 2.1 자율 학습 루프

```
사용자 메시지
    ↓
에이전트 루프 실행 (도구 호출 반복)
    ↓
최종 응답 전달
    ↓
[백그라운드] 리뷰 스레드 생성 (비동기)
    ├─ 메모리 리뷰 (10턴마다) → MEMORY.md / USER.md 갱신
    └─ 기술 리뷰 (도구 5회 반복 시) → 기술 생성/갱신
```

### 2.2 핵심 디렉토리

| 경로 | 역할 |
|------|------|
| `run_agent.py` (9,857줄) | 메인 에이전트 루프, 학습 트리거 |
| `cli.py` (9,185줄) | CLI 인터페이스 |
| `agent/` | 메모리 관리, 기술 유틸, 프롬프트 빌더, 컨텍스트 압축 |
| `tools/` (53파일) | 메모리, 기술 관리, 40+ 통합 도구 |
| `plugins/memory/` | 메모리 플러그인 (Honcho, Hindsight, Mem0 등 8개) |
| `gateway/` | Discord/Telegram/Slack 어댑터 |
| `hermes_state.py` | SQLite 세션 저장소 (FTS5 검색) |

### 2.3 메모리 아키텍처

```
Built-in Memory (항상 활성)
  ├─ ~/.hermes/MEMORY.md — 환경 사실, 도구 특성, 프로젝트 규칙
  └─ ~/.hermes/USER.md — 사용자 선호도, 통신 스타일

External Plugin (최대 1개 동시)
  ├─ Honcho — 변증법적 사용자 모델링
  ├─ Hindsight — 시맨틱 메모리
  ├─ Mem0 — AI 네이티브 메모리
  ├─ Holographic — 3D 벡터
  ├─ OpenViking — 의미론적 저장소
  ├─ SuperMemory — 다중 모드
  ├─ RetainDB — 구조화된 보존
  └─ ByteRover — 바이트 레벨 검색
```

---

## 3. 핵심 소스 분석

### 3.1 학습 트리거 메커니즘

**메모리 누지 (Memory Nudging):**
```python
# run_agent.py:7193-7200
if (self._memory_nudge_interval > 0
        and "memory" in self.valid_tool_names
        and self._memory_store):
    self._turns_since_memory += 1
    if self._turns_since_memory >= self._memory_nudge_interval:
        _should_review_memory = True
        self._turns_since_memory = 0
```
기본 10턴마다 트리거. 설정: `memory.nudge_interval`

**기술 누지 (Skill Nudging):**
```python
# run_agent.py:9573-9577
if (self._skill_nudge_interval > 0
        and self._iters_since_skill >= self._skill_nudge_interval):
    _should_review_skills = True
```
도구 반복 5회 이상 시 트리거. 설정: `skills.creation_nudge_interval`

### 3.2 백그라운드 리뷰 실행

```python
# 전용 포크 에이전트 생성
review_agent = AIAgent(
    model=self.model,
    max_iterations=8,
    quiet_mode=True,       # 사용자에게 출력 안 함
    platform=self.platform,
)
# 메모리/기술 저장소 공유, 누지 간격 0 (이중 리뷰 방지)
# 데몬 스레드로 비차단 실행
```

**메모리 리뷰 프롬프트:**
> "사용자가 자신에 대해 밝힌 것 (성격, 욕구, 선호, 개인 정보)과
> 작업 스타일/기대사항이 있었는가?"

**기술 리뷰 프롬프트:**
> "시행착오를 통한 비자명한 접근법이 사용되었는가?
> 기존 기술이 있으면 업데이트, 없으면 새로 생성."

### 3.3 메모리 제공자 인터페이스

```python
class MemoryProvider(ABC):
    def is_available(self) -> bool
    def initialize(self, session_id, **kwargs) -> None
    def prefetch(self, query, session_id="") -> str      # 턴 전 회상
    def queue_prefetch(self, query, session_id="") -> None # 백그라운드 회상
    def sync_turn(self, user_content, assistant_content, session_id="") -> None
    def get_tool_schemas(self) -> List[Dict]
```

**메모리 주입:**
```xml
<memory-context>
[System note: 회상된 메모리 — 새 사용자 입력이 아닌 배경 데이터]
[회상 내용]
</memory-context>
```

### 3.4 기술 시스템

**SKILL.md 포맷 (agentskills.io 표준):**
```yaml
---
name: skill-name          # 최대 64자
description: Brief desc   # 최대 1024자
version: 1.0.0
platforms: [macos, linux]
metadata:
  hermes:
    tags: [tag1]
    config:
      - key: wiki.path
        default: "~/wiki"
        prompt: 위키 경로
---
# 기술 내용 (절차적 지식)
```

**기술 도구 액션:**
- `create` — 새 기술 생성
- `edit` — 전체 수정
- `patch` — targeted find-and-replace
- `delete` — 삭제
- `write_file` / `remove_file` — 지원 파일 관리

### 3.5 세션 관리

**저장소:** SQLite (`~/.hermes/state.db`)
- `sessions` 테이블 — 세션 메타데이터
- `messages` 테이블 — 메시지 히스토리
- `messages_fts` — FTS5 전체 텍스트 검색

**컨텍스트 압축 (context_compressor.py):**
1. 압축 전 메모리 플러시
2. 메모리 제공자 on_pre_compress() 호출
3. 오래된 메시지 요약
4. 세션 분할 (부모-자식 연쇄)

### 3.6 MCP 서버 (mcp_serve.py)

Hermes를 MCP 클라이언트가 접근 가능한 서버로 노출:
- `conversations_list` — 대화 나열
- `messages_read` — 메시지 히스토리
- `messages_send` — 메시지 전송
- `permissions_respond` — 승인 응답

---

## 4. 우리가 가져올 것

### 4.1 반드시 가져올 것 (MUST)

| 컴포넌트 | 가져올 패턴 | 이유 |
|----------|-------------|------|
| **자율 학습 루프** | 백그라운드 리뷰 에이전트 + 누지 기반 트리거 | Haemil "두뇌"의 핵심 |
| **메모리 제공자 인터페이스** | MemoryProvider ABC (prefetch/sync_turn) | 플러그인 메모리 확장 |
| **기본 메모리** | MEMORY.md + USER.md 패턴 | 파일 기반 영속 메모리 |
| **기술 시스템** | SKILL.md 포맷 + 기술 생성/편집 도구 | 절차적 지식 축적 |
| **메모리 누지** | 턴 카운터 기반 자동 리뷰 | 사용자 개입 없는 학습 |
| **기술 누지** | 도구 반복 감지 → 기술 자동 생성 | 시행착오 → 재사용 가능 지식 |
| **FTS5 검색** | SQLite FTS5 기반 세션 검색 | 과거 대화 빠른 조회 |

### 4.2 선택적으로 가져올 것 (SHOULD)

| 컴포넌트 | 이유 |
|----------|------|
| **Honcho 플러그인** | 변증법적 사용자 모델링 — 깊은 사용자 이해 |
| **기술 설정 변수** | config.yaml 기반 기술 맞춤화 |
| **컨텍스트 압축** | 세션 분할 + 메모리 보존 |
| **기술 보안 검사** | skills_guard — 유해 패턴 감지 |

### 4.3 구체적 패턴

| 패턴 | 설명 |
|------|------|
| `_should_review_memory` | 턴 카운터 기반 메모리 리뷰 트리거 |
| `_should_review_skills` | 도구 반복 감지 기반 기술 리뷰 트리거 |
| `review_agent (fork)` | 별도 에이전트로 비차단 분석 |
| `build_memory_context_block()` | `<memory-context>` 태그로 시스템 프롬프트 주입 |
| `scan_skill_commands()` | 기술 디렉토리 스캔 → 슬래시 명령 매핑 |
| `extract_skill_config_vars()` | SKILL.md frontmatter에서 설정 변수 추출 |

---

## 5. 우리가 안 가져올 것

| 컴포넌트 | 이유 |
|----------|------|
| **run_agent.py 전체** (9,857줄) | 모놀리식 — Go로 모듈화 재설계 |
| **cli.py** (9,185줄) | CLI 불필요 (웹/데스크탑 UI 사용) |
| **Python 게이트웨이** | OpenClaw의 게이트웨이가 더 완성 |
| **8개 메모리 플러그인 전체** | 초기에 기본 메모리 + 1개 외부만 구현 |
| **anthropic_adapter.py** | GoClaw의 Go 프로바이더 어댑터 사용 |
| **batch_runner.py** | 평가 전용 |

---

## 6. Go 포팅 난이도

| 모듈 | 난이도 | 근거 |
|------|--------|------|
| 학습 루프 | **MED** | Python 비동기 스레드 → Go goroutine 변환. 로직 단순하나 리뷰 에이전트 fork 패턴 재설계 필요 |
| 메모리 제공자 | **LOW** | 인터페이스 → Go interface 직역 |
| 기본 메모리 | **LOW** | 파일 읽기/쓰기 — 언어 무관 |
| 기술 시스템 | **LOW** | YAML 파싱 + 파일 관리 — 단순 |
| 누지 메커니즘 | **LOW** | 카운터 + 조건 분기 |
| FTS5 검색 | **LOW** | SQLite FTS5는 Go에서도 동일 |
| 세션 압축 | **MED** | LLM 호출 + 세션 분할 로직 |
| 메모리 플러그인 | **MED** | 외부 API 연동 — 각 플러그인별 Go 클라이언트 필요 |

**종합: LOW-MED** — 핵심 학습 패턴은 단순하나 외부 플러그인 연동 시 작업량 증가

---

## 7. 다른 플랫폼과의 접점

```
Hermes가 제공하는 것       →  통합 시 만나는 플랫폼
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

자율 학습 루프             →  claw-code (대화 루프에 학습 스테이지 추가)
                            →  GoClaw (파이프라인 FinalizeStage에 학습)
                            →  AutoAgent (자동 최적화와 학습 루프 통합)

영속 메모리                →  GoClaw (멀티테넌트 메모리 저장소)
                            →  claw-code (세션 관리와 메모리 연동)

기술 시스템                →  OpenClaw (스킬 시스템과 통합)
                            →  Goose (MCP 도구를 기술로 캡처)

세션 검색 (FTS5)           →  GoClaw (PostgreSQL FTS/pgvector로 확장)

메모리 리뷰 에이전트       →  Paperclip (Heartbeat 사이에 메모리 리뷰)
                            →  AutoAgent (메트릭 기반 학습)
```

### 통합 순서

1. **Hermes ↔ claw-code** — 대화 루프에 학습 트리거 내장 (핵심)
2. **Hermes ↔ GoClaw** — 멀티테넌트 메모리 + FTS/pgvector (저장)
3. **Hermes ↔ AutoAgent** — 학습 + 최적화 통합 (진화)
4. **Hermes ↔ OpenClaw** — 기술 ↔ 스킬 통합 (도구)
5. **Hermes ↔ Paperclip** — Heartbeat 기반 메모리 동기화 (관리)
