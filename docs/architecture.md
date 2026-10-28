# 架构说明（阶段1 · 供后续阶段使用）

本文件是全项目的架构基线，后续阶段在此之上增量开发，不推翻本设计。

## 1. 总体拓扑

```
┌────────────── 浏览器(免下载, 响应式) ──────────────┐
│  Vue3 + TS + Vite + Tailwind + Pinia               │
│  ┌─────────┐ ┌─────────┐ ┌──────────────────────┐ │
│  │ UI 层    │ │ 状态层   │ │ 传输适配层(三种联机)   │ │
│  │(App.vue  │ │(Pinia   │ │ OnlineAdapter(WS)    │ │
│  │ 阶段3+)  │ │ stores) │ │ P2PAdapter(阶段6)    │ │
│  └─────────┘ └─────────┘ │ LANAdapter(阶段6)     │ │
│        ↑ 仅消费投影        └──────────────────────┘ │
└──────────────┬────────────────────────────────────┘
               │ 统一协议信封 (v=1, seq, ref, type, payload)
┌──────────────▼────────────────────────────────────┐
│  Go 服务器(1核1GB 目标)                              │
│  httpapi(REST+WS) → room.Registry(权威状态)          │
│       │                 │ 事件日志(单调 seq)          │
│       │                 └→ 按 guestID 裁剪投影        │
│  store(SQLite+迁移) ← 房间元数据/游客身份/令牌哈希     │
│  ratelimit(令牌桶) ← 每连接消息频率限制               │
└───────────────────────────────────────────────────┘
```

## 2. 模块边界（M1~M10）

| 模块 | 位置 | 职责 | 禁止 |
|---|---|---|---|
| M1 协议 | `server/internal/protocol` + `web/src/protocol` | 信封、消息类型、视图模型；前后端各一份、线缆稳定 | 业务逻辑 |
| M2 领域模型 | `server/internal/game` | 牌(34种×4=136 TileID)、牌墙、座位风位、动作、规则配置、主题枚举 | 任何 IO |
| M3 规则引擎 | `server/internal/game`(阶段2扩展) | 洗牌、判定(吃碰杠胡)、状态机转移 | 传输/持久化 |
| M4 房间/会话 | `server/internal/room` | 权威房间、座位分配、事件日志(seq)、投影裁剪、空房清扫 | 知道 WebSocket 存在 |
| M5 传输 | `server/internal/httpapi` | REST、WS 升级、握手认证、限流、广播 | 麻将规则 |
| M6 持久化 | `server/internal/store` | SQLite 迁移、游客、令牌哈希、房间元数据 | 内存对局状态 |
| M7 前端状态 | `web/src/stores` | identity(游客身份持久化)、connection(会话镜像) | 规则判定 |
| M8 前端 UI | `web/src/App.vue`(阶段3扩展为牌桌) | 渲染与交互 | 判定权威数据 |
| M9 AI 接口 | 阶段4预留于 `game` 包 | `type AIPlayer interface { Decide(view) Action }` | 直改权威状态 |
| M10 主题 | `game.Theme` 枚举 + 阶段3 CSS 令牌 | classic / ink | — |

**核心原则**：游戏逻辑与渲染分离（M2/M3 永不 import UI）；规则引擎与传输层分离（M3 不 import M4/M5）。

## 3. 服务端权威状态机

### 3.1 权威与投影

- **唯一权威**：`room.Room`（持锁变更）。所有敏感判定（发牌、牌墙、他人手牌、可行动作）只发生在服务端。
- **投影规则**（`ViewFor(guestID)`）：
  - 他人 → `PlayerView{guestId, nickname, seat, connected, tileCount}` —— **永无 hand 字段**（编译期由结构体保证）。
  - 自己 → 额外 `YouView{seat, hand}`（仅下发本人连接）。
  - 大厅阶段 `hand` 为空数组；对局开始后（阶段2）为本人手牌代码列表。
- **牌墙**：`game.Wall` 只存在于服务端内存，任何投影/事件/持久化都不含牌墙内容。

### 3.2 事件序列号与断线重连

- 每房间维护单调递增 `seq`（int64，从 1 起）。所有状态变更（加入/离开/断线/对局事件）都追加进房间事件日志。
- **重连令牌**：游客创建时生成 64 hex 随机令牌，**仅下发一次**；库中只存 SHA-256 哈希，验证用常数时间比较。
- **重连流程**：
  1. 客户端 `POST /api/v1/reconnect` 验证令牌（恢复身份）；
  2. 新 WS `guest.hello` 握手 → 服务端 `FindByGuest` 找回房间会话、标记 connected=true 并广播；
  3. 客户端发 `sync.resync{lastSeq}` → 服务端回放 `EventsSince(lastSeq)` + 最新投影（带请求 ref 标识完成）。
- **快速重连安全**：hub 以 `removeIf` 仅当注册连接仍是旧实例时才注销，新连接不背旧 teardown 干扰。
- 房主迁移（阶段7）将复用令牌与会话找回机制。

## 4. 协议（v=1）

信封：`{"v":1,"seq":N?,"ref":"x"?,"type":"...","payload":{...}}`

| 方向 | type | payload | 说明 |
|---|---|---|---|
| C→S | `guest.hello` | `{guestId, token}` | 握手认证，首条消息，15s 超时 |
| C→S | `ping` | — | 保活（前端 30s 心跳） |
| C→S | `sync.resync` | `{lastSeq}` | 增量同步 |
| C→S | `room.create` | `{ruleSet?, theme?}` | 建房（房主座 0） |
| C→S | `room.join` | `{code}` | 按邀请码/房间ID加入（服务端规范化大写） |
| C→S | `room.leave` | — | 离房 |
| C→S | `game.action` | 阶段2定义 | 出牌/吃碰杠胡 |
| S→C | `hello` | `{guestId, nickname}` | 握手成功 |
| S→C | `pong` | `{ts}` | 保活回复 |
| S→C | `room.state` | `RoomStateView` | 状态投影（变化时广播各人视角） |
| S→C | `game.event` | 阶段2定义 | 对局事件 |
| S→C | `error` | `{code, message}` | 错误（回显请求 ref） |

**错误码**（稳定，勿改）：`rate_limited / auth_failed / bad_message / bad_payload / expected_hello / room_full / room_started / already_in_room / not_in_room / room_not_found / room_gone / max_online_rooms / game_not_started / unknown_type`。

## 5. 游客身份与账号预留

- 游客：昵称（1~24 可见字符，去首尾空白，拒控制字符）+ 随机 ID + 一次性令牌。
- 前端 localStorage `majiang.identity.v1` 持久化；启动时 `restore()` 走 `/api/v1/reconnect` 验证，失效则清除重注册。
- **账号系统预留**（阶段8+）：接口形态不变——`identity` store 的 `restore/register` 可替换为账号登录实现；协议新增 `account.*` 消息族即可，不动信封结构。

## 6. 三种联机适配

| 方式 | 权威方 | 传输 | 服务端角色 | 阶段 |
|---|---|---|---|---|
| 在线 | 服务器 | WS(本阶段已实现) | 全权威 | 5 |
| WebRTC P2P | 房主端模拟权威 | DataChannel + STUN/TURN | 仅初始信令交换（不计入在线房间上限） | 6 |
| 局域网 | 房主端模拟权威 | WebRTC over mDNS 或手动 SDP 交换 | 无（完全离线可用） | 6 |

**浏览器限制（已设计规避）**：浏览器无法监听 TCP/UDP，故 LAN 模式不含本地信令服务器；设备发现用 mDNS 候选，失败则走手动复制粘贴 SDP/邀请码交换。前端 `connection` store 的操作面（connect/send/handleEvent）就是统一适配接口，阶段6 为 P2P/LAN 提供同签名的实现。

## 7. 配置

默认值 → `config.json`（`-config` 参数）→ 环境变量（`MAJIANG_*`），统一校验。

| 项 | 默认 | 说明 |
|---|---|---|
| `http.listen` | 127.0.0.1:8080 | 监听地址 |
| `http.allowed_origins` | localhost:5173 ×2 | CORS 白名单（协议+主机+端口，禁路径） |
| `database.path` | data/majiang.db | SQLite(WAL, 单连接) |
| `rooms.max_online` | **3** | 在线房间上限 |
| `rooms.idle_ttl_seconds` | 900 | 空房清理时间 |
| `rooms.sweep_interval_seconds` | 60 | 扫描间隔 |
| `limits.max_message_bytes` | 65536 | 单消息上限 |
| `limits.message_burst` | 30 | 令牌桶容量 |
| `limits.messages_per_second` | 10 | 每秒补充 |
| `diy.dir` | data/diy | DIY 牌面图片目录（`MAJIANG_DIY_DIR`） |
| `diy.max_image_bytes` | 2097152 | 单张自定义牌面上限（≥65536） |
| `diy.password` | 空 | 首次启动固化为库内哈希，之后只在库里改（`MAJIANG_DIY_PASSWORD`） |

空房清理：周期扫描 + 空房即时回收（离房后房间无人立即删）+ 启动时清理停机期间过期元数据。

## 8. 关键不变量（后续阶段必须维护）

1. 他人手牌内容永不离开服务端（结构体字段级保证）。
2. 事件 seq 单调递增、房间内连续；重连同步基于 seq 增量。
3. 令牌只在创建时下发一次，库中仅存哈希。
4. 在线房间数（不含 P2P/LAN）≤ `max_online`。
5. 所有判定（包括 P2P 模式的房主端模拟权威）由单一权威执行，客户端 UI 永远只是投影消费者。
6. 协议信封与既有 type/错误码向后兼容（新增不修改）。
7. **响应窗口只对"有真实选择"的座位开放**：`AvailableActions` 只返回能吃/碰/杠/胡的座位（外加 `pass`），其余座位由 `resetResponseLocked` 自动标记已答；任何新增动作都必须同时给出"该座位是否真需要决策"的判据，否则会重新引入"逐家点过"。
8. **响应仲裁只有一个出口**：`settleLocked()` 是唯一决定"谁的动作成立"的地方（优先级 `priWin=3 > priMeld=2 > priChow=1`，同级按离出牌者座次，`RobWin/RobMeld=false` 时退化为先按者得），任何新响应动作都必须走 claim 提交流程。
9. **规则字段向后兼容**：`room.create` 的 `ruleSet` 按字段合并（`httpapi/rules_patch.go` 全指针结构），不得整体替换 `DefaultRuleSet()`，否则新增规则字段会被旧客户端清零。
10. **计时由引擎持有**：`Match.respDeadlines` / `discardDeadline` 是唯一时间源，`Tick(now)` 是唯一的超时推进入口（服务端 `room.AutoStep` 与 WASM `tick` 都调它）；房间层不再自己判超时。
11. **番数与得分只算一次**：`resolveWin` 计算后写入 `Match.winFan/winPay/winFlags`，`view.go` 的 `fanOf/payOf` 只读这些字段，不得在视图层重算（阶段 11 加时序番时踩过：两处各算一遍会导致结算面板与实际扣分不一致）。
12. **时序番互斥**：抢杠胡 / 杠上开花 / 海底捞月 / 河底捞鱼 同时只计最高的一个（`resolveWin` 里的 `if/else if` 链），新增时序番必须插进同一条链，不能叠加。
13. **抢杠窗口不是普通响应窗口**：`pendingDiscard.robKong=true` 时其他家只能胡，且 `ForceDraw` 必须拒绝（该窗口没有"摸牌"语义）；关闭响应倒计时也要给它兜底秒数（`resetResponseLocked` 里 `sec <= 0 → 5`），否则会永久挂起。
14. **DIY 牌面是服务端共享状态**：图片存在 `MAJIANG_DIY_DIR`、元数据在 `diy_tiles`，所有选择 DIY 主题的玩家看到同一套；客户端只在启动时 `diy.refresh()` 一次，设置页保存后本地 `applyTile` 增量更新。图片名由服务端生成（纳秒时间戳 + 内容嗅探扩展名），**不得**使用客户端提供的文件名。

## 9. 前端牌桌与双主题架构（阶段3）

### 9.1 双主题系统
- **经典传统主题 (`classic`)**：使用基于 Public Domain 的矢量麻将图（riichi-mahjong-tiles），提供完整 34 种牌面、牌背及牌底，适度阴影及立体质感。
- **明文水墨主题 (`ink`)**：牌面采用优雅考究的中文书法大字，万/条/饼/字均有明确中文全称（如“二条”牌面显示“二”与“条”），纯 SVG/CSS 渲染，高对比度，保障无障碍识读。
- **DIY 主题 (`diy`)**（阶段 11）：以经典图案为模板，每张牌可用自定义图片替换并选填充方式（`cover`/`contain`/`fill`/`scale-down`/`none`）；数据存服务端、全员共享，未自定义的牌自动回退经典图案。详见 §14.3。
- **牌值与视觉分离**：`web/src/utils/tiles.ts` 提供完整的分类、颜色、点数和 `aria-label` 无障碍元数据；牌组件 `MahjongTile.vue` 支持 xs/sm/md/lg/xl 尺寸及横竖摆放。

### 9.2 主题优先级（严格四级裁定）
```
房间强制主题 (最高优先级，权威房间配置)
   ↓ (未配置时)
玩家个人主题 (本地存储 majiang.theme.preference)
   ↓ (未配置时)
房间默认主题 (room.theme)
   ↓ (未配置时)
系统默认主题 (classic)
```
- **强制机制**：房间强制期间，玩家本地个人偏好保留但不生效；房间解除强制或退房后，玩家个人偏好立即自动恢复生效。
- **无感切换**：Pinia 响应式驱动，主题切换即时作用于全桌手牌、牌河与副露，无需重启对局。
- **动画开关**：支持一键开关摸打与出牌过渡动画（`majiang.animations.enabled`），低性能设备与高无障碍要求友好。

### 9.3 响应式 2D/伪3D 牌桌组件
- `MahjongTable.vue`：四方环绕式牌桌，自适应 PC/平板/手机（横屏/竖屏）。
- `TableCenter.vue`：中心方位指示盘、当前行动指示灯条、剩余牌墙计数、回合阶段提示。
- `PlayerRiver.vue`：标准 6 张一行牌河，最新弃牌带动态光环高亮提示。
- `PlayerMelds.vue`：吃、碰、明杠、暗杠（两端覆背）规范副露展示。
- `ActionControls.vue`：摸牌、打牌、吃、碰、杠、自摸、和牌、过操作栏，支持吃牌多组搭子弹窗选择。
- `GameResultModal.vue`：对局结算弹窗，清晰展示自摸/点炮、番数、单家分值及四家得分损益明细。
- `TestDriverModal.vue`：具备明确标识的本地测试驱动工具，可一键装配 5 种典型规则与极值对局场景，供单机牌桌与样式验收。

## 10. 单机模式与 AI(阶段4)

### 10.1 为什么用 Go→WASM

单机对局需要"权威规则"而**不能**重写一份 TS 规则(会出现双实现分叉)。因此把 `server/internal/game`(规则引擎)、`server/internal/ai`、`server/internal/local`(本地对局驱动)编译为
`GOOS=js GOARCH=wasm` 产物 `web/public/engine/engine.wasm`, 与联机服务器**同一份 Go 代码**。

带来的副产品: 阶段6 的 P2P/LAN 需要"权威主机是某个浏览器", 该能力在此已具备。

- 构建: `pnpm run engine`(调用 `scripts/build-wasm.mjs`, 自动定位 Go 工具链, 可 `GO_BIN` 覆盖)
- 产物不入库(约 4.8 MB), 开发/构建前需执行一次
- 桥接: `web/src/engine/engine.ts` 加载 `wasm_exec.js` + `engine.wasm`, 复用 `syscall/js` 导出的
  `mjEngine` 对象, 所有调用收/发 JSON 字符串, 统一 `{ok,error}` 信封

### 10.2 分层

```
web/src/stores/localGame.ts    UI 状态与回合调度(真人等待/超时/托管)
        ↕  JSON
web/src/engine/engine.ts       进程内单例, 封装 wasm 调用
        ↕  syscall/js
server/cmd/wasm/main.go        导出 newMatch/act/aiStep/view/state
server/internal/local/table.go 本地对局: 引擎 + AI 驱动 + 事件日志
server/internal/game/*         唯一权威规则(与联机同一份)
server/internal/ai/*           三档 AI(简单/普通/困难)
```

### 10.3 AI 设计要点

- **信息边界**: `ai.Request` 只注入该座位自己的 `game.SeatView`(他人手牌只有张数), 与真人看到的完全一致, 结构上无法偷看。
- **有界计算**: 打牌枚举"能降低向听的牌"再对最优的至多 6 个候选算进张, 单次决策为常数级枚举, 无搜索树、无云端调用。
- **难度差异**: 简单=孤张/字牌启发式且随机漏鸣牌; 普通=向听+进张; 困难=在向听/进张之外叠加危险度(现物/字牌/幺九/中张)与威胁局面判断。
- **随机性**: 由调用方注入 `game.RandomSource`, 测试用固定种子可复现。

### 10.4 真人等待与托管

`local.Table` 只在"轮到真人且存在可选动作"时停下等输入; 其余时间由 `Advance()` 连续推进 AI。
前端 `localGame` store 每 300ms 轮询 `state()/view()`, 真人可选动作超时(默认 20s)自动 `SetAuto`, 点按钮可接管。

### 10.5 验证

- `go test ./internal/ai/ ./internal/local/ ./internal/game/`: 向听/进张、AI 决策合法性、100 局全 AI 长时间稳定性、非法动作拒绝、2/3/4 人开局。
- `pnpm run test:wasm`(`scripts/e2e-wasm.mjs`): 直接对真实 wasm 跑全 AI 对局、真人+3AI、2 人局、视图隐私。
- 前端单测与 `vue-tsc` 覆盖 UI 层。

### 10.6 已知限制(阶段4)

- 番型仅: 基础 + 七对 + 自摸 + 门清(未含十三幺/清混一色/碰碰胡/抢杠/杠上开花/海底等)。
- 2 人对局的计分沿用四人公式(自摸三家设计算), 属于"简化规则", UI 已标注人数为体验参数。
- 音效为 WebAudio 合成, 无音频素材文件。
- 单机引擎 wasm 首次加载约 5 MB(浏览器缓存后不再请求)。

## 11. 在线房间、实时同步与邀请(阶段5)

### 11.1 房间生命周期

```
创建(公开/私有, 可选密码) → 大厅(座位/准备/机器人/改名/邀请) → 开局(规则锁定) → 对局 → 结算
                                                    ↘ 解散(房主) / 空房超时清理
```

- 座位按 `RuleSet.Players` 决定容量, **开局仅支持 4 人**: 非 4 人规则返回 `unsupported_players`, 不会静默按 4 人开局。
- 开局前所有人(机器人除外)必须 `seat.ready`; 开局即 `rulesLocked = true`, 此后座位/规则/机器人/房间配置一律拒绝(`rules_locked`), 避免中途改规则影响公平。
- 邀请码为 6 位大写字母数字(去除易混字符), 服务端统一 `ToUpper(TrimSpace)` 规范化。
- 解散: 房主 `room.dissolve` 后房间移出注册表, **房内所有连接**都会收到 `room.state {roomId:"", status:"dissolved"}` 并清空房间归属。

### 11.2 密码与邀请

- 房间密码与邀请密码均用 PBKDF2-HMAC-SHA256(120000 轮, 随机盐)存储于 `rooms.password_hash` / `invites.password_hash`; 只下发 `hasPassword` 布尔值, 明文永不回传。
- 邀请令牌 = 32 字节 `crypto/rand` → base64url 明文**仅在创建时下发一次**, 库中存 SHA-256 哈希。
- 邀请可设: 有效期(秒, 0=永久)、最大使用次数(0=不限)、独立密码、是否免房间密码(`bypassRoomPassword`)。
- 校验顺序: 令牌存在 → 未撤销 → 未过期 → 次数未用尽 → (若设了邀请密码)密码正确 → 才占用一次使用额度。**用尽/撤销/过期后不会回退为"凭房间密码进入"**, 否则次数限制可被绕过。
- 邀请入口 `invite.join`: 必须同时给出令牌与邀请码; 免密令牌仍需房间存在且未开局满员。

### 11.3 实时同步与幂等

- **单一序列空间**: 房间状态事件与对局事件共用 `Room.ReserveSeq()` 的单调计数下发, 客户端看到的 `seq` 在一条连接内严格递增(早期两条独立计数器会导致 seq 回退, 已修复)。
- **动作幂等**: `game.action` 携带客户端自增 `actionSeq`; 服务端 `ApplyActionWithSeq` 记录每游客水位, 重复请求不再执行, 改为回一条 `game.event {kind:"resync", view}` 让客户端校准。
- **断线重连**: 客户端重连后 `sync.resync {lastSeq}`; 服务端回放 `lastSeq` 之后的房间事件(重新领取序列号)+ 当前 `room_state`, 并在对局中补发一次本人私有视图(`kind:"resync"`)。
  - 日志中只存公共信息, 私有视图永远由 `GameViewFor(guestID)` 在传输层实时投影, 因此回放不会串视角。
  - 连接水位 `lastSentGameSeq` 记录的是**对局事件空间**的最后序列(`Room.GameSeq()`), 与房间序列空间分离, 避免水位域混用导致漏发/重放。
- **心跳**: 客户端每 20s `ping`, 服务端 90s 无消息断开; 客户端带 4 级退避自动重连。
- **风扇**: 服务器每 250ms 推进一次 `rooms.Tick()`(机器人行动、人类超时托管), 内容变化则广播对局事件与大厅快照。

### 11.4 服务器权威

洗牌、发牌、合法性判定、结算、AI 决策全部在服务端; 浏览器只发送动作意图。机器人由服务端 `AutoStep` 在 Tick 中驱动, 用与真人完全相同的 `ApplyAction` 管线。

### 11.5 上限与清理

- 在线房间数上限 `rooms.max_online`(默认 3): 满额时**拒绝创建**, 但既有房间仍可加入。
- 空房即时回收 + 周期 `Sweep`; 对局房间在结算后按 `idle_ttl` 回收。

### 11.6 验证与已知限制

- `go test ./internal/room/ ./internal/httpapi/ ./internal/store/`: 密码/邀请生命周期/准备与机器人/规则锁定/解散/超时托管/整局推进/版本化迁移。
- `node scripts/e2e-online.mjs`(37 项, 真实 WS): 健康检查、8 个游客、建房(公开/私有/密码)、大厅可见性、房间上限拒绝、密码错误/正确、邀请免密与一次性、满员、未准备拒绝开局、私有视图与手牌隐私、整局到结算、中途断线重连(座位与手牌一致)、各端结算一致、seq 严格递增、解散通知。
- 已知限制: 尚无观战与回放; 邀请无二维码; 房间列表只覆盖单实例(多实例需共享注册表)。
- 游客身份自 2026-10-24 起存在 **sessionStorage(每标签一份)**: 同一浏览器多标签页各自是独立游客, 可以「开两个标签自己陪自己打」; 代价是关闭标签后身份不再保留(游客本就是匿名身份, 重新输入名字即可)。旧版 localStorage 身份会在首次加载时一次性迁移进当前标签并清除。

## 12. P2P 与局域网联机(阶段6)

### 12.1 拓扑与信令

```
房主(浏览器) ── DataChannel ──┬─ 加入者 A(真人)
  ↑ 本地跑权威引擎(WASM)      ├─ 加入者 B(真人)
  │                          └─ AI 座位(房主本地引擎驱动)
  └── WebSocket 信令(/ws/signal) ── 房间码交换 + SDP/ICE 中继
```

- **星形拓扑**: 房主是权威(本地 WASM 引擎), 每个加入者只与房主建立一条 `RTCDataChannel`; 加入者之间不互连, 因此不需要 NAT 穿透的网状结构。
- **信令服务** `server/internal/sig`: 只做房间码交换、SDP/ICE 中继与在场列表, **不接触对局数据**。op: `room.create` / `room.join` / `room.leave` / `signal.offer` / `signal.answer` / `signal.ice` / `room.peers` / `room.list` / `ping`。
- 6 位房间码(去除易混字符)、可选密码(HMAC-SHA256)、1 分钟 120 条限流、10 分钟空闲房间清理; 信令房间**不占用在线托管房间上限**(默认 3)。
- `DataChannel` 走**与在线模式完全相同的信封/op 列表**, 因此 join/ready/bot/start/action 的语义两端一致, UI 无需分支。

### 12.2 房主权威与作弊边界

- 房主浏览器内运行完整规则引擎(与服务器同一份 Go 代码的 WASM 构建), 洗牌/发牌/合法性/结算/机器人全在房主端。
- **座位表即阵容**: 开局时房主把 `room.players`(房主 + 真实加入者 + 机器人)换算成引擎的显式 `SeatSpec[]`(`localGame.start({ seats, seatOwners, hostSeat: 0 })`), 每个真人占一个真实座位, 空位由 AI 补足。因此 P2P 房主跑的是多真人同一局, 而不是「1 真人 + AI」的单机局。
- 真人动作按座位进入引擎: 房主自己的动作走 `local.act()`(座位 = `humanSeat` = 0), 远端加入者的动作按 `peerId → 座位` 反查(`connection._seatByPeer`)后走 `local.actAs(seat, ...)`。走到哪个真人就停在哪个真人,**任一真人未决定时引擎不会替该座位出牌**。
- **信任模型**: P2P 模式信任房主。房主理论上可篡改自己的引擎逻辑(改牌、看牌), 这是 P2P 的固有代价; 在线服务器模式不存在此问题。文档化而非掩饰。
- 加入者的动作经由 DataChannel 上行, 房主用同一个权威引擎校验后才推进, 非法动作被拒绝并回错误。
- **手牌绝不下发给非本人**: 房主对每个 peer 只单播「该座位的私有视图」(手牌/摸牌/可行动作), 房间级 `room_state` 只含公开信息(公开手牌数、河牌、副露)。

### 12.3 私有视图分发(易错点)

在线模式下「给每个连接单独投影」由服务端天然完成; P2P 模式必须由房主手工做, 这是本阶段最主要的缺陷来源:

1. **seat → peerId 映射必须独立保存**: 开局后房间投影会重建 `players`, 因此映射不能挂在 `room.players` 上, 而是 `connection._peerBySeat`(从 `room.join` 的来源 peer 记录)。
2. **引擎自动推进不会触发动作分发**: 摸牌、机器人行动、响应窗口都由引擎内部 pump 推进, 不经过 op 分发。只靠「动作后单播」会漏状态, 因此房主在对局中运行 **300ms 私有视图泵**(`_startPrivateViewPump`), 对每个座位做状态指纹比对, 变化才推送。
3. **加入者视图丢失必须能自愈**: 页面刷新/重连后加入者 `match.view` 为空, 而房主可能正等这一步决策(表现为「卡住」)。加入者侧每 2.5s 检查一次: 若房间在打而本地无视图, 主动 `sync.resync`, 房主收到后立即补推该座位视图。
4. **响应窗口必须双向可见**: 吃/碰/杠/胡/过的窗口只出现在对应座位的 `actions` 里, 因此私有视图必须包含 `pendingTile/pendingBy/actions`, 否则该座位无法回应。

### 12.4 降级与 TURN

- ICE 候选顺序: 局域网主机候选(同网段直连) → STUN 反射候选(公网直连) → TURN 中继。默认 STUN: `stun.l.google.com:19302`、`stun1.l.google.com:19302`。
- 对称型 NAT/严格防火墙下直连失败时, 需配置 TURN(`turnUrls` + 用户名/凭据), 由房主/加入者在设置中填入; 未配置时表现为「P2P 连接超时(DataChannel 未打开)」。
- TURN 成本提示: 中继流量计费且延迟高于直连, 仅供必要时兜底; 局域网/常见家用 NAT 下不会用到。

### 12.5 验证与已知限制

- 真实浏览器双标签页(房主 + 加入者 + 2 AI)**完整打完一局**: 加入者拿到自己 13/14 张手牌、响应窗口可行动, 双方终局结果完全一致(赢家座位/番数/分数/牌墙/河牌逐项相同)。
- 已知限制: 房主断线即对局终止(阶段7 房主迁移); 房主作弊无法在 P2P 下防御; 目前仅 IPv4 直连与 STUN, TURN 需手工配置; 单实例信令(多实例需共享注册表)。

### 12.6 部署与 P2P 的两个硬依赖

1. **`engine.wasm` 不能删**: P2P/局域网模式下房主浏览器要跑权威引擎, `web/src/engine/engine.ts` 从 `${import.meta.env.BASE_URL}engine/engine.wasm` 加载它(还有 `engine/wasm_exec.js`)。`web/public/engine/` 由 `pnpm run engine` 生成、不入库, 但**构建时必须已存在**, 否则 P2P 房主会在开局时报「单机引擎未就绪」。前端产物因此约 5.7MB, 其中 5.0MB 是它。
2. **部署前缀必须构建进产物**: 线上是根路径 `https://mj.wole110.xyz/`, 用默认 `pnpm build`(base `/`)。前端资源与 REST/WS/信令地址全靠 `import.meta.env.BASE_URL`; 若将来改回子路径(如 `/mj/`), 构建出来的包必须带对应 base —— 根路径构建的包放在 `/mj/` 下会请求到站点根部而全盘失败。用 `pnpm build:mj`(即 `vite build --base=/mj/`), 或设 `MAJIANG_BASE=/mj/` 后 `pnpm build`。

## 13. 响应窗口、优先级仲裁与计时(阶段10)

### 13.1 为什么要重做响应窗口

阶段 5~9 的响应窗口是"全员表态"模型: `Discard` 后把 `respAnswered[4]` 全部清零, `tryCloseWindow()` 必须等到**除出牌者外每家都已表态**才轮转; 而 `AvailableActions` 又无条件给每个座位追加 `"pass"`, 于是"能吃/碰/杠/胡"的座位要看「过」按钮, 什么都做不了的座位**也**要看「过」按钮 —— 一局里绝大部分点击都是无意义的"过"。

### 13.2 现在的模型

1. **只对真有选择的座位开窗**: `Discard` 末尾 `resetResponseLocked()` 遍历座位, 用 `respondActions(seat)`(胡/碰/杠/吃, 不含 pass)判断; 返回空的座位直接 `respAnswered[seat]=true`; 有选择的座位按 `ResponseSecondsFor(actions)` 设**独立** `respDeadlines[seat]`(同人可胡可碰取两者较大值)。
2. **响应动作 = 提交 claim**: `Respond` 非 pass 分支不再立刻执行, 而是构造 `respondClaim{pri, sub, seq, run, with, shape}` 存进 `respClaims[seat]`, 置 `claimState[seat]="pending"`, 产出 `EvJudge` 事件, 再调 `settleLocked()`。
3. **仲裁**: `settleLocked()` 取当前最优 claim, 若存在**更高优先级**或**同优先级但座次更靠前**的座位尚未表态则继续等; 否则(或全员已答)执行最优 claim, 其余 claim 置 `failed` 并各发一条 `EvJudge`。事件顺序固定为 `failed → 动作事件 → success`, 前端据此渲染「判定失败」再切到「判定成功」。
4. **优先级**: `priWin=3 > priMeld=2 > priChow=1`; 同级按 `claimDistance`(离出牌者的顺时针距离)升序。`RuleSet.RobWin=false` 时荣和退化为"先提交者得", `RobMeld=false` 时碰/杠同理。
5. **计时与超时**: `Match.Tick(now)` 是唯一推进入口 —— 响应阶段把到期座位标记已答后 `settleLocked()`; 出牌阶段 `DiscardSec>0` 且到期则 `autoDiscardTile()`(优先刚摸的牌, 否则手牌最后一张)并 `Discard`。服务端 `room.AutoStep(now)` 与 WASM `tick(nowMs, since)` 都只调它。
6. **`DiscardSec=0`(不限时)的收敛**: 此时引擎不设出牌 deadline, 由出牌者的**下家**用 `forceDraw` 动作(前端「摸牌」按钮)主动关窗; `ForceDraw` 只在 `!RespondEnabled()` 时合法, 且必须正是下家。
7. **房间层兜底**: `room.AutoStep` 在 `Tick` 与机器人之后还有 `nextStalledLocked`(`lastActivity + DefaultTurnTimeout` = 30s), 覆盖"座位断线/挂机且引擎没有计时器"的情况。

### 13.3 规则字段

`game.RuleSet` 的计时与截牌字段(`json` 名与服务端一致): `respond_meld_sec`(默认 3, 0~30)、`respond_win_sec`(默认 5, 0~30)、`discard_sec`(默认 0, 0 或 3~120)、`rob_win`(默认 true)、`rob_meld`(默认 true)。`room.create` 的 `ruleSet` 由 `httpapi/rules_patch.go` 的全指针 `ruleSetPatch.apply(base)` **逐字段合并**到 `DefaultRuleSet()`, 未提交字段保留默认值。

### 13.4 视图契约

`SeatView` 新增(全部可选, 空值省略): `respondSecLeft` / `respondDeadline`(UnixMilli) / `discardSecLeft` / `discardDeadline` / `claimStates []string`(pending/success/failed) / `forceDraw bool`。前端 `MahjongTable.vue` 用 200ms 本地时钟重算剩余秒(不依赖服务端每次推送), `ActionControls.vue` 显示倒计时胶囊。

### 13.5 已知限制(阶段10)

- ~~番型仍只有基础/七对/自摸/门清; 未实现抢杠胡、杠上开花、海底捞月等"时序型"番。~~ → 阶段 11 已实现，见 §14。
- `Tick` 依赖调用频率: 服务端 `Run` 循环 250ms 一次, WASM 侧前端 200ms 一次; 倒计时精度即为此量级(±0.25s)。
- 判定提示是"事件回放"式的, 若客户端在判定期间重连, 只能从 `claimStates` 看到最终状态, 看不到中间 pending 过程。

## 14. 时序番型、副露多选与 DIY 牌面(阶段11)

### 14.1 时序番型

三类番都依赖"什么时候和的牌"，因此引擎必须在关键动作里记录时序上下文：

| 字段 | 含义 | 写入点 |
|---|---|---|
| `kongFlower` | 当前手牌来自杠后补牌 | `afterKongDraw()` 置 true；`Draw()` 置 false |
| `lastDrawTile` | 这次摸牌是牌墙最后一张 | `Draw()` / `afterKongDraw()` 里 `wall.Remaining()==0` |
| `riverEnd` | 出牌后牌墙已空（这是最后一张出牌） | `Discard()` 里 `wall.Remaining()==0` |
| `winRobKong` | 本局是抢杠胡 | `executeClaimLocked` 的 `hu.robbing` 分支读 `pend.robKong` |

**抢杠胡**是唯一需要新窗口的：`KongSelf` 补杠前先 `canRobKong(seat, tile)`（遍历其他座位 `CanWinHand(hand+tile, melds)`），有人听则挂起 `pendingKong` 并开 `respPending{robKong:true}`，`closeWindowLocked()` 在无人抢时继续补杠。窗口内其他家只能胡（`respondActions` 里 `robKong` 时不返回碰/杠/吃），`ForceDraw` 明确拒绝。

**番数**在 `resolveWin` 一次算定：基础 1「和牌」+ 七对子 +2 + 自摸 +1 + 非七对门清 +1，然后时序番**互斥取最高一个** +2（抢杠胡 / 杠上开花 / 海底捞月 / 河底捞鱼）。结果写进 `Match.winFan/winPay/winFlags`，`SeatView.Result.Flags` 供结算面板逐条展示。

### 14.2 副露多选的 UI 陷阱

多个吃/暗杠/补杠候选时必须让玩家选一组。旧实现用 `absolute bottom-full` 把面板挂在牌桌内，而 `.mahjong-table` 是 `overflow-hidden` —— 面板被裁到可视区外，点击坐标落到下层元素上，表现就是"点了没反应"。现在统一 `<Teleport to="body">` + `fixed inset-0` 遮罩，与任何祖先的 `overflow`/`transform` 解耦。

配套三点：(1) 候选用**多重集**校验（同种牌需真有 2 张），与服务端 `chowCandidates` 同口径；(2) 面板内点击后加 900ms 提交锁，避免连点导致第二次 `already_responded`；(3) `watch` 动作/候选/倒计时变化，失效即自动收起，防止玩家对着过期面板点出非法动作。

### 14.3 DIY 主题

数据流：`diy_tiles`（code → file_name + fit）+ `diy_settings`（管理密码哈希）+ `MAJIANG_DIY_DIR` 下的图片文件。前端 `stores/diy.ts` 在应用启动时 `refresh()` 一次，`MahjongTile` 按 `code` 取图并套 `object-fit`；未自定义的牌回退经典 SVG。

密码：首次启动把 `MAJIANG_DIY_PASSWORD` 用 `secret.HashPassword`（PBKDF2-SHA256/120k）固化进库，之后只能通过 `POST /api/v1/diy/password` 改（需旧密码）。`checkDIYPassword` 对空哈希一律拒绝——`secret.VerifyPassword` 在空哈希时对任意密码返回 true，直接复用会变成"没设密码就能进"。

图片：客户端传 `data:image/...;base64,`，服务端嗅探文件头定扩展名（PNG/JPEG/WEBP/GIF），文件名用纳秒时间戳生成，因此客户端无法影响路径。请求体上限单独放宽（`MaxImageBytes/3*4 + 64KB`），因为 base64 有 4/3 膨胀而 `decodeJSON` 全局卡 1MB。

---

## 15. 房主迁移与断线恢复(阶段7)

### 15.1 为什么在线模式不需要搬运隐藏信息

在线模式下**唯一权威状态一直在服务端** `room.Room.match`（`*game.Match`），房主身份只是"开局 / 改规则 / 解散"的权限位。因此房主迁移只是换一个人拿权限，牌墙、各家手牌、事件日志原地不动，天然满足"不向普通玩家明文同步隐藏手牌"。

P2P 模式相反：权威引擎在房主浏览器的 WASM 里（`server/cmd/wasm/main.go` 的 `WoleEngine`），牌墙只存在于那台设备的内存中。中途接管必须靠 seed + 完整操作日志重放，且无法证明新房主重放出的牌墙没被篡改。阶段要求允许"无法可信恢复时明确终止"，因此 P2P 只在**大厅 / 一局已结束**时交棒；对局中失去房主时前端弹「对局已中断」并建议重开，不做假恢复。

### 15.2 任期(epoch)与迁移日志

`Room.hostEpoch` 是单调递增的房主任期号，随每次真实换人 +1，并记录一条 `MigrationEntry{At, Kind, Epoch, From, To}`（保留最近 32 条）。

**只有房主真的变了才 bump 任期**：注册表每秒扫描所有房间，如果每次扫描都自增，客户端的"房主已转交"提示会疯狂刷屏，也无法用 epoch 当作客户端水位。

迁移种类：`host.leave`（主动离开，立即）/ `host.disconnect`（断线超 `HostReconnectGrace=15s`）/ `host.reap`（幽灵座位被回收）/ `host.seat_bot`（房主座位被转托管）。选举规则：在线真人中座位号最小者接任，退化为任意真人，无真人则不迁移。

### 15.3 三条状态流

| 场景 | 处理 |
|---|---|
| 主动离开（对局中） | 座位保留但 `IsBot=true; Left=true`，权限立即交棒；牌局由机器人继续，不打断其他人 |
| 断线（关页面/掉网） | `SetConnected(false)` → 对局中先由 `nextStalledLocked` 托管推进；超 `SeatReclaimGrace=60s` 转 `IsBot`；房主另加 15s 宽限后迁走权限 |
| 重连 | 握手 `FindByGuest` 找回房间 → `SetConnected(true)` → `if p.IsBot { p.IsBot=false }` 收回座位；**旧房主回来不抢任期** |

`HasPlayer` 必须排除 `Left`，否则 `FindByGuest` 会把刚离开的人抓回牌局——这正是"返回的人一直占着位置"的一半根因。另一半是座位直到一局结束才真正释放，因此 `PurgeLeft()` 由注册表 tick 周期调用：只在 `match == nil || Phase()==Finished` 时删座位，删完无真人则 `teardownLocked()` 广播房间结束。

### 15.4 前端：返回键与自动回房

- **返回键**：不再让浏览器直接离开 SPA。进入房间/牌桌时 `history.pushState` 压一个哨兵，`popstate` 时按当前屏决定：牌桌 → 等待房间；其它房间态 → 首页且保留房间（`sessionStorage` 记 `majiang.session.room`）。
- **两个坑**（都表现为"返回键没反应"）：① 服务端每次 `room.state` 都是新对象，`watch(conn.room)` 若无条件切屏会把刚回首页的人拽回房间——必须用 `explicit`（create/join）或"首页但未被标记留在首页"判断；② 对局中每次事件都会更新 `match.view`，`watch(match.view)` 会把首页弹回牌桌——需要 `leftTable` 挡板。
- **重连**：`onMounted` 读 hint 自动 `connect()`，且 `connection.ts` 收到 `room.state` 且 `status === 'playing'` 而本地没有 `match.view` 时补一次 `resync()`——服务端握手只会推房间状态，牌桌视图必须显式要。

### 15.5 已知限制

- 迁移只在服务端进程内存里（房间本身不持久化），进程重启等于全部房间结束。
- 迁移日志目前只暴露给 `room` 包（`Migrations()`），未通过协议下发；排障要看服务端日志。
- P2P 中途换主不做恢复（见 15.1），属于设计取舍而非缺陷。
