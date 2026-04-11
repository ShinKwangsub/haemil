# 해밀 (Haemil)

> 순 우리말. AI 비즈니스 파트너 플랫폼.

7개 오픈소스 AI 에이전트 플랫폼의 핵심 장점을 하나의 제품으로 통합한다.

## 현재 상태

- **Phase 1** (플랫폼별 분석) ✅ 완료 — `analysis/platforms/` 아래 7개 상세 분석 문서
- **Phase 2** (통합 엔진 뼈대) 🚧 진행 중
  - Phase 2a (스텁) ✅ 완료 — 컴파일 가능한 Go 스켈레톤 + 테스트 + 설계 문서
  - Phase 2b (본문) 🔜 다음 세션 — SSE 파싱, bash 실행, 턴 루프, REPL
- **Phase 3** (컴포넌트 추출 + 통합) — 예정

자세한 설계는 `analysis/integration/skeleton.md` 참조.

## 빌드 & 실행

```bash
# 요구사항: Go 1.23+
go version

# 빌드
go build ./...

# 테스트 (5개: Message/Bash/Provider/Redact/Skeleton Skip)
go test ./...

# 실행 (현재는 "skeleton ready" 메시지만 출력하고 종료)
go run ./cmd/haemil

# API 키 있을 때
ANTHROPIC_API_KEY=sk-ant-... go run ./cmd/haemil
```

## 소스 플랫폼

| 플랫폼 | 역할 | 언어 | 라이선스 |
|---|---|---|---|
| claw-code | 코어 엔진 뼈대 | Rust | MIT |
| Hermes | 자기 학습 + 메모리 | Python | MIT |
| OpenClaw | 24+ 멀티채널 게이트웨이 | TypeScript | MIT |
| Paperclip | 조직 관리 + 거버넌스 | TypeScript | MIT |
| GoClaw | Go 고성능 + 보안 | Go | MIT |
| Goose | MCP 네이티브 도구 통합 | Rust | Apache-2.0 |
| AutoAgent | 에이전트 자동 최적화 | Python | Apache-2.0 |

각 플랫폼별 분석은 `analysis/platforms/<name>.md` 에 있다.

## 기술 스택 (확정)

- **코어:** Go 1.23
- **웹 UI:** React
- **데스크탑:** Tauri (Go + React)
- **모바일:** React Native
- **DB:** PostgreSQL (RLS 멀티테넌트)
- **세션 저장:** JSONL (append-only)

## 프로젝트 구조

```
haemil/
├── cmd/haemil/               # CLI 엔트리포인트
├── internal/
│   ├── runtime/              # 도메인 타입 + Provider/Tool 인터페이스 + 세션 + 대화 루프
│   ├── provider/             # Anthropic 등 LLM 프로바이더 구현
│   ├── tools/                # bash 등 도구 구현
│   └── cli/                  # REPL 조립 + 입력 루프
├── analysis/
│   ├── platforms/            # 7개 플랫폼 상세 분석 (Phase 1)
│   └── integration/          # 통합 설계 문서 (Phase 2+)
│       ├── skeleton.md       # Phase 2 뼈대 설계서 (가장 중요)
│       └── multi-agent-communication.md  # Phase 3 멀티 에이전트 통신 설계
├── plan/                     # 설계서 + 구현 로드맵
└── reference/                # 참조 소스 (gitignore됨, 로컬 전용, ~1.6GB)
```

### 디렉토리 트리 빠르게 보기 (macOS)

```bash
# tree 없어도 find로 충분
find . -type d -not -path '*/reference/*' -not -path '*/.git/*' | sort
```

## 관련 프로젝트

- `~/openclaw-harness-project/` — OpenClaw 하네스 개선 (별도 진행)
