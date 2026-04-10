# Goose 상세 분석

> 분석일: 2026-04-10
> 분석 범위: Rust 전체 (562개 파일, 7개 크레이트)

---

## 1. 개요

| 항목 | 내용 |
|------|------|
| 한줄 설명 | MCP 네이티브 AI 에이전트 — 외부 도구를 MCP 프로토콜로 원활히 연결 |
| 언어 | Rust |
| 라이선스 | Apache-2.0 |
| 개발 | Agentic AI Foundation (AAIF, Linux Foundation 산하) |
| 역할 | 손 — MCP를 통한 외부 도구 연결 |
| 규모 | 562 Rust 파일, 7개 크레이트 |
| MCP 라이브러리 | rmcp 1.2.0 (schemars, auth 기능) |
| LLM 제공자 | 15+ |

---

## 2. 아키텍처

### 2.1 크레이트 구조

```
goose/
├── crates/
│   ├── goose/           # 메인 에이전트 엔진
│   ├── goose-mcp/       # 빌트인 MCP 서버 (4개)
│   ├── goose-cli/       # CLI 인터페이스
│   ├── goose-server/    # HTTP API 서버
│   ├── goose-sdk/       # Rust SDK (ACP 통신)
│   ├── goose-acp/       # Agent Client Protocol
│   └── goose-acp-macros/# ACP 매크로
```

### 2.2 6가지 확장 타입

```
1. Stdio         — 자식 프로세스 (stdin/stdout JSON-RPC)
2. StreamableHttp — MCP Streamable HTTP 프로토콜
3. Builtin       — In-process MCP 서버 (duplex 채널)
4. Platform      — 에이전트 프로세스 내 직접 실행
5. Frontend      — 프론트엔드 제공 도구
6. Inline Python — uvx를 통한 인라인 Python
```

### 2.3 에이전트 루프

```
reply(user_message, session_config)
  ↓
슬래시 커맨드 / Elicitation 응답 처리
  ↓
Provider.complete(model, system_prompt, messages, tools)
  ↓
도구 호출 추출 → 보안 검사 (SecurityInspector)
  ↓
사용자 승인 (ActionRequiredManager)
  ↓
dispatch_tool_call(tool_call, request_id, session)
  ↓
도구 결과 + 다시 LLM 호출 (또는 최종 출력)
  ↓
AgentEvent 스트림: Message, ToolCall, ToolResponse, HistoryReplaced
```

---

## 3. 핵심 소스 분석

### 3.1 McpClientTrait — MCP 클라이언트 추상화

```rust
pub trait McpClientTrait: Send + Sync {
    async fn list_tools(&self, session_id, next_cursor, cancel_token)
        -> Result<ListToolsResult, Error>;
    async fn call_tool(&self, ctx: &ToolCallContext, name, arguments, cancel_token)
        -> Result<CallToolResult, Error>;
    async fn list_resources(&self, session_id, next_cursor, cancel_token)
        -> Result<ListResourcesResult, Error>;
    async fn read_resource(&self, session_id, uri, cancel_token)
        -> Result<ReadResourceResult, Error>;
    async fn list_prompts(...) -> Result<ListPromptsResult, Error>;
    async fn get_prompt(...) -> Result<GetPromptResult, Error>;
}
```

### 3.2 McpClient 구현

```rust
pub struct McpClient {
    client: Mutex<RunningService<RoleClient, GooseClient>>,
    notification_subscribers: Arc<Mutex<Vec<mpsc::Sender<ServerNotification>>>>,
    server_info: Option<InitializeResult>,
    timeout: Duration,
    docker_container: Option<String>,
}
```

**연결 흐름:**
1. 트랜스포트 생성 (stdio / StreamableHttp / duplex)
2. GooseClient (ClientHandler) 초기화
3. RunningService<RoleClient, GooseClient> 래핑
4. 서버 정보 캡처, 알림 구독자 설정

### 3.3 세션 컨텍스트 자동 주입

```rust
fn inject_session_context_into_extensions(
    mut extensions: Extensions,
    session_id: Option<&str>,
    working_dir: Option<&str>,
) -> Extensions {
    // SESSION_ID_HEADER, WORKING_DIR_HEADER를
    // Extensions._meta에 자동 주입
    // → 모든 MCP 요청에 세션/작업디렉토리 전달
}
```

**패턴:** 모든 list_tools, call_tool, list_resources, read_resource에 세션 컨텍스트 자동 포함

### 3.4 ExtensionManager — 핵심 조율

```rust
pub struct ExtensionManager {
    extensions: Mutex<HashMap<String, Extension>>,
    context: PlatformExtensionContext,
    provider: SharedProvider,
    tools_cache: Mutex<Option<Arc<Vec<Tool>>>>,
    tools_cache_version: AtomicU64,
    client_name: String,
    capabilities: ExtensionManagerCapabilities,
}
```

**주요 메서드:**
- `add_extension()` — 확장 타입별 MCP 클라이언트 생성, Docker 지원, 환경 변수 검증
- `fetch_all_tools()` — 모든 확장에서 도구 병렬 수집, 페이지네이션, 충돌 감지
- `dispatch_tool_call()` — 도구명에서 확장 해석 → MCP call_tool 호출

### 3.5 도구명 네임스페이스

```
도구 공개명: "extension__toolname"
내부 매핑:  extension → client, toolname → 실제 도구명

메타데이터 주입:
  tool.meta["goose_extension"] = extension_name
```

### 3.6 에러 처리 및 취소

```rust
async fn await_response(handle, timeout, cancel_token) -> Result<ServerResult> {
    tokio::select! {
        result = receiver => { /* 정상 응답 */ }
        _ = sleep(timeout) => {
            send_cancel_message(..., "timed out").await;
            Err(ServiceError::Timeout{timeout})
        }
        _ = cancel_token.cancelled() => {
            send_cancel_message(..., "operation cancelled").await;
            Err(ServiceError::Cancelled { reason: None })
        }
    }
}
```

### 3.7 환경 변수 보안 (31개 차단)

차단 목록: PATH, PATHEXT, LD_LIBRARY_PATH, LD_PRELOAD, DYLD_*, PYTHONPATH, NODE_OPTIONS, RUBYOPT, CLASSPATH, APPINIT_DLLS, ComSpec, TEMP, TMP 등

### 3.8 빌트인 MCP 확장 (goose-mcp)

```rust
pub static BUILTIN_EXTENSIONS: Lazy<HashMap<&str, SpawnServerFn>> = Lazy::new(|| {
    HashMap::from([
        builtin!(autovisualiser, AutoVisualiserRouter),  // 차트/다이어그램
        builtin!(computercontroller, ComputerControllerServer),  // 파일/시스템
        builtin!(memory, MemoryServer),  // 장기 기억
        builtin!(tutorial, TutorialServer),  // 튜토리얼
    ])
});
```

In-process 통신: duplex 채널 (65536 버퍼)

### 3.9 리소스 관리

```rust
pub async fn read_resource_tool(&self, session_id, params, cancel_token)
    -> Result<Vec<Content>, ErrorData>
```

바이너리 처리: Base64 디코딩 → UTF-8 변환 시도 → 실패 시 `[Binary content (mime) - N bytes]`

---

## 4. 우리가 가져올 것

### 4.1 반드시 가져올 것 (MUST)

| 컴포넌트 | 가져올 패턴 | 이유 |
|----------|-------------|------|
| **McpClientTrait** | list_tools/call_tool/list_resources/read_resource 추상화 | MCP 통합의 핵심 인터페이스 |
| **세션 컨텍스트 주입** | Extensions._meta에 session_id/working_dir 자동 주입 | 멀티테넌트 MCP 지원 |
| **ExtensionManager** | 확장 등록/도구 수집/디스패치 패턴 | MCP 서버 관리 |
| **도구명 네임스페이스** | `extension__toolname` 매핑 + 충돌 감지 | 도구 이름 충돌 방지 |
| **취소 토큰** | CancellationToken 기반 타임아웃/취소 | 긴 작업 제어 |
| **환경 변수 차단** | 31개 위험 환경 변수 목록 | 보안 |
| **6가지 확장 타입** | Stdio/HTTP/Builtin/Platform/Frontend/InlinePython | 유연한 MCP 연결 |

### 4.2 선택적으로 가져올 것 (SHOULD)

| 컴포넌트 | 이유 |
|----------|------|
| **버전 기반 도구 캐시** | tools_cache_version — 성능 최적화 |
| **알림 구독** | ServerNotification 스트림 — 실시간 진행 |
| **Docker 내 builtin** | 컨테이너 격리된 MCP 서버 실행 |
| **페이지네이션** | cursor 기반 도구 목록 — 대규모 도구 지원 |
| **리소스 관리** | list_resources/read_resource — 파일/데이터 접근 |

### 4.3 구체적 패턴

| 패턴 | 설명 |
|------|------|
| `inject_session_context_into_extensions()` | 모든 MCP 요청에 세션 메타 주입 |
| `resolve_tool()` | 공개명 → (확장, 실제 도구명) 해석 |
| `TOOL_EXTENSION_META_KEY` | 도구에 소유 확장 메타 태깅 |
| `await_response() select!` | 타임아웃 + 취소 + 응답 3-way 경쟁 |
| `BLOCKED_ENV_VARS` | 환경 변수 보안 화이트리스트 |
| `builtin!()` 매크로 | duplex 채널로 in-process MCP 서버 |

---

## 5. 우리가 안 가져올 것

| 컴포넌트 | 이유 |
|----------|------|
| **goose-cli** | 우리 자체 UI |
| **goose-server** | GoClaw의 HTTP 서버 사용 |
| **goose-acp** | Agent Client Protocol — 현시점 불필요 |
| **ComputerController** | 스크린샷/마우스 제어 — 초기 불필요 |
| **AutoVisualiser** | 차트 렌더링 — 프론트엔드에서 별도 |
| **Tutorial** | Goose 전용 |

---

## 6. Go 포팅 난이도

| 모듈 | 난이도 | 근거 |
|------|--------|------|
| McpClientTrait | **MED** | Rust trait → Go interface. async → goroutine. rmcp 라이브러리 대응 Go MCP 라이브러리 필요 |
| 세션 컨텍스트 주입 | **LOW** | JSON 메타데이터 추가 — 언어 무관 |
| ExtensionManager | **MED** | Mutex<HashMap> → sync.RWMutex + map. 도구 캐시 + 버전 관리 |
| 도구 디스패치 | **LOW** | 문자열 파싱 + 라우팅 |
| 취소 토큰 | **LOW** | Go context.WithCancel() 직접 대응 |
| Stdio 트랜스포트 | **MED** | exec.Cmd + JSON-RPC — GoClaw에 이미 MCP 브릿지 존재 |
| 환경 변수 차단 | **LOW** | 문자열 목록 비교 |
| Builtin 확장 | **HIGH** | In-process MCP 서버를 Go로 — duplex 채널 패턴을 Go io.Pipe로 변환 |

**종합: MED** — 핵심 패턴은 이식 가능하나 MCP 라이브러리 선택과 in-process 서빙이 관건

---

## 7. 다른 플랫폼과의 접점

```
Goose가 제공하는 것        →  통합 시 만나는 플랫폼
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

MCP 클라이언트 추상화      →  claw-code (mcp_stdio.rs와 병합 — 더 성숙한 패턴)
                            →  GoClaw (Go MCP 브릿지에 Goose 패턴 적용)

세션 컨텍스트 주입          →  GoClaw (멀티테넌트 MCP — 테넌트별 도구 격리)

도구 네임스페이스           →  claw-code (mcp__{server}__{tool} 네이밍 통합)

확장 타입 (6가지)           →  OpenClaw (채널을 MCP 확장으로 노출 가능)

취소/타임아웃               →  GoClaw (context.WithTimeout 패턴과 통합)
                            →  Paperclip (Heartbeat 타임아웃과 연계)

환경 변수 보안              →  GoClaw (Input Guard와 보안 레이어 통합)

빌트인 MCP 서버             →  Hermes (메모리 MCP 서버 → 기술 통합)
```

### 통합 순서

1. **Goose ↔ GoClaw** — Go MCP 브릿지에 세션 컨텍스트 + 도구 네임스페이스 적용
2. **Goose ↔ claw-code** — MCP 클라이언트 패턴 병합 (Goose가 더 성숙)
3. **Goose ↔ OpenClaw** — 채널을 MCP 확장으로 노출
4. **Goose ↔ Hermes** — 메모리/기술을 MCP 리소스로 노출
5. **Goose ↔ Paperclip** — MCP 도구에 거버넌스 정책 적용
