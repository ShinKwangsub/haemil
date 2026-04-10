# OpenClaw 상세 분석

> 분석일: 2026-04-10
> 분석 범위: TypeScript 전체 (10,894개 TS 파일)

---

## 1. 개요

| 항목 | 내용 |
|------|------|
| 한줄 설명 | 24+ 메시징 채널을 통합하는 개인 AI 어시스턴트 게이트웨이 플랫폼 |
| 언어 | TypeScript (Node 22+) |
| 라이선스 | MIT |
| 역할 | 입과 귀 — 멀티채널 메시지 수신/발신 + 자율 스킬 생성 |
| 규모 | 10,894 TS 파일, 109개 플러그인, 55개 스킬 |

---

## 2. 아키텍처

### 2.1 핵심 계층 구조

```
Gateway (Control Plane) ──── WebSocket Protocol (버전 관리)
    ↓
Channel Manager (채널 생명주기 관리)
    ↓
Plugin Registry (Bundled + External 채널 로딩)
    ↓
Channel Adapters (Discord, Telegram, Slack, WhatsApp, Signal, iMessage, ...)
    ↓
Outbound Delivery (메시지 정규화 → 채널별 전송)
```

### 2.2 주요 디렉토리

| 경로 | 역할 |
|------|------|
| `src/gateway/` | 게이트웨이 서버, 프로토콜, 채널 관리자 |
| `src/channels/` | 채널 플러그인 타입, 레지스트리, 로더 |
| `src/routing/` | 메시지 라우팅 엔진, 계정 룩업 |
| `src/infra/outbound/` | 아웃바운드 전송 파이프라인 |
| `src/auto-reply/` | 자동 응답, 회신 디스패쳐 |
| `src/config/` | 설정 스키마 (260+ 파일) |
| `src/plugins/` | 플러그인 SDK, 타입 |
| `src/hooks/` | 이벤트 훅 시스템 |
| `extensions/` | 109개 플러그인 (채널, 프로바이더, 메모리 등) |
| `skills/` | 55개 에이전트 스킬 |

### 2.3 메시지 흐름 (Discord DM 예시)

```
① Discord WebSocket → 채널 어댑터 수신
② 메시지 정규화 (senderId, text, ChatType: "direct")
③ 라우팅 결정 (resolveAgentRoute → sessionKey, agentId)
④ 게이트웨이 → 에이전트 처리 (AI 응답 생성 + 스킬 실행)
⑤ ReplyPayload 정규화 → 채널 핸들러 전달
⑥ sendText() / sendMedia() 실행 → 전송 완료
⑦ message.sent 훅 실행, 메시지 ID 저장
```

---

## 3. 핵심 소스 분석

### 3.1 게이트웨이 프로토콜

**파일**: `src/gateway/protocol/index.ts` (26,993줄)

Typed wire protocol로 클라이언트/노드 간 통신:
- `ChatEvent` — 채팅 이벤트 스트림
- `ChatSendParams`, `ChatInjectParams` — 메시지 전송/주입
- `ChannelsStatusParams` — 채널 상태 조회
- `AgentEvent`, `AgentIdentityParams` — 에이전트 생명주기
- `PROTOCOL_VERSION` — 버전 기반 호환성 관리

### 3.2 채널 플러그인 계약

**파일**: `src/channels/plugins/types.plugin.ts` (128줄)

```typescript
type ChannelPlugin<ResolvedAccount = any> = {
  id: ChannelId;
  meta: ChannelMeta;
  capabilities: ChannelCapabilities;
  config: ChannelConfigAdapter<ResolvedAccount>;
  gateway?: ChannelGatewayAdapter<ResolvedAccount>;
  outbound?: ChannelOutboundAdapter;
  messaging?: ChannelMessagingAdapter;
  threading?: ChannelThreadingAdapter;
  streaming?: ChannelStreamingAdapter;
  // 40+ 추가 어댑터...
};
```

**Capabilities 선언:**
```typescript
type ChannelCapabilities = {
  supportsDm, supportsGroup, supportsMedia,
  supportsMarkdown, supportsThreads, supportsReactions,
  supportsEdits, supportsDeletes, supportsPolls, supportsVoice
};
```

### 3.3 채널 레지스트리

**파일**: `src/channels/plugins/registry.ts`

2단계 로딩: 번들 채널 → 외부 플러그인
```typescript
function getChannelPlugin(id: ChannelId): ChannelPlugin | undefined {
  return getLoadedChannelPlugin(id) ?? getBundledChannelPlugin(id);
}
```

캐시 구조:
```typescript
type CachedChannelPlugins = {
  registryVersion: number;
  sorted: ChannelPlugin[];        // 순서 보장
  byId: Map<string, ChannelPlugin>; // 빠른 조회
};
```

### 3.4 메시지 라우팅 엔진

**파일**: `src/routing/resolve-route.ts`

입력:
```typescript
type ResolveAgentRouteInput = {
  cfg: OpenClawConfig;
  channel: string;        // 채널 식별자
  accountId?: string;     // 채널 계정
  peer?: RoutePeer;       // 발신자 (direct/group/channel)
  parentPeer?: RoutePeer; // 스레드 상위
  guildId?: string;       // Discord 길드
  teamId?: string;        // Slack 워크스페이스
  memberRoleIds?: string[];
};
```

출력:
```typescript
type ResolvedAgentRoute = {
  agentId: string;
  channel: string;
  accountId: string;
  sessionKey: string;       // 세션 지속성 키
  mainSessionKey: string;   // DM 병합 키
  matchedBy: "binding.peer" | "binding.guild+roles" | "binding.guild" | "binding.channel" | "default";
};
```

**라우팅 우선순위**: `binding.peer` > `binding.guild+roles` > `binding.guild` > `binding.channel` > `default`

### 3.5 아웃바운드 전송

**파일**: `src/infra/outbound/deliver.ts`

채널별 핸들러:
```typescript
type ChannelHandler = {
  chunker: Chunker | null;          // 텍스트 분할
  textChunkLimit?: number;          // 메시지 크기 제한
  supportsMedia: boolean;
  sendText: (text) => Promise<OutboundDeliveryResult>;
  sendMedia: (caption, url) => Promise<OutboundDeliveryResult>;
  sanitizeText?: (payload) => string;
  normalizePayload?: (payload) => ReplyPayload | null;
};
```

인간다운 지연:
```typescript
const DEFAULT_HUMAN_DELAY_MIN_MS = 800;
const DEFAULT_HUMAN_DELAY_MAX_MS = 2500;
```

### 3.6 채널 매니저 & 생명주기

**파일**: `src/gateway/server-channels.ts` (23,154줄)

```typescript
type ChannelManager = {
  getRuntimeSnapshot: () => ChannelRuntimeSnapshot;
  startChannels: () => Promise<void>;
  stopChannel: (channelId) => Promise<void>;
  restartChannel: (channelId) => Promise<void>;
};
```

재시작 정책: 지수 백오프 (5초 → 최대 5분, 최대 10회)

상태 추적:
```typescript
type ChannelAccountSnapshot = {
  accountId, name, enabled, configured, linked,
  running, connected, restartPending,
  reconnectAttempts, lastConnectedAt,
  lastDisconnect: { at, status, error, loggedOut },
  lastMessageAt, lastEventAt, lastError,
  healthState, mode, dmPolicy, allowFrom, tokenSource
};
```

### 3.7 회신 디스패쳐

**파일**: `src/auto-reply/reply/reply-dispatcher.ts`

3가지 전송 유형:
```typescript
type ReplyDispatchKind = "tool" | "block" | "final";
// tool: 도구 실행 결과
// block: 스트리밍 블록 (중간 응답)
// final: 최종 응답
```

### 3.8 다중 계정 구성

단일 채널에 여러 봇 계정 독립 관리:
```yaml
channels:
  discord:
    "bot-main":
      token: "{{ .secrets.DISCORD_BOT_TOKEN }}"
      enabled: true
    "bot-staging":
      token: "{{ .secrets.DISCORD_BOT_TOKEN_2 }}"
      enabled: false
```

### 3.9 스킬 시스템

**위치**: `skills/` (55개 스킬)

스킬 팩토리 패턴:
```typescript
type ChannelAgentToolFactory = (params: {
  cfg?: OpenClawConfig
}) => ChannelAgentTool[];
```

주요 스킬: healthcheck, discord, spotify-player, gifgrep, coding-agent, session-logs, oracle

### 3.10 플러그인 분류

| 유형 | 설명 | 예시 |
|------|------|------|
| 채널 | 메시징 채널 어댑터 | discord, telegram, slack, zalo, tlon |
| 프로바이더 | AI 모델 제공자 | anthropic, openai, openrouter |
| 메모리 | 컨텍스트 엔진 | memory-lancedb |
| 기술 | 도구/기능 확장 | device-pair |

---

## 4. 우리가 가져올 것

### 4.1 반드시 가져올 것 (MUST)

| 컴포넌트 | 원본 | 가져올 패턴 | 이유 |
|----------|------|-------------|------|
| **ChannelPlugin 계약** | types.plugin.ts | 통일된 채널 인터페이스 (capabilities 선언 포함) | 멀티채널 확장의 핵심 |
| **채널 레지스트리** | registry.ts | 2단계 로딩 (번들 + 외부), 캐시 | 동적 채널 추가/제거 |
| **라우팅 엔진** | resolve-route.ts | 계층적 바인딩 (peer > guild > channel > default) | 에이전트-채널 매핑 |
| **아웃바운드 파이프라인** | deliver.ts | 메시지 정규화 → 채널별 핸들러 → 전송 | 통일된 발신 |
| **채널 생명주기** | server-channels.ts | 상태 추적 (connected/disconnected), 지수 백오프 재시작 | 안정적 연결 관리 |
| **메시지 타입 정규화** | chat-type.ts | ChatType (direct/group/channel) 3분류 | 채널 간 통일된 메시지 모델 |

### 4.2 선택적으로 가져올 것 (SHOULD)

| 컴포넌트 | 이유 |
|----------|------|
| **회신 디스패쳐** | 스트리밍 응답 (block) + 최종 응답 분리 패턴 |
| **인간다운 지연** | 봇 느낌 방지 (800ms~2500ms) |
| **프로토콜 버전 관리** | 클라이언트-서버 호환성 유지 |
| **다중 계정 관리** | 채널당 여러 봇 인스턴스 독립 운영 |
| **훅 시스템** | message.sent, channel.connected 이벤트 확장 |

### 4.3 구체적 패턴

| 패턴 | 설명 |
|------|------|
| `resolveAgentRoute()` | 5단계 우선순위 라우팅 |
| `ChannelCapabilities` | 채널별 기능 선언 (DM/그룹/미디어/스레드/리액션 등) |
| `OutboundDeliveryResult` | 전송 결과 정규화 (messageId, chatId, timestamp, meta) |
| `ChannelAccountSnapshot` | 연결 상태 + 건강 상태 + 마지막 이벤트 시간 추적 |
| `ReplyDispatchKind` | tool/block/final 3단계 응답 분류 |

---

## 5. 우리가 안 가져올 것

| 컴포넌트 | 이유 |
|----------|------|
| **게이트웨이 프로토콜 전체** (26,993줄) | 너무 거대 — Go로 경량화된 프로토콜 재설계 |
| **Jiti 동적 로딩** | TypeScript 전용 — Go 플러그인 시스템으로 대체 |
| **109개 플러그인 전체** | 초기에 Discord + Telegram + Slack 3개만 구현 |
| **55개 스킬 전체** | 우리 자체 스킬 시스템 (Hermes 기반) 사용 |
| **260+ 설정 파일** | Go 구조체 기반 설정으로 단순화 |
| **auto-reply 전체** | 우리 대화 루프 (claw-code 기반)에서 처리 |

---

## 6. Go 포팅 난이도

| 모듈 | 난이도 | 근거 |
|------|--------|------|
| ChannelPlugin 인터페이스 | **LOW** | Go interface로 직역 가능 |
| 채널 레지스트리 | **LOW** | map + sync.RWMutex로 충분 |
| 라우팅 엔진 | **LOW** | 조건 분기 로직 — 언어 무관 |
| 채널 어댑터 (Discord) | **MED** | discordgo 라이브러리 사용, WebSocket 관리 |
| 채널 어댑터 (Telegram) | **LOW** | telebot 라이브러리 성숙함 |
| 아웃바운드 파이프라인 | **LOW** | 메시지 변환 + 전송 — 단순 |
| 채널 생명주기 관리 | **MED** | goroutine + context.Cancel 패턴 |
| 프로토콜 버전 관리 | **LOW** | protobuf 또는 JSON 스키마 |
| 다중 계정 관리 | **MED** | 계정별 goroutine 격리 필요 |

**종합: LOW-MED** — 핵심 패턴은 단순하나 채널 어댑터별 SDK 학습 필요

---

## 7. 다른 플랫폼과의 접점

```
OpenClaw가 제공하는 것        →  통합 시 만나는 플랫폼
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

채널 게이트웨이              →  claw-code (대화 루프가 채널 메시지를 처리)
                              →  GoClaw (Go 네이티브 채널 어댑터)

라우팅 엔진                  →  Paperclip (에이전트 할당 — 어떤 에이전트가 응답할지)
                              →  Hermes (에이전트별 메모리 — sessionKey 기반)

스킬 시스템                  →  Hermes (Skills Hub와 통합)
                              →  Goose (MCP 도구를 스킬로 노출)

채널 상태 관리               →  Paperclip (하트비트 — 채널 건강 모니터링)
                              →  AutoAgent (채널 성능 자동 최적화)

메시지 정규화                →  claw-code (ContentBlock 타입과 매핑)
```

### 통합 순서

1. **claw-code ↔ OpenClaw** — 대화 루프에 채널 입력 연결 (필수)
2. **GoClaw ↔ OpenClaw** — Go 네이티브 채널 어댑터 구현
3. **Paperclip ↔ OpenClaw** — 에이전트 라우팅 + 채널 모니터링
4. **Hermes ↔ OpenClaw** — 채널별 세션 메모리
5. **Goose ↔ OpenClaw** — MCP 도구를 채널 스킬로 노출
