# 해밀 (Haemil) — AI 비즈니스 파트너 플랫폼

## 프로젝트 개요
7개 오픈소스 AI 에이전트 플랫폼(claw-code, Hermes, OpenClaw, Paperclip, GoClaw, Goose, AutoAgent)의
핵심 장점을 통합하여 상용 AI 비즈니스 파트너 제품을 만드는 프로젝트.

## 현재 단계
Phase 1: 각 플랫폼 상세 소스 분석 ✅ 완료 (2026-04-10)
Phase 2: 컴포넌트 추출 확정 + 인터페이스 설계 (다음)

## 기술 스택 (확정)
- 코어 엔진: Go
- 웹 UI: React
- 데스크탑: Tauri (Go + React)
- 모바일: React Native
- DB: PostgreSQL (RLS 멀티테넌트)
- 세션 저장: JSONL (append-only)

## 디렉토리 구조
- `analysis/platforms/` — 7개 플랫폼 상세 분석 (동일 템플릿, 전부 완료)
  - claw-code.md (451줄), goclaw.md (323줄), hermes.md (282줄)
  - goose.md (298줄), paperclip.md (319줄), openclaw.md (351줄), autoagent.md (256줄)
- `analysis/integration/` — 컴포넌트 추출, 인터페이스 설계, 조립 설계도 (작성 예정)
- `analysis/` — 기존 패턴 분석 (하네스, 압축, 복구)
- `plan/` — 설계서 + 구현 로드맵
- `reference/` — 7개 플랫폼 소스 전부 클론됨
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
