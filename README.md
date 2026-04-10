# 해밀 (Haemil)

> 순 우리말. AI 비즈니스 파트너 플랫폼.

7개 오픈소스 AI 에이전트 플랫폼의 핵심 장점을 하나의 제품으로 통합한다.

## 소스 플랫폼

| 플랫폼 | 역할 | 라이선스 |
|---|---|---|
| claw-code | 코어 엔진 뼈대 | MIT |
| Hermes | 자기 학습 + 메모리 | Open Source |
| OpenClaw | 24+ 멀티채널 게이트웨이 | Open Source |
| Paperclip | 조직 관리 + 거버넌스 | Open Source |
| GoClaw | Go 고성능 + 보안 | Open Source |
| Goose | MCP 네이티브 도구 통합 | Apache 2.0 |
| AutoAgent | 에이전트 자동 최적화 | MIT |

## 기술 스택

- **코어:** Go
- **웹 UI:** React
- **데스크탑:** Tauri (Go + React)
- **모바일:** React Native
- **DB:** PostgreSQL (내장)

## 프로젝트 구조

```
analysis/platforms/    — 플랫폼별 상세 분석 (7개)
analysis/integration/  — 통합 설계 (컴포넌트 추출, 인터페이스, 조립도)
plan/                  — 설계서 + 구현 로드맵
reference/             — 참조 소스 코드
```
