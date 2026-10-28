# 我嘞麻将（woleMajiang）

免下载的响应式麻将网页游戏：支持 2~4 人、真人/AI、在线 / WebRTC P2P / 局域网三种联机方式、房间与邀请、多规则、三主题（经典/水墨/DIY 自定义牌面）。

- 前端：Vue3 + TypeScript + Vite + Pinia + Tailwind
- 后端：Go + WebSocket + SQLite（纯 Go 驱动 modernc.org/sqlite，无 CGO）
- 部署目标：1 核 1GB，在线房间默认上限 3 个

> 当前为阶段11 + 阶段7：阶段 1~7 与阶段 9~11 已完成（架构基础、单机 AI、在线房间与邀请、P2P/局域网、移动端适配与安全加固、规则细则计时与优先级仲裁、时序番型与 DIY 牌面主题、房主迁移与断线恢复）。完整架构见 [docs/architecture.md](docs/architecture.md)。

## 目录结构

```
woleMajiang/
├── server/               # Go 后端
│   ├── cmd/server/       # 入口
│   ├── internal/
│   │   ├── config/       # 配置(默认值→JSON→环境变量)
│   │   ├── logging/      # slog JSON 日志
│   │   ├── protocol/     # 协议信封与视图模型(与前端镜像)
│   │   ├── game/         # 麻将领域模型(牌/牌墙/座位/动作/规则/主题)
│   │   ├── room/         # 权威房间注册表、事件日志、投影、清扫
│   │   ├── httpapi/      # REST + WebSocket(握手认证/限流/广播)
│   │   ├── store/        # SQLite + 嵌入式迁移
│   │   └── ratelimit/    # 令牌桶
│   ├── config.example.json
│   └── migrations 嵌入于 internal/store/migrations/
├── web/                  # Vue3 前端
│   ├── src/
│   │   ├── api/          # REST 客户端
│   │   ├── protocol/     # 协议类型(与服务端镜像)
│   │   ├── stores/       # Pinia: identity / connection
│   │   ├── App.vue       # 阶段1: 身份/连接/房间 UI
│   │   └── main.ts
│   └── vite.config.ts    # dev 代理 /api /ws → 127.0.0.1:8080
├── scripts/
│   ├── e2e-ws.mjs        # WS 协议端到端验证(需服务器运行中)
│   └── e2e-online.mjs    # 在线房间/邀请/重连端到端
└── docs/architecture.md  # 架构基线
```

## 环境要求

- Go ≥ 1.27（本机装于 `D:\SOFT\go`，见下）
- Node ≥ 22 / pnpm（开发期可用 DSH 内置运行时）
- 无 CGO 依赖（SQLite 为纯 Go 实现）

## 启动

### 后端

```powershell
# Windows(PowerShell), 使用本机便携 Go:
$env:Path = 'D:\SOFT\go\bin;' + $env:Path
$env:GOPATH = 'D:\SOFT\gopath'
cd server
go run ./cmd/server                    # 默认 127.0.0.1:8080, 数据库 data/majiang.db
# 或使用配置文件:
go run ./cmd/server -config config.example.json
```

环境变量覆盖（优先级最高）：`MAJIANG_HTTP_LISTEN / MAJIANG_DB_PATH / MAJIANG_MAX_ONLINE_ROOMS / MAJIANG_ROOM_IDLE_TTL_SECONDS / MAJIANG_SWEEP_INTERVAL_SECONDS / MAJIANG_MAX_MESSAGE_BYTES / MAJIANG_MESSAGE_BURST / MAJIANG_MESSAGES_PER_SECOND / MAJIANG_ALLOWED_ORIGINS / MAJIANG_LOG_LEVEL`

### 前端

```powershell
cd web
pnpm install
pnpm dev          # http://localhost:5173 (自动代理 /api /ws 到后端)
```

### 生产构建

```powershell
cd web; pnpm build      # 产物 web/dist/
cd server; go build -o majiang-server.exe ./cmd/server
```

## 测试

```powershell
# Go 单元测试(全部包)
cd server; go test ./... -count=1

# 前端单元测试
cd web; pnpm test

# 前端类型检查 + 生产构建
cd web; pnpm build

# WS 协议端到端验证(需后端运行于 127.0.0.1:8080)
node scripts/e2e-ws.mjs

# 在线房间/邀请/断线重连端到端(37 项, 需后端运行于 127.0.0.1:18080 且库为空)
node scripts/e2e-online.mjs
```

## 验证过的接口

- `GET /healthz` · `GET /readyz` · `GET /api/v1/health`（服务状态、房间数、游客数）
- `POST /api/v1/guests` `{nickname}` → `{guestId, nickname, token}`（令牌仅此一次）
- `POST /api/v1/reconnect` `{guestId, token}` → 200/401
- `GET /ws`：握手 `guest.hello` → 房间创建/加入/离开/增量同步/限流

## 开发路线（对应 D:\PandV\D\文档\麻将提示词）

1. ✅ 架构与项目基础
2. ✅ 麻将规则引擎（洗牌/判定/状态机/计分）
3. ✅ 前端牌桌与双主题（经典矢量 / 明文水墨 / 2D伪3D牌桌 / 测试驱动）
4. ✅ 单机可玩与 AI
5. ✅ 在线房间与邀请
6. ✅ P2P 与局域网联机
7. 房主迁移与断线恢复
8. 高级规则与完整配置
9. ✅ 安全性能与适配测试
10. ✅ 部署验收与交付

### 阶段4: 单机可玩与 AI ✅
- Go 规则引擎/AI/本地对局编译为 WASM, 浏览器内离线对局(与联机同一份判定)
- 三档 AI(简单/普通/困难), 只看自己座位视图
- 回合超时自动托管 + 手动接管; 音效开关
- 构建: `pnpm run engine`; 引擎端到端: `pnpm run test:wasm`

### 阶段5: 在线房间与邀请 ✅
- 公开/私有房间、房间名与密码、公开大厅、座位与准备、房主权限、解散与空房回收
- 邀请链接: 安全随机令牌(仅下发一次, 库存哈希)、独立密码、有效期、次数上限、撤销、可选免房间密码
- 幂等动作(actionSeq)、断线重连增量同步、心跳与自动重连、单一单调 seq
- 服务端权威 + 机器人/超时托管; 在线房间上限默认 3 可配
- 端到端: `node scripts/e2e-online.mjs`(37 项, 含四端真联机一局与中途断线重连)
- 说明: 开局仅支持 4 人规则, 2/3 人会明确拒绝(unsupported_players)而非静默降级

### 阶段6: P2P 与局域网联机 ✅
- WebRTC 星形拓扑: 房主本地跑权威引擎(WASM), 每个加入者一条 DataChannel, 加入者之间不互连
- 独立信令服务 `/ws/signal`(server/internal/sig): 房间码交换 + SDP/ICE 中继, 不接触对局数据, 不占在线房间上限
- 6 位房间码、可选房间密码、PeerConnection 状态与失败提示、`room.peers` 在场列表
- 智能降级: 局域网主机候选 → STUN 反射候选 → TURN 中继(默认 STUN 双节点, TURN 可自行配置)
- 手牌隐私: 房主按座位**单播私有视图**, 房间级状态只含公开信息; 对局中 300ms 私有视图泵推送变化
- 断线自愈: 加入者视图丢失时自动 `sync.resync`, 房主立即补推该座位视图
- 入口: 首页「在线服务器 / P2P · WebRTC」两个 tab, P2P 下建房得到房间码, 另一台设备/标签页粘贴房间码加入
- 真实验证: 双标签页(房主 + 加入者 + 2 AI)完整打完一局, 双方终局结果逐项一致
- 已知限制: P2P 模式中途失去房主无法恢复(牌墙只在房主浏览器里, 见阶段7); P2P 模式信任房主(可作弊), 在线服务器模式无此问题

### 阶段10: 规则细则(计时 / 优先级仲裁 / 截胡截碰) ✅
- 「更多规则设置」二级面板(计时 / 优先级 / 和牌 三 tab), 不改动时跟随玩法预设
- 响应倒计时**按座位独立**: 碰/杠/吃默认 3s、荣和默认 5s(均可 0~30s 或关闭); 同一家可胡可碰时取较长的一个
- 关闭响应倒计时时, 由出牌者的下家点「摸牌」推进(`forceDraw`), 不再需要逐家点「过」
- 优先级仲裁 **胡 > 碰/杠 > 吃**, 同级按离出牌者座次; 截胡/截碰开关(关掉即"先按者得")
- 判定过程可见: 牌桌显示「判定中」, 出结果后「判定成功 / 判定失败」(被更高优先级截走)
- 出牌倒计时默认关闭, 可设 3~120s; 超时自动打出刚摸的牌(没有则最右一张), 其他家显示「等待 X 出牌」
- 无真实选择的座位由引擎自动通过, 一局里不再出现"无意义的过"
- 服务端权威 / WASM 单机 / P2P 房主三处共用同一份 `game` 实现, 无规则分叉

### 阶段11: 时序番型 / 副露多选 / DIY 牌面主题 ✅
- **时序型番种**(各 +2 番, 同时只计最高的一个, 可在建房的「和牌」tab 单独关闭):
  - 抢杠胡: 别人补杠时你手上正好听那张牌 → 开抢杠窗口(只有胡, 不能碰/杠/吃)
  - 杠上开花: 杠后补的那张牌直接自摸
  - 海底捞月(自摸最后一张) / 河底捞鱼(胡别人打出的最后一张)
  - 结算面板显示番种明细胶囊(和牌 / 七对子 / 自摸 / 门清 / 时序番)
- **副露多选交互重做**: 多个吃/暗杠/补杠候选时弹出**全屏遮罩面板**(Teleport 到 body), 不再被牌桌 `overflow-hidden` 裁掉而"点了没反应"; 面板内带 900ms 提交锁防连点、显示候选牌张与倒计时, 动作失效(超时/被截走)时自动收起
- 吃牌候选改为按手牌**多重集**校验(同种牌需真有 2 张), 与服务端 `chowCandidates` 口径一致
- **第三套主题「DIY」**: 首页左边缘齿轮 → 密码门(无账号, 只有密码) → 以「经典图案」为模板逐张替换牌面
  - 每张牌可上传图片(png/jpg/webp/gif, ≤2MB) + 选填充方式(填充/完整/拉伸/缩小/原始) + DIY 与经典对照预览 + 牌桌三种尺寸实景预览
  - 牌面存在服务端, **所有选择 DIY 主题的玩家看到同一套**; 未自定义的牌自动回退经典图案
  - 设置页可「修改密码」(验证一次旧密码即可改)
  - 服务端: `GET /api/v1/diy`(公开) + `POST /api/v1/diy/{auth,password,tiles}` + `GET /api/v1/diy/images/{name}`; 图片按内容嗅探扩展名, 文件名服务端生成, 无路径穿越
  - 部署环境变量: `MAJIANG_DIY_DIR`(图片目录)、`MAJIANG_DIY_PASSWORD`(首次启动固化为库内密码哈希)、`MAJIANG_DIY_MAX_IMAGE_BYTES`

### 阶段7: 房主迁移与断线恢复 + 音效 ✅
- **按返回键不再丢对局**: 牌桌按返回 → 回到等待房间(有「回到牌桌」); 再返回 → 回首页并保留房间, 首页显示「你还在房间 X / 继续对局 / 离开房间」
- **刷新/断线自动回房**: `sessionStorage` 记房间, 重新打开页面自动重连并 `resync` 出牌桌(含手牌与可用操作); 服务端握手本来就会恢复座位
- **房主任期(epoch) + 迁移日志**: 位移只在房主真的变化时 `+1`; 迁移种类 `host.leave / host.disconnect / host.reap / host.seat_bot`
  - 主动离开 → 立即交棒; 断线 → 15s 宽限后交棒; 掉线座位 60s 转机器人托管; 重连自动收回座位; **旧房主回来不抢任期**
  - 房主悄悄换人时给全员顶部提示("你已成为新房主" / "房主已转交给 X")
- **离开的人不再占座**: 对局中离开只转托管不删座位(避免引擎座位错位), 一局结束由注册表 tick 让位, 弃座后无真人则房间自动结束
- **P2P 明确终止**: 牌墙只存在于房主浏览器, 中途失去房主时弹「对局已中断」并建议重开, 不做不可信的"假恢复"(在线模式无此限制)
- **音效**: 纯 WebAudio 现场合成(无音频文件), 覆盖出牌/摸牌/吃碰/杠/和牌/流局/过/轮到你/点击; 牌桌右上「音效: 开/关」, 偏好存 `localStorage`
- 服务端 `room/migrate.go`(新) + `registry.Tick` 里的 `ReclaimSeats → TickHost → purgeLeft`; 测试 `room/migrate_test.go` 9 个用例(含 `hostGrace`/`seatGrace` 故障注入)