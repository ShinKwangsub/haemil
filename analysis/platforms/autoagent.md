# AutoAgent 상세 분석

> 분석일: 2026-04-10
> 분석 범위: Python 전체 (101개 .py 파일)

---

## 1. 개요

| 항목 | 내용 |
|------|------|
| 한줄 설명 | 자연언어만으로 에이전트/도구/워크플로우를 자동 생성하고 실패에서 학습하는 메타 에이전트 |
| 언어 | Python |
| 라이선스 | Apache-2.0 |
| 역할 | 진화 — 에이전트가 스스로 개선 |
| 규모 | 101 Python 파일 |
| 핵심 특징 | Meta-Agent 패턴: 에이전트가 에이전트를 만든다 |
| 평가 벤치마크 | GAIA, MultiHopRAG, Math500 |

---

## 2. 아키텍처

### 2.1 자동 최적화 루프

```
사용자 입력 (자연언어)
    ↓
[Agent Former] — 에이전트 형태 생성 (XML)
    ↓
[Agent Creator] — 에이전트 생성 (Python 코드)
    ↓
[Tool Editor] — 필요한 도구 생성
    ↓
[Workflow Former] — 다중 에이전트 워크플로우 설계
    ↓
에이전트 시스템 실행
    ↓
평가 메트릭 수집 (GAIA 등)
    ↓
실패 시 자동 개선 (도구 추가, 전략 변경)
    ↓
[반복] 최대 3회
```

### 2.2 4계층 최적화

| 계층 | 메타 에이전트 | 역할 |
|------|-------------|------|
| 1 | Agent Former | 자연언어 → XML 에이전트 형태 |
| 2 | Agent Creator | XML → Python 코드 컴파일 + 등록 |
| 3 | Tool Editor | 기존 도구 검토 → 새 도구 생성/테스트 |
| 4 | Workflow Former | 다중 에이전트 상호작용 자동 설계 |

---

## 3. 핵심 소스 분석

### 3.1 Agent Former — 에이전트 형태 생성

**파일:** `autoagent/agents/meta_agent/agent_former.py`

자연언어 요청 → XML 형태:
```xml
<agents>
    <system_input>사용자의 질문</system_input>
    <agent>
        <name>Helper Agent</name>
        <instructions>너는 도움 에이전트다...</instructions>
        <tools category="existing">
            <tool><name>query_db</name></tool>
        </tools>
        <tools category="new">
            <tool><name>send_email</name></tool>
        </tools>
    </agent>
</agents>
```

단일 vs 다중 에이전트 시스템 자동 결정

### 3.2 Agent Creator — 코드 생성

**파일:** `autoagent/agents/meta_agent/agent_creator.py`

XML → Python 코드 자동 변환:
```python
@register_plugin_agent(name="Helper Agent", func_name="get_helper_agent")
def get_helper_agent(model: str):
    instructions = "너는 도움 에이전트다..."
    return Agent(
        name="Helper Agent",
        model=model,
        instructions=instructions,
        functions=[query_db, send_email]
    )
```

파일 시스템에 저장 → 데코레이터로 자동 등록

### 3.3 Tool Editor — 도구 자동 생성

**파일:** `autoagent/agents/meta_agent/tool_editor.py`

3가지 도구 통합 전략:
1. **API 기반**: `get_api_plugin_tools_doc()` → RapidAPI 등 자동 검색
2. **모델 기반**: `search_trending_models_on_huggingface()` → HF 모델 통합
3. **시각 분석**: `visual_question_answering` 도구 재사용

### 3.4 실패 주도 개선 (Failure-Driven Improvement)

```python
# main.py
async def run_in_client(agent, messages, context_variables, meta_agent):
    MAX_RETRY = 3
    for i in range(MAX_RETRY):
        response = await client.run_async(agent, messages, context_variables)

        if 'Case resolved' in response:
            break  # 성공
        elif 'Case not resolved' in response and i < 2:
            # 메타 에이전트로 도구 생성 → 재시도
            response = await client.run_async(meta_agent, messages, context_variables)
```

실패 감지 → Tool Editor 활성화 → 새 도구 생성 → 재시도 (최대 3회)

### 3.5 Registry 시스템

```python
class Registry:
    _registry = {
        "tools": {},           # 기본 도구
        "agents": {},          # 기본 에이전트
        "plugin_tools": {},    # 동적 생성 도구
        "plugin_agents": {},   # 동적 생성 에이전트
        "workflows": {}        # 동적 생성 워크플로우
    }
```

데코레이터 기반 자동 등록: `@register_plugin_agent`, `@register_plugin_tool`

### 3.6 평가 프레임워크

```python
# evaluation/utils.py
def run_evaluation(dataset, metadata, output_file, num_workers, process_instance_func):
    # 병렬 평가 실행
    # 각 문제에 대해 에이전트 실행
    # 정답 비교 → 메트릭 수집
    # JSON 결과 저장
```

지원 벤치마크: GAIA (복잡한 멀티스텝), MultiHopRAG (다중 홉 검색), Math500 (수학)

### 3.7 영속성

```
Runtime Memory (Registry) — 현재 세션
    ↓ 저장
File System (Python 파일) — 영구 보존
    ↓ import
다음 세션에서 재사용
```

---

## 4. 우리가 가져올 것

### 4.1 반드시 가져올 것 (MUST)

| 컴포넌트 | 가져올 패턴 | 이유 |
|----------|-------------|------|
| **Meta-Agent 패턴** | 에이전트가 에이전트를 만드는 구조 | 자동 조직 확장 |
| **실패 주도 개선** | Case not resolved → 도구 생성 → 재시도 | 자율 문제 해결 |
| **Registry 시스템** | plugin_tools/plugin_agents 동적 등록 | 런타임 확장 |
| **자동 도구 발견** | API 문서 검색 + HF 모델 검색 | 도구 생태계 자동 확장 |
| **평가 프레임워크** | 벤치마크 기반 에이전트 성능 측정 | 개선 효과 정량화 |

### 4.2 선택적으로 가져올 것 (SHOULD)

| 컴포넌트 | 이유 |
|----------|------|
| **Agent Former XML** | 에이전트 형태를 선언적으로 정의 |
| **Workflow Former** | 다중 에이전트 워크플로우 자동 설계 |
| **MAX_RETRY 루프** | 재시도 횟수 제한 (무한 루프 방지) |
| **도구 테스트 자동화** | 생성된 도구 즉시 검증 |

### 4.3 구체적 패턴

| 패턴 | 설명 |
|------|------|
| `Case not resolved` → 메타 에이전트 | 실패 감지 → 자동 도구 생성 |
| `@register_plugin_agent` | 데코레이터 기반 에이전트 자동 등록 |
| `get_api_plugin_tools_doc()` | 외부 API 문서 자동 수집 → 도구 구현 |
| `search_trending_models_on_huggingface()` | 최신 모델 자동 발견 → 통합 |
| `run_evaluation()` 병렬 평가 | num_workers 기반 벤치마크 실행 |

---

## 5. 우리가 안 가져올 것

| 컴포넌트 | 이유 |
|----------|------|
| **Python 코드 생성** | Go 기반이므로 코드 생성 방식 다름 |
| **Docker 실행 환경** | GoClaw의 샌드박스 사용 |
| **GAIA/MultiHopRAG 벤치마크** | 우리 자체 평가 기준 설계 |
| **Swarm 프레임워크 의존** | 자체 에이전트 프레임워크 사용 |
| **process_tool_docs.py** | 도구 문서 전처리 — 우리 방식으로 |

---

## 6. Go 포팅 난이도

| 모듈 | 난이도 | 근거 |
|------|--------|------|
| Meta-Agent 패턴 | **MED** | 개념은 언어 무관하나 에이전트 동적 생성을 Go에서 구현하려면 플러그인 시스템 필요 |
| 실패 주도 개선 | **LOW** | 재시도 루프 + 조건 분기 — 단순 |
| Registry | **LOW** | map + sync.RWMutex |
| API 도구 발견 | **MED** | HTTP 클라이언트 + 응답 파싱 — 언어별 차이 |
| 평가 프레임워크 | **MED** | 병렬 실행 + 결과 집계 — goroutine 활용 |
| 에이전트 코드 생성 | **HIGH** | Python exec() 대응이 Go에 없음 — 설정 기반 선언적 에이전트 생성으로 재설계 필요 |

**종합: MED** — 핵심 패턴(실패 주도 개선, Registry)은 쉬우나 동적 에이전트 생성을 Go 방식으로 재설계 필요

---

## 7. 다른 플랫폼과의 접점

```
AutoAgent가 제공하는 것     →  통합 시 만나는 플랫폼
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

실패 주도 개선              →  claw-code (recovery_recipes와 통합 — 실패 → 학습)
                             →  Hermes (기술 리뷰와 연계 — 실패 패턴 → 기술 생성)

Meta-Agent 패턴             →  Paperclip (CEO가 에이전트 채용 제안 → Auto 생성)
                             →  GoClaw (에이전트 라우터에 동적 등록)

자동 도구 발견              →  Goose (MCP 도구로 등록)
                             →  Hermes (기술 Hub에 자동 등록)

평가 프레임워크             →  Paperclip (태스크 완료율 → 예산 효율성)
                             →  GoClaw (Self-Evolution 메트릭과 통합)

에이전트 생성               →  Paperclip (조직 구조에 자동 배치)
                             →  GoClaw (프로바이더 + 모델 자동 선택)
```

### 통합 순서

1. **AutoAgent ↔ claw-code** — 실패 → 복구 레시피 → 학습 루프 (코어)
2. **AutoAgent ↔ Hermes** — 실패 패턴 → 기술 자동 생성 (학습)
3. **AutoAgent ↔ Paperclip** — 에이전트 자동 채용 제안 + 성과 평가 (관리)
4. **AutoAgent ↔ GoClaw** — 에이전트 동적 등록 + Self-Evolution 통합 (실행)
5. **AutoAgent ↔ Goose** — 자동 생성 도구를 MCP 서버로 등록 (도구)
