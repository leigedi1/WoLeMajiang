# 接续报告（Handoff）· 我嘞麻将 / woleMajiang

> 生成时间：2026-10-24
> 目的：上下文过长需开新对话。本文档自包含，新对话**先读完本文件**即可无损接续。
> 仓库根目录：`D:\PandV\D\CONprogram\woleMajiang`

---

## 0. 一句话现状

免下载的响应式联网麻将网页游戏。**阶段 1~6 + 阶段 9~11 已完成**（后端权威 Go 引擎、2/3/4 人可开局、牌集自选全链路生效、三种联机方式均已在真实浏览器打到结算）。
**已部署到阿里云并可从公网访问**：<https://mj.wole110.xyz/>（2026-10-25 迁到独立二级域名，根路径直出；旧地址 `https://tsapi.wole110.xyz/mj/` 仍 301 跳转过来）。
**本轮（2026-10-24）已完成**：§7-A 信令 403 根因修复并上线；P2P/局域网真实 WebRTC 打通（P2P 2 人、局域网 3 人整局到结算）；2/3 人在线整局跑通；身份改 sessionStorage 支持同浏览器多标签测试。
**随后又完成阶段 9（安全/性能/适配，跳过 7、8）**：新增可复现压测器 `server/cmd/stress`、实测资源数据、8 项滥用探测全过、补安全响应头（已上线）；报告见 `docs/阶段9-安全性能与适配测试报告.md`，细节见 §7-E。
**2026-10-25 域名迁移**：站点从听书共用域名 `https://tsapi.wole110.xyz/mj/` 迁到独立二级域名 **`https://mj.wole110.xyz/`**（根路径部署，前端 base 改回 `/`，systemd 白名单加新域名，旧 `/mj/*` 保留 301；Caddy 配置在 Docker 容器内热加载）。
**2026-10-27 阶段 10（规则细则与移动端）**：新增「更多规则设置」二级面板（计时 / 优先级 / 和牌 三个 tab）；响应倒计时按座位独立（碰/杠/吃 默认 3s、胡默认 5s，0=关闭改为下家点「摸牌」推进），出牌倒计时默认关闭（3~120s，超时自动打出刚摸的牌）；引入响应仲裁优先级 **胡 > 碰/杠 > 吃** + 截胡/截碰开关 + 牌桌「判定中 / 判定成功 / 判定失败」提示；不再要求逐家点「过」（引擎自动通过无真实选择的座位）。**仍缺**：真两台设备（手机+电脑）的 P2P/局域网穿越证据、TURN（对称 NAT 下必失败）、阶段 7（房主迁移/断线恢复）。
**2026-10-27 阶段 11（时序番型 / 副露多选 / DIY 牌面）**：新增抢杠胡、杠上开花、海底捞月/河底捞鱼三类时序番（各 +2 番，互斥只计最高一个，可在建房「和牌」tab 单独关闭）；重做多选副露交互（全屏 Teleport 面板 + 提交锁 + 失效自动收起，修掉「点了没反应」）；新增第三套主题 **DIY**——首页齿轮 + 密码门 + 以经典图案为模板逐张上传图片/选填充方式/预览/保存，牌面存服务端、所有玩家共享，并支持改密码。详见 §7-G。
**2026-10-27 阶段 7（房主迁移与断线恢复 + 音效）**：修掉「房主按返回键回不到对局」「房主不迁移」「返回的人一直占座」三个缺陷。返回键改为 SPA 内导航（牌桌→等待房间→首页，房间始终保留，首页有「继续对局」）；刷新/重连自动回房并 `resync` 出牌桌；服务端引入 `hostEpoch` 任期 + 迁移日志 + 断线宽限（15s）自动交棒 + 对局中掉线/离开座位转机器人托管（60s）+ 重连收回座位 + 一局结束后让位；房主悄悄换人时给全员提示。另加 9 个现场合成的 WebAudio 音效（出牌/摸牌/鸣牌/杠/和牌/流局/过/轮到你/点击）与「音效: 开/关」开关。P2P 中途失去房主时**明确终止并提示重开**（牌墙只存在于房主浏览器，不做不可信的"假恢复"）。详见 §7-H。

---

## 1. 用户目标（当前 goal，原文）

> 在 `D:\PandV\D\CONprogram\woleMajiang` 依次完成：
> (1) 补齐 2 人与 3 人麻将规则（规则引擎 + 房间 + 前端可正常开局打完一局）并保持 4 人可用；
> (2) 真实验证 P2P 与局域网（WebRTC DataChannel）路径可正常联机打一局；
> (3) 把项目部署到用户的阿里云服务器（含静态资源托管、systemd/进程守护、域名或 IP 可访问），交付可让用户亲自测试的线上地址。

goal id `goal-9d1fac63-d8c9-47cd-b602-3d38d8b278e1`（session 级，新对话不会继承，按上面文字重建即可）。

**用户的产品取向（很重要）**：讨厌复杂。明确要求"名字 → 创建房间设置"为主流程，删掉单机/AI/大厅/邀请面板那套复杂 UI。
现在首页只有：名字输入 →（进入）→ 创建房间 / 加入房间 / 换个名字。

---

## 2. 技术栈与目录

- 前端：Vue 3.5 + TypeScript + Vite 6 + Tailwind 4 + Pinia
- 后端：Go 1.27.3 + gorilla/websocket + SQLite（modernc 纯 Go）
- 联机：①在线服务器（WS 中转，最稳）②P2P（WebRTC DataChannel，房主本地权威、STAR 拓扑）③局域网（WebRTC，仅 host candidate，`lanOnly`）
- 规则引擎：纯 Go，`server/internal/game/`，同一份代码另编 WASM 供浏览器单机（当前 UI 已无单机入口，wasm 产物仍留在 dist，无用可删）

```
woleMajiang/
├─ server/                     Go 后端（cmd/server, internal/{config,httpapi,room,game,sig,store,ai,local,protocol,ratelimit,secret,logging}）
├─ web/                        前端（src/{App.vue,components,stores,config,api,engine,protocol,theme}）
├─ docs/architecture.md        架构基线（§1~§11；P2P 那节在第 12 节位置，见 §8）
├─ scripts/                    e2e-ws.mjs / e2e-match.mjs / e2e-online.mjs / measure-load.mjs / sig-signal.mjs
└─ README.md
```

主要前端文件：
- `web/src/App.vue`（395 行）：四屏状态机 `home | create | join | room | table`，`transport`（房主）/`joinTransport`（加入者）都是 `'server' | 'p2p' | 'lan'`
- `web/src/stores/connection.ts`：WS + P2P 双传输，`_sendP2P` 在房主侧本地分发、加入者侧 `dc.send` 给房主；`_projectLocalToRoom` 仅在引擎 playing 时返回 view
- `web/src/stores/webrtc.ts`：信令 WS（`/ws/signal`）+ PeerConnection + DataChannel + `_pendingIce` + `_tmpHandlers`
- `web/src/stores/localGame.ts`：浏览器内 Go/WASM 引擎驱动（P2P 房主用它做权威）
- `web/src/config/rules.ts`：预设（济南/济宁/淄博/山东二五八）+ 牌池构建 `buildPool` / `poolTotal` / `COUNT_OPTIONS`
- `web/src/components/`：`CreateRoomScreen.vue`、`WaitingRoom.vue`、`MahjongTable.vue`、`TilePoolModal.vue`、`GameResultModal.vue`、`ThemeSwitcher.vue`

后端关键点：
- `internal/game/rules.go`：`RuleSet` 含 `Players`、`Pool map[string]int`（牌种→每种张数）、各种玩法开关；`RulesFromPool` 由牌池推导
- `internal/game/match.go`：`deal()` 按 `rules.Players` 发 13×n（庄家 +1）；`settle()` 分 4 人 / 2-3 人两种计分
- `internal/room/room.go`：`StartMatch` 要求满座（**已去掉"每个人都要 ready"的限制**，因为简化 UI 没有准备按钮）
- `internal/httpapi/ws.go`：`opRoomCreate` 接受 `players` / `presetId` / `pool`
- `internal/httpapi/static.go`（新增）：`WithStatic(root, next)` 单进程托管前端（文件命中直出、未知路径回退 index.html、API/WS 透传、目录穿越拦截、`/assets/` 长缓存）——**部署就靠它**
- `internal/sig/`：P2P 信令（6 位房间码、HMAC-SHA256 房间密码、限流、空闲清理），挂 `/ws/signal`

---

## 3. 已完成阶段（git log）

```
685e4ab 简化前端为名字+创建房间两屏，房间人数/玩法预设透传，满座即可开局
0c5fea5 feat(stage6): P2P 与局域网联机 (WebRTC 星形拓扑 + 信令服务 + 按座位私有视图)
9cb9204 stage5: 在线房间/大厅/邀请/准备/机器人/断线重连与幂等
7ae646f test(stage4): 单机 store 调度测试(假引擎注入) + 引擎可注入钩子
450daad feat(stage4): 单机可玩与三档 AI (Go 引擎编译为 WASM 在浏览器内权威运行)
```

**提交状态**：截至 `5ffd14c`（阶段 9）工作树干净；本次域名迁移的改动（`docs/HANDOFF.md`、`docs/architecture.md`、`docs/阶段9-安全性能与适配测试报告.md` 的地址/部署说明，以及 `server/internal/sig/handler.go` 注释里的示例域名）随后一并提交。

---

## 4. 线上部署现状（已验证可用）

| 项 | 值 |
|---|---|
| 访问地址 | <https://mj.wole110.xyz/>（根路径） |
| 旧地址 | `https://tsapi.wole110.xyz/mj/*` → 301 到 `https://mj.wole110.xyz/*`（Caddy 里 `handle_path` 去前缀后重定向） |
| 主机 | 阿里云 `root@<服务器IP>`，密钥 `<本地SSH私钥路径>`（真实值见 deploy/deploy-local.md，不入库），Debian 12 x86_64 |
| DNS | `mj.wole110.xyz` A → `<服务器IP>`（阿里云添加；`tsapi.wole110.xyz` 是听书项目专用域名，勿再复用） |
| 后端监听 | `127.0.0.1:8790`（**8080 被 Steam 占用，千万别用**） |
| 进程守护 | systemd `majiang.service`（`systemctl status/restart majiang`） |
| 静态资源 | 由 Go 后端直接托管（`MAJIANG_WEB_ROOT=/opt/majiang/dist`），Caddy 全量反代 |
| 反向代理 | Caddy **跑在 Docker 容器 `caddy` 里**（`network_mode: host`，无端口映射）：配置文件 host `/opt/caddy/Caddyfile` 以 bind-mount 挂在容器 `/etc/caddy/Caddyfile`；binary 只在容器内（host 没有 `/usr/bin/caddy`）。改配置→ `docker exec caddy caddy validate/reload --config /etc/caddy/Caddyfile --adapter caddyfile`，或热加载 admin API `POST http://127.0.0.1:2019/load` |
| Caddy 相关片段 | `mj.wole110.xyz { encode zstd gzip ; reverse_proxy 127.0.0.1:8790 }`；tsapi 保留 `/apk/*`、听书后端与旧链接 301 |
| 数据库 | `/opt/majiang/data/majiang.db` |
| 实测（2026-10-25） | `https://mj.wole110.xyz/` 200、`/api/v1/health` 200、`/assets/index-*.js` 200、`/engine/engine.wasm` 200(5,015,225B)、WS `/ws` 与 `/ws/signal` 带 `Origin: https://mj.wole110.xyz` 均 101、带 `Origin: https://evil.example` → 403、邀请链接为 `https://mj.wole110.xyz/?join=XXXXXX`、P2P 双标签 2/2 到齐、在线房创建成功 |

systemd 单元关键环境变量（`/etc/systemd/system/majiang.service`）：

```
Environment=MAJIANG_HTTP_LISTEN=127.0.0.1:8790
Environment=MAJIANG_DB_PATH=/opt/majiang/data/majiang.db
Environment=MAJIANG_ALLOWED_ORIGINS=https://mj.wole110.xyz,https://tsapi.wole110.xyz
Environment=MAJIANG_MAX_ONLINE_ROOMS=300
Environment=MAJIANG_WEB_ROOT=/opt/majiang/dist
Environment=MAJIANG_LOG_LEVEL=info
Environment=MAJIANG_DIY_DIR=/opt/majiang/data/diy
Environment=MAJIANG_DIY_PASSWORD=<本地填写，不入库>
```

> DIY 管理密码不入库，真实值见本地 deploy/deploy-local.md（首次启动时被 HashPassword 固化进 `diy_settings.password_hash`，之后再改 `MAJIANG_DIY_PASSWORD` 不会覆盖库里的值——要改密码请走设置页「🔑 修改密码」，或手动删掉该行后重启）。单元文件已纳入版本库：`deploy/majiang.service`。

> 注意：试过 `MAJIANG_LISTEN` 不生效，**正确名是 `MAJIANG_HTTP_LISTEN`**；`MAJIANG_ALLOWED_ORIGINS` 支持逗号分隔多值。

### 重新部署（可复现，2026-10-24 实测走通）

当前线上版本（2026-10-27 阶段 7）：后端 `md5=def0896e640bc2edd591c1e91bc71a79`（12,005,536 B；房主任期 `hostEpoch` + 迁移日志 + 断线自动交棒 + 座位转托管/让位 + 开局失败先广播名单），前端 `dist/assets/index-CElwIIiq.js`(237,359 B) + `index-Czq1nfXg.css`(66,659 B)（**根路径 base**，含返回键软守卫、刷新自动回房、房主换人提示、WebAudio 音效与「音效: 开/关」开关，`engine/engine.wasm` 5,067,971 B）。上一版为阶段 11：后端 `md5=9d92548429eff0f39e1ff9e3654e66a5`（11,997,344 B），前端 `index-I2cTMo-p.js`(227,833 B) + `index-CMa1tncT.css`(66,096 B)。systemd 单元新增 `MAJIANG_DIY_DIR=/opt/majiang/data/diy` 与 `MAJIANG_DIY_PASSWORD`（值不入库，已归档为 `deploy/majiang.service`）。已在公网核验：`/` 200、`/api/v1/health` 200、`/api/v1/diy` 200、`/assets/index-CElwIIiq.js` 200、`/assets/index-Czq1nfXg.css` 200、`/tiles/classic/wan1.svg` 200、`POST /api/v1/diy/auth` 正确密码 200 / 错误 401、`systemctl is-active majiang` = active；真实浏览器线上实测：建房 3 电脑开局 → 返回键回等待房间（「回到牌桌」可回）→ 再返回回首页（「你还在房间 …／继续对局」）→「继续对局」直接回牌桌且对局仍在进行。

上一版（2026-10-27 阶段 10）：后端 `md5=23790f3e340158c51beb004ffcb14d63`，前端 `dist/assets/index-C3N2v54d.js` + `index-CXBg7pbJ.css`。

```powershell
# 0) 运行时（HANDOFF 旧路径 D:\SOFT\appCoTools\... 已失效，以下为真实路径）
$node='C:\Users\<用户名>\.dsh\dsh-runtimes\dsh-next-primary-runtime\dependencies\node\bin\node.exe'
# 注意：pnpm.mjs 内部要 spawn 'node'，必须先把 node\bin 放进 PATH，否则 ENOENT
$env:Path='C:\Users\<用户名>\.dsh\dsh-runtimes\dsh-next-primary-runtime\dependencies\node\bin;D:\SOFT\go\bin;'+$env:Path

# 1) 后端交叉编译（Windows → linux/amd64）
cd D:\PandV\D\CONprogram\woleMajiang\server
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
go build -trimpath -ldflags '-s -w' -o majiang-server-linux ./cmd/server

# 2) 上传 + 重启（先备份）
$key='<本地SSH私钥路径>'; $srv='root@<服务器IP>'
scp -i $key majiang-server-linux "${srv}:/opt/majiang/majiang-server.new"
ssh -i $key $srv "cp /opt/majiang/majiang-server /opt/majiang/majiang-server.bak && chmod +x /opt/majiang/majiang-server.new && mv /opt/majiang/majiang-server.new /opt/majiang/majiang-server && systemctl restart majiang && systemctl is-active majiang && curl -s http://127.0.0.1:8790/healthz"

# 3) 前端构建（根路径部署：**不要**再带 --base=/mj/） + 上传 dist
cd ..\web
node C:\Users\<用户名>\.dsh\dsh-runtimes\dsh-next-primary-runtime\dependencies\pnpm\bin\pnpm.mjs run build   # vue-tsc -b && vite build（base 默认 '/'）
ssh -i $key $srv "rm -rf /opt/majiang/dist.new /opt/majiang/dist.old"
scp -i $key -r dist "${srv}:/opt/majiang/dist.new"
ssh -i $key $srv "cd /opt/majiang && mv dist dist.old && mv dist.new dist && systemctl restart majiang && systemctl is-active majiang"
```

> 关键：`web/vite.config.ts` 的 `base` 取 `process.env.MAJIANG_BASE || '/'`。**线上是根路径部署，用默认 `pnpm build` 即可**；只有将来要放回某个子路径（如 `/mj/`）时才用 `build:mj`（`vue-tsc -b && vite build --base=/mj/`）或 `MAJIANG_BASE=/mj/`。P2P/局域网依赖 `dist/engine/engine.wasm`，**scp 时别漏 `engine/` 目录**。

### 阿里云域名的坑（已踩过）

- `mj.wole110.xyz` 单独一条 A 记录 → `<服务器IP>`；`tsapi.wole110.xyz` 属听书项目，只保留旧链接 301。
- 阿里云安全组只放行 80/443 等少数端口（试过 8088：外网连不上，内网通）。
- 因此线上只能走域名 80/443；前端所有请求经 `web/src/api/client.ts` 的 `basePath()`（REST + WS + 信令三处），根路径部署时它是空串。

---

## 5. 本地开发与验证命令

```powershell
# 后端（本地 8790，避开 Steam 占用的 8080）
$env:Path='D:\SOFT\go\bin;'+$env:Path
cd D:\PandV\D\CONprogram\woleMajiang\server
$env:MAJIANG_HTTP_LISTEN='127.0.0.1:8790'
$env:MAJIANG_MAX_ONLINE_ROOMS='300'
go run ./cmd/server            # 或 go build -o majiang-server.exe ./cmd/server 后启动

# 前端 dev（必须 --host 0.0.0.0，否则只监听 IPv6 [::1]，浏览器 127.0.0.1 打不开）
cd ..\web
$env:MAJIANG_API='http://127.0.0.1:8790'
node "D:\...\pnpm\bin\pnpm.mjs" dev --host 0.0.0.0 --port 5173

# 三绿（前端）
node "D:\...\pnpm\bin\pnpm.mjs" exec vue-tsc -b    # EXIT=0
node "D:\...\pnpm\bin\pnpm.mjs" test               # 36 例全过
node "D:\...\pnpm\bin\pnpm.mjs" build

# 三绿（后端，最后一次实测全部 ok）
cd ..\server
go build ./... ; go vet ./... ; go test ./...
#   ai / config / game / httpapi / local / protocol / ratelimit / room / store 全 ok
```

E2E 脚本（需要后端在跑）：
- `node scripts/e2e-ws.mjs`（12 项握手/房间/重连/限流）
- `node scripts/e2e-match.mjs`（引擎整局）
- `node scripts/e2e-online.mjs`（4 客户端真实联机整局 + 断线重连 + 解散）
- `node scripts/sig-signal.mjs`（P2P 信令房间创建/加入）
- `node scripts/measure-load.mjs`（资源实测：空载 RSS 12.4MB；30 房最坏 22.2MB、CPU≈4.6% 单核）

---

## 6. 关键架构事实（改代码前必读）

1. **后端权威**：在线模式一切以 Go 引擎为准；P2P 模式以房主浏览器内的 WASM 引擎为准（`localGame`）。
2. **事件序号**：服务器侧所有事件带单调 `seq`；客户端 `lastSeq`，重连走 `opResync{lastSeq}` 增量回放 + 私有视图补发。
3. **私有视图**：`view` 是按座位投影的（别人手牌只有 `tileCount`）。P2P 房主必须**按座位单播** `Ev.RoomState`/`Ev.GameEvent`，`_peerBySeat` 记录 seat→peerId；私有视图有 300ms 泵。
4. **动作幂等**：客户端 `actionSeq` 自增，服务端按 `{guestId, actionSeq}` 去重。
5. **牌池**：`RuleSet.Pool` 是 `{"wan1":4,...}` 形式，引擎按它建墙；`rules.Players` 决定人数与发牌。二者前端都可自选，经 `opRoomCreate{players,presetId,pool}` 传后端。
6. **P2P 信令**：`/ws/signal`，op 有 `room.create / room.join / room.leave / room.list / room.peers / signal.offer / signal.answer / signal.ice`；**DataChannel 由加入者创建**（房主等 `ondatachannel`），否则 SDP 无 `m=application`。
7. **满座即开局**：没有 ready 按钮了；房主点"开始"要求座位满（AI 可补位）。
8. **UI 事件落点**：所有上行都走 `conn`（`createRoom/joinRoom/startMatch/gameAction/setBot/leaveRoom`），P2P 时内部自动改走 DataChannel。

---

## 7. 当前未完成 / 已知问题（按优先级）

### F. ✅【阶段 10 已完成】规则细则（响应倒计时 / 优先级仲裁 / 截胡截碰 / 出牌倒计时）

用户诉求：碰杠要等、胡要等，但不想被"点错就没了"坑；响应窗口不想逐家点「过」；希望有截胡/截碰的正式判定而不是"谁手快谁得"；出牌方挂机要有倒计时。

**引擎层（`server/internal/game/`，服务端房间 / WASM 单机 / P2P 三处共用）**
- `rules.go`：`RuleSet` 新增 `RespondMeldSec`（碰/杠/吃，默认 3）、`RespondWinSec`（荣和，默认 5）、`DiscardSec`（出牌，默认 0=不限）、`RobWin`/`RobMeld`（默认 true）；`Validate` 校验 0~30 / 0~120（非 0 时 ≥3）；`RespondEnabled()`、`ResponseSecondsFor(actions)`（同人可胡可碰取较长）。
- `match.go`：新增 `respClaims [4]*respondClaim`（`pri`/`sub`/`seq`/`run`/`with`/`shape`）、`respDeadlines [4]time.Time`、`claimState [4]string`（pending/success/failed）、`claimSeq`、`discardDeadline`、可注入 `clock`。
- `actions.go`：`Discard` 后 `resetResponseLocked()` —— **只给"真正能吃到/碰到/杠到/胡到"的座位开计时窗口**，没有真实选择的座位直接 `respAnswered=true`（自动过，不再要玩家点「过」）；响应动作变成"提交 claim"（`respondClaimOf`），由 `settleLocked()` 统一仲裁：优先级 `priWin=3 > priMeld=2 > priChow=1`，同优先级按离出牌者的座次顺序；`RobWin=false` / `RobMeld=false` 时退化为"先按者得"（先提交的立刻成立）；仲裁期间其余 claim 收到 `failed` 判定事件；`ForceDraw(seat)` 让下家在**未启用响应倒计时**时点「摸牌」直接收敛窗口；`Tick(now)` 统一处理响应超时（自动过）与出牌超时（优先打刚摸的牌，否则打最右一张）；`EvJudge="judge"` 事件承载判定过程。
- `view.go`：`SeatView` 增 `respondSecLeft` / `respondDeadline` / `discardSecLeft` / `discardDeadline` / `claimStates` / `forceDraw`；`AvailableActions` 响应阶段只给有真实选择的座位（含 `pass`），`draw` 不进 `Actions`（避免 AI 误选，改用 `forceDraw` 标记）。
- `local/table.go`：`case "forcedraw"` + `Tick(now)`，WASM 桥新增 `tick(nowMs, since)` 导出。

**房间层**
- `room/autoplay.go` 完全重写：先 `match.Tick` 循环（倒计时），再逐家驱动机器人，最后用 `nextStalledLocked`（`lastActivity + DefaultTurnTimeout`）兜底掉线/挂机；`CurrentTurn()` 返回最早到期的响应座位，用于房间级广播。
- `room.go`：`commitLocked(res)` 去掉 seat 参数，只同步 `Status`/`turnDeadline`；`StartMatch` 重置 `actionSeq` 并允许 `finished` 后重开。
- `httpapi/rules_patch.go`（新文件）：`ruleSetPatch` 全字段用指针，`apply(base)` 逐字段合并 —— **原因**：`opRoomCreate` 原先整体覆盖 `RuleSet`，旧客户端不发的阶段 10 字段会被清零。
- `protocol.go`：`RoomStateView.Rules *RulesView`，前端房间页可展示本局细则。

**前端**
- `config/rules.ts`：`RuleOptions` / `DEFAULT_RULE_OPTIONS` / `presetRuleOptions`；`CreateRoomScreen.vue` 新增「更多规则设置」二级面板（计时 / 优先级 / 和牌 三个 tab，档位按钮 + 开关 + 底分滑杆，标题行实时摘要）。
- `engine/engine.ts`：`RulesConfig` 补五个字段 + `tick()`；`stores/localGame.ts` 加 `_tickTimer`（200ms）驱动倒计时、`forcedraw` 分支。
- `ActionControls.vue`：倒计时胶囊（≤2s 变红脉冲）+「摸牌」按钮；`MahjongTable.vue`：对手名牌「判定中/判定成功/判定失败」徽标、中央「等待 X 出牌 · Ns」、「判定中… 等待其他家确认」提示、全屏按钮。

**验证**：`go build ./...` / `go vet ./...` / `go test ./...` 全绿（新增 `TestRobWinOrder`、`TestRespondCountdownAutoPass`、`TestRespondCountdownPrefersWin`、`TestDiscardCountdownAutoDiscards`、`TestForceDrawWhenNoRespondTimer`、`TestClaimStatesPendingSuccessFailed`、`TestRuleSetTimingValidation`、`TestPriorityWinOverPong`）；前端 `vue-tsc -b` + `pnpm test` 38/38；本地与线上浏览器实测（见 §4 版本行）。

### G. ✅【阶段 11 已完成】时序番型 / 副露多选 / DIY 牌面主题

用户诉求（原文见 m02323）：(1) 多个「碰」时弹出的选择框点了有时无效，要优化多选碰/吃/杠；(2) 加抢杠胡、杠上开花、海底捞月这类"时序型"番型；(3) 加第三套主题 DIY——首页左边缘小齿轮 → 密码门（无账号只输密码）→ 以第一套主题为模板逐张改牌面（上传图片 + 选填充模式 + 预览 + 保存），打牌时选 DIY 即可看到，所有玩家共享；设置页还要能改密码。

**1) 时序番型（`server/internal/game/`，三处引擎共用）**
- `rules.go`：`RuleSet` 新增 `RobKongWin` / `KongFlower` / `LastTile`（均默认 true）。
- `match.go`：新增 `kongFlower`（本次摸牌来自杠后补牌）、`lastDrawTile`（牌墙已空时摸到的最后一张）、`riverEnd`（出牌后牌墙为空）、`winRobKong` / `winFan` / `winPay` / `winFlags`，以及 `pendingKong *pendingKong{seat,tile,added}`；`pendingDiscard` 增 `robKong bool`；`lastDiscard` 字段改名 `lastDiscardSeat`（与新增的布尔量区分）。
- `actions.go`：
  - **抢杠胡**：`KongSelf` 补杠分支先校验（有对应 MeldPong + 手牌有第 4 张），若 `rules.RobKongWin && canRobKong(seat,tile)`（其他家有能和的牌）则挂起抢杠窗口（`turnStage=stageRespond`、`respPending={by:seat,tile,robKong:true}`），否则直接 `executeAddedKongLocked`。`closeWindowLocked()` 增 `pendingKong != nil` 分支：无人抢则补杠继续。`ForceDraw` 在抢杠窗口拒绝（该窗口没有"摸牌"语义，否则下家一点就替别人放弃抢杠）。
  - **杠上开花 / 海底捞月**：`Draw()`、`afterKongDraw()` 维护 `kongFlower` / `lastDrawTile`；`Discard()` 维护 `riverEnd`。
  - **结算**：`resolveWin` 重写番数计算——基础 1 番「和牌」+ 七对子 +2 + 自摸 +1 + 非七对门清 +1；时序番**互斥只计最高一个**：抢杠胡 +2 / 杠上开花 +2 / 海底捞月 +2（自摸最后一张）/ 河底捞鱼 +2（胡别人最后一张出牌）。结果写入 `m.winFan/winPay/winFlags`，EvWin Extra 增 `win_flags`。
- `view.go`：`ResultView` 增 `Flags []string`；`fanOf/payOf` 直接读 `m.winFan/winPay`（**原因**：原先视图层另算一遍，加番后容易与结算实际扣分不一致）。
- `ai/ai.go`：`decideRespond` 最前面加 `hu.robbing` 判定（原来 AI 永不荣和）。
- 透传：`protocol.RulesView` 三个新布尔 → `room.viewLocked` → `httpapi/rules_patch.go` 指针合并；前端 `config/rules.ts`、`engine/engine.ts`、`protocol/types.ts`、`CreateRoomScreen.vue`（「和牌」tab 三个开关）、`WaitingRoom.vue`（规则摘要）、`GameResultModal.vue`（番种明细胶囊）。
- 测试：`TestRobKongWin`、`TestRobKongWinDeclined`、`TestKongFlowerFan`、`TestLastTileFan`、`TestWinFlagsSwitchable`。

**2) 副露多选交互（`web/src/components/ActionControls.vue` 重写，约 490 行）**
- **根因**：原吃牌弹窗是 `absolute bottom-full` 浮层，挂在 `.mahjong-table`（`overflow-hidden`）内部 → 被裁掉、点击落到下层。这正是"点了没反应"。
- 改为 `<Teleport to="body">` + `fixed inset-0 z-50` 遮罩面板，绕开任何祖先裁剪；统一 `chow` / `kong.concealed` / `kong.added` 三种多选（原来暗杠/补杠只取候选第 0 个）。
- 新增 `splitCode`（正则拆花色数字，修掉 `parseInt(slice(-1))` 对两位数字解析错）、`handCounts` 多重集（修掉"同种牌只有 1 张也给出组合"）、`submitting` 900ms 提交锁（防连点发两次）、`watch` 失效自动收起（动作已不在 actions / 候选降为 ≤1 / 倒计时归零）。
- 面板内显示候选牌张、说明与倒计时胶囊（≤2s 变红脉冲）；单候选仍直接执行。
- 测试：`TableComponents.test.ts` 新增「多候选吃牌点第 2 项 / 点第 1 项 / 两张暗杠候选点第 2 项」。**注意**：Teleport 到 body 的节点要求 `mount(..., { attachTo: document.body })`，且触发后要 `await nextTick()`。

**3) DIY 牌面主题**
- 后端：
  - 迁移 `server/internal/store/migrations/0003_diy_theme.sql`：`diy_tiles(code, file_name, fit, updated_at)` + `diy_settings(key, value, updated_at)`。
  - `store/diy.go`：`ListDIYTiles` / `GetDIYTile` / `SaveDIYTile`（返回旧文件名）/ `DeleteDIYTile` / `DIYPasswordHash` / `SetDIYPasswordHash`；`NormalizeDIYFit` 非法值退回 `cover`。
  - `config.go`：`DIY{Dir, MaxImageBytes, Password}`，env `MAJIANG_DIY_DIR` / `MAJIANG_DIY_MAX_IMAGE_BYTES` / `MAJIANG_DIY_PASSWORD`；`Validate()` 校验目录与 ≥64KB。
  - `game/model.go`：`ThemeDIY Theme = "diy"`，`Valid()` 接受三个主题。
  - `httpapi/diy.go`（356 行）：`GET /api/v1/diy`（公开，返回 tiles/passwordSet/fits/maxBytes）、`POST /api/v1/diy/auth`、`POST /api/v1/diy/password`（未设过密码时允许空 currentPassword；新密码 4~64）、`POST /api/v1/diy/tiles`、`POST /api/v1/diy/tiles/{code}/delete`、`GET /api/v1/diy/images/{name}`（`ServeContent` + 一年 immutable 缓存）。
  - 安全细节：`effectiveDIYPasswordHash` 首次启动把 env 密码 HashPassword 后固化进库；`checkDIYPassword` 对空 hash 一律拒绝（**不能依赖 `secret.VerifyPassword` 的空 hash 放行**）；图片名由服务端按纳秒时间戳生成 + 内容嗅探扩展名（PNG/JPEG/WEBP/GIF 文件头），无路径穿越；base64 上传单独放宽到 `MaxImageBytes/3*4 + 64KB`（`decodeJSON` 卡 1MB 不够）。
  - 测试 `httpapi/diy_test.go` 5 个：初始为空 / 密码全流程 / 保存删除（含错误密码 401、未知牌 400、非图片 400、`..%2f..%2fetc%2fpasswd` 404）/ fit 回退。
- 前端：
  - `stores/theme.ts`：`ThemeId = 'classic' | 'ink' | 'diy'`，导出 `THEME_IDS` / `THEME_NAMES` / `isThemeId`；`ThemeSwitcher.vue` 三个按钮。
  - `stores/diy.ts`（新）：`tiles` / `passwordSet` / `fits` / `maxBytes`；`refresh()` / `applyTile()` / `dropTile()` / `invalidate()`。
  - `components/DIYThemeSettings.vue`（新，约 480 行）：34 张牌分 5 组；点牌开二级窗口（上传 / 5 种填充方式 / DIY 与经典对照 / 牌桌三种尺寸实景预览 / 恢复默认 / 保存）；「🔑 修改密码」；「在牌桌预览」。
  - `MahjongTile.vue`：DIY 分支按 `code` 取自定义图 + `object-fit`，未自定义的牌回退经典。
  - `App.vue`：首页左边缘齿轮（`aria-label="DIY 牌面设置"`，**`dsh_browser_click` 用 text 匹配不到，要用 CSS 选择器**）→ 密码门 → `screen='diy'`。
- **实测中修掉的两个真 bug**（都会表现成"改了但牌桌没变"）：
  1. `App.vue` 的 `onMounted` 从未调 `diy.refresh()` → 直接选 DIY 打牌时 store 为空、全部回退经典。已加 `void diy.refresh()`。
  2. `MahjongTile.vue` 的 `diyFailed` / `imgFailed` 是一次性布尔，图片失败后永不复位 → 换图后仍停在经典。改为记录失败时的 URL（`diyFailedUrl` / `imgFailedPath`）并用 `diyBroken` / `classicBroken` 计算属性判断"当前 URL 是否失败过"，换 URL 自动恢复。回归测试见 `MahjongTile.test.ts` 两个新用例。

**验证**：`go build ./...` / `go vet ./...` / `go test ./...` 全绿；`vue-tsc -b` 无输出；`pnpm test` **43/43**；`pnpm build` 成功。线上 `https://mj.wole110.xyz/` 实测：DIY 密码门（错误密码拒绝 / 正确密码通过）→ 设置页 34 张牌 → 建房 3 电脑 → 打完整局到结算（含番种显示）→「再来一局」正常开新局。

### H. ✅【阶段 7 已完成】房主迁移与断线恢复 + 音效

用户诉求（原文见 m04197 / m04203）：按 `docs` 里阶段 7 提示词实现房主迁移与断线恢复，并点明三个实际 bug——「房主一按返回键，回到创建房间页面就回不来了」「房主还不会迁移」「返回的人还会一直占着房间位置，导致无法继续游戏」；另加「一部分游戏音效，现在下牌太突兀了」。

**0) 范围决策（重要，别推翻）**
- **在线模式完整实现**：对局状态本来就在服务端（房主只是「开局/改设置/解散」权限），所以房主迁移**不需要搬运任何隐藏信息**，也不存在牌墙泄漏问题。
- **P2P 中途换主机不做"真恢复"**：权威引擎（牌墙、各家手牌）只活在房主浏览器（`server/cmd/wasm/main.go` 暴露的 `WoleEngine`），中途接管需要 seed + 完整操作日志重放，且无法证明新房主的牌墙没被篡改。阶段提示词允许"无法可信恢复则明确终止"，因此 P2P 只在**大厅/已结束**阶段交棒；对局中失去房主时前端明确弹窗终止并提示重开（`stores/connection.ts` 的 `hostLost` + `p2p_host_lost` 文案 + `App.vue` 的「对局已中断」弹窗）。

**1) 服务端：任期、迁移日志、选举、托管、让位**
- `server/internal/room/migrate.go`（新）：
  - 常量 `HostReconnectGrace = 15s`（房主断线多久后交棒）、`SeatReclaimGrace = 60s`（掉线玩家多久转机器人托管）。
  - 迁移种类 `MigrateHostLeave="host.leave"` / `MigrateHostDisconnect="host.disconnect"` / `MigrateHostReap="host.reap"` / `MigrateHostSeatBot="host.seat_bot"`；`MigrationEntry{At,Kind,Epoch,From,To}`，`migrationLogLimit=32`（可 `room.Migrations()` 查）。
  - `electHostLocked()`：在线真人中座位号最小者接任，退化为任意真人，无真人返回 nil。
  - `promoteHostLocked(kind)`：**只有房主真的变了才 `hostEpoch++` 并写日志** —— 否则每秒扫描都会 bump 任期，客户端"房主变化"提示会疯狂刷屏。
  - `tickHostLocked/tickHost`：房主断线超 `hostGrace` 才迁移；`reclaimSeatsLocked`：对局中掉线超 `seatGrace` 的真人 `IsBot=true`（**转托管而不是删座位**，删座位会让引擎座位映射错位）。
- `room.go`：`Room` 增 `hostEpoch int` / `migrations []MigrationEntry` / `hostGrace, seatGrace time.Duration`（后两个供测试做故障注入）；`SetConnected` 重写为「连接状态变化 **或** 回到座位时 `IsBot` 为真」都要处理，`if connected && p.IsBot { p.IsBot = false }`（重连自动收回托管）；**旧房主回来不抢任期**。
- `registry.go` 的 `Tick` 顺序：`AutoStep` → `ReclaimSeats` → `TickHost` → `reapDisconnected` → `PurgeLeft`（顺序有意：先把"没人在"的座位交给机器人，再判断房主还在不在）。
- **"离开的人一直占座"的修法**：`Player.Left` 标记主动离开 → 对局中不删座位只转托管；`HasPlayer` 改为 `ok && !p.Left`（否则 `FindByGuest` 会把刚离开的人抓回牌局）；`purgeLeftLocked()` 只在**非对局中**才真正删座位，`PurgeLeft()` 由注册表 tick 周期调用（一局结束后立刻让位），`join()`/`StartMatch()` 开头也各清一次。
- `httpapi/ws.go`：`opGameStart` 失败时先 `broadcastRoom(rm)` 再报错 —— 开局失败常常正是因为刚把离开的座位清掉、人数不够，不广播的话房主看到的是「名单还是 4/4、按钮点了没反应」。
- 测试 `room/migrate_test.go` 9 个：立刻迁移 / 断线宽限后迁移 / 旧房主不回抢 / 座位转托管 / 房主被托管也迁移 / 对局中离开转机器人 / 一局结束后让位 / 注册表 tick 让位并广播。

**2) 前端：返回键、自动回房、房主提示**
- **返回键不再直接离开 SPA**：`App.vue` 里 `history.pushState({majiang:'room'}, '')` 武装软守卫 + `popstate` 处理。牌桌按返回 → 回等待房间；等待房间/建房/加入/DIY 按返回 → 回首页且**房间保留**，顶部胶囊提示「房间仍为你保留 —— 点「继续对局」随时回来」。
- **"回不来"的两个真根因**（都在 `App.vue`）：
  1. `watch(() => conn.room)` 原来在 `screen === 'home'` 时也会自动切回房间，而服务端每次 `room.state` 都是新对象 → 用户刚按返回就被拽回房间，以为返回键没反应。改为只有 `explicit`（`create`/`join`）或「在首页但没被"留在首页"标记」时才切。
  2. `watch(() => match.view)` 在对局中收到新事件时会把首页弹回牌桌 → popstate 里置 `leftTable = true` 挡住。
- **刷新/重连自动回房**：`sessionStorage` 存 `majiang.session.room`（code/name/transport），`onMounted` 走 `tryResumeSession()`；`connection.ts` 的 `Ev.RoomState` 分支在 `status === 'playing' && !match.view` 时补一次 `resync()`（服务端握手本来就会推回房间状态，但没有 resync 就拿不到牌桌视图）。
- **房主换人提示**：`watch(conn.room?.hostEpoch)`，任期变大才提示；自己是新房主时给「原房主已离开，你已成为新房主（可以开局 / 添加机器人）」。
- 首页新增「你还在房间 X」卡片（`继续对局` / `离开房间`）。

**3) 音效（`web/src/utils/sfx.ts`，纯 WebAudio 现场合成，无音频文件）**
- `sfxDiscard()` 特意用低频柔和落桌声（白噪声 0.055s / lowpass 2200 + 320Hz triangle），因为用户反馈"下牌太突兀"；另有 `sfxDraw/sfxMeld/sfxKong/sfxWin/sfxDrawWall/sfxPass/sfxYourTurn/sfxTap`。
- 开关在 `ThemeSwitcher.vue`（「音效: 开/关」），偏好存 `localStorage['majiang.sound.enabled']`（`'true'`/`'false'`）；AudioContext 在首次 `pointerdown` 才 `unlock()`。

**验证**：`go build/vet/test ./...` 全绿；`vue-tsc -b` 无输出；`pnpm test` **46/46**（新增 `sfx.test.ts` 3 个 + `migrate_test.go` 9 个）。浏览器实测：房主按返回 → 首页且房间保留 →「继续对局」回房 ✅；房主「离开房间」→ 对家立刻变房主且座位空出 ✅；对局中玩家离开 → 转机器人、牌局继续 ✅；对局中刷新 → 自动回牌桌（含手牌与操作按钮）✅。

### A. ✅【已定位并修复】「信令连接失败」

**根因**：`server/internal/sig/handler.go` 的包级 `var upgrader` 内置了**硬编码** `CheckOrigin`（只放行 `localhost/127.0.0.1/0.0.0.0/192.168./10./172.16~19/172.2/172.3` 前缀），**不读** `MAJIANG_ALLOWED_ORIGINS`。浏览器带 `Origin: https://tsapi.wole110.xyz` 的握手被 403 拒绝 ⇒ P2P/局域网（都走 `/ws/signal`）在生产环境 100% 不可用；在线服务器走 `httpapi/ws.go` 的 `IsOriginAllowed`，所以一直正常。
（教训：curl 探针**不带 Origin** 会拿到 101，从而误判"信令健康"——必须带浏览器真实 Origin 复测。）

**修复**：`sig` 按请求注入 origin 校验，顺序 ① 无 Origin（非浏览器客户端）放行 ② 与 `r.Host` 同源放行（反代后的公网域名走这条）③ 配置白名单 ④ 局域网私有网段前缀。`sig.Config.AllowedOrigins` 由 `httpapi` 注入（即 `MAJIANG_ALLOWED_ORIGINS`）；新增 `TestOriginAllowed` 11 例。
**线上验证**：带公网 Origin 的 WS 握手 101；带 `Origin: https://evil.example` 403。
**域名迁移后复测（2026-10-25）**：`Origin: https://mj.wole110.xyz` → 101，`Origin: https://evil.example` → 403；浏览器内 P2P 建房/加入正常。

以下为当时的原始记录（现象已属 A 的一部分，保留作历史）：

现象：在 `https://tsapi.wole110.xyz/mj/` 第二个标签用邀请链接进入加入屏，点「加入」后页面显示 **信令连接失败**（该文案只在 `web/src/stores/webrtc.ts:104` 的 `connectSignaling` 超时分支出现）。

可能原因（按概率）：
1. 我的自动化点击误点到了 `P2P` / `局域网` 交通方式按钮（`joinTransport` 默认 `'server'`，见 `App.vue:39`），于是真的走了 WebRTC；此时若 `wss://tsapi.wole110.xyz/mj/ws/signal` 升级失败就会报这个错。
2. Caddy `reverse_proxy` 未正确升级子路径 WebSocket（一般不会，但需实测）。
3. 信令 WS 建连后 10s 内没等到带 `peer` 的 `room.join.ok`（后端首帧回 `OpJoinOK` 携带 peerId）。

**排查动作（建议新对话第一件事）**：
```js
// 浏览器控制台/execute
const proto = location.protocol === 'https:' ? 'wss' : 'ws';
const ws = new WebSocket(`${proto}://${location.host}/mj/ws/signal?peer=probe-1`);
ws.onopen = () => console.log('sig open');
ws.onmessage = (e) => console.log('sig msg', e.data.slice(0, 120));
ws.onerror = (e) => console.log('sig err', e);
```
同时确认：`joinTransport.value` 当前值、`conn.status`、`conn.lastError`。
（生产构建里 `document.querySelector('#app').__vue_app__._instance` 为 `null`，拿不到 setupState；调试可以用 dev server 或临时 `window.__dbg = { conn, wrtc, ... }`。）

### B. ✅【已用真实 WebRTC 通过】P2P / 局域网；真双设备 + TURN 仍待补

**本次实测（生产环境，真实 WebRTC + 公网信令）**：
- **P2P 2 人整局**：房 `M26ZWP`，丙(房主·东)/丁(南)。两侧手牌互不可见、剩余牌墙一致；丁自摸 3 番 8 分，结算 `东·丙 −8 / 南·丁 +8`，两侧完全一致。
- **局域网 3 人整局**：房 `QUN8HK`（lanOnly，仅 host candidate，不经 STUN/TURN），戊(东)/己(南)/庚(西)；己 2 番 4 分，`戊 0 / 己 +4 / 庚 −4`，三个标签一致。

**本次修掉的两个设计级缺陷**（详见 `architecture.md` §12.2/§12.3）：
1. P2P 房主原本用 `local.start({ players })`，开出来是「1 真人 + AI」单机局，远端加入者根本不是引擎座位，其动作会被当成房主自己的动作。现改为按 `room.players` 生成显式 `SeatSpec[]`（真人占真实座位、空位 AI 补足）交给 `localGame.start({ seats, seatOwners, hostSeat: 0 })`；远端动作经 `peerId → 座位`（`connection._seatByPeer`）反查后走 `local.actAs(seat, …)`；`localGame` 的自动推进改为「逐真人座位等待决策」，任一真人未决定时不替该座位出牌。
2. 房主从不把 `local.view` 灌进 `match` store，所以点开始后永远停在等待房。现由 `connection._syncHostView()` 在视图单播与 300ms 私有视图泵里同步。
3. 身份存储改 `sessionStorage`（每标签一份），同浏览器多标签不再互相覆盖身份（旧 `localStorage` 身份首次加载迁移一次）。

以下为原始记录（保留作历史）：

- 同一浏览器开两个标签**旧的**不可靠原因：两个标签共享 `localStorage['majiang.identity.v1']`，会互相覆盖身份——现已修复，见上。
- 曾用 dev server 双标签跑通过一局（房主 4 人 + 私有视图 + 开局），但体验"不稳定"：`createP2PRoom` / `joinP2PRoom` 偶尔超时（10s / 12s），ICE 在复杂网络下失败率高。
- **没有 TURN**：对称 NAT / 运营商级 NAT 下 P2P 必失败（提示词里 TURN 是可选项，尚未做）。想更稳可接 coturn 并在 `webrtc.ts` 里配 `turn:`。
- `lanOnly` 参数已传给 `createP2PRoom`，但**未验证**"两台同网段设备只走 host candidate"。

### E. ✅【阶段 9 已完成】安全、性能与适配

报告全文：`docs/阶段9-安全性能与适配测试报告.md`。要点：

- **可复现压测器**：`server/cmd/stress`（新）。`-mode normal`（多房多人自动对局 + RTT/动作延迟/广播延迟统计）、`-mode abuse`（8 项恶意输入探测）。采集脚本 `server/data/run-final.sh`。
- **实测（生产机 2 vCPU/1676MB，3 房 × 4 人 = 12 连接，240s）**：动作 615、消息 3198 入 / 3510 出、**错误 0**、CPU 均值 ≈0.5%（峰值 11.5% 仅启动瞬间）、RSS 15.0→20.8MB、load1 ≤0.07；动作视图延迟 p50 <1ms、广播延迟 p50 0.59ms。**瓶颈是回合往返与客户端节流，不是 CPU/内存。**
- **滥用探测 8/8 通过**：坏 token→`auth_failed`、首帧非 hello→`expected_hello`、2MB 帧→强制关闭、坏 JSON→`bad_message`、60 条/秒洪泛→`rate_limited`、无效房间码/邀请→`room_not_found`、超房间数→`max_online_rooms`。
- **已修并上线**：Caddy/Go 之前不下发任何安全头 → 新增 `X-Content-Type-Options`/`X-Frame-Options`/`Referrer-Policy`/`Permissions-Policy`（**静态资源由 `WithStatic` 在中间件之外直接返回，两处都要加**，已补单测）。
- **前端适配**：`MahjongTable.vue` 桌面高度改 `min(540px, calc(100dvh - 8rem))`，`@media (max-height: 640px)`（横屏）取消最小高度并允许内部滚动，`@media (pointer: coarse)` 加 `touch-action: manipulation`；动效开关已有。
- **未覆盖（如实）**：未跑到整局结算（240s 内 615 动作）、未在真 1 核 1GB 复测、断线恢复耗时未自动化、手机真机未验证、TURN 未评估、Caddy 压缩未核验。

### C. ✅【已跑通】2/3 人整局

- 2 人在线：房 `42B3UD`，标准形自摸 2 番，`东·甲 +4 / 南·乙 −4`，两侧一致（约 25 分钟自动对局）。
- 3 人在线：房 `U2U2Q4`（在线服务器模式，子/丑/寅三真人）同桌推进正常（牌墙 96→87、三方各自视角与手牌正确）；**未跑到结算**（长跑测试被叫停，结论按"能开局、能同桌推进"记，不夸大）。
- 3 人局域网：房 `QUN8HK`，见 §7-B。
- 后端单测一直绿：`server/internal/game/players_test.go`（2/3/4 人各 8 seed）、`room/match_test.go`、`room/autoplay_test.go`。

以下为原始记录（保留作历史）：

- 已绿：`server/internal/game/players_test.go`（2/3/4 人各 8 seed 随机对局：4000 步内终局、136 张守恒、零和、空位 0 分）、`room/match_test.go`（2/3 人开局）、`room/autoplay_test.go`（2/3 人托管到终局）。
- **未做**：前端浏览器里 2 人 / 3 人整局（在线模式）。建议用两个浏览器 profile + 创建 2 人房走一局（4 人已实测通过）。

### D. 杂项（已更新）

- ❌ **「删掉 engine.wasm 让产物变小」是错的**：P2P/局域网模式下房主浏览器要跑权威引擎，`web/src/engine/engine.ts:112` 从 `${import.meta.env.BASE_URL}engine/` 加载 `wasm_exec.js` + `engine.wasm`；删掉等于 P2P/局域网全废。见 `architecture.md` §12.6。
- ✅ `docs/architecture.md` 已补 P2P/LAN 第 12 节；README 阶段 6 现状已更新。
- ✅ 构建产物已清出仓库并写入 `.gitignore`（`majiang-server-linux`、`*.exe~`、`server/majiang-server-e2e.exe`）；`.gitignore` 已重写为纯 ASCII 注释版（原文件是 UTF-8/GBK 混合的坏文件）。
- ⚠️ `/mj/index.html` 返回 301（`http.ServeFile` 规范化跳转）→ 浏览器跟随到 `/mj/`，无实际影响。
- ⚠️ 房间状态全在**单进程内存**，重启即清空（SQLite 只存房间/邀请元数据）——阶段 7 处理。
- ⚠️ 加入方头部联机方式文案：局域网模式下显示 `P2P`（`transport` 只区分 `'ws' | 'p2p'` 两值），纯文案问题，不影响行为。

---

## 8. 后续任务清单（建议顺序）

1. ✅ **[P0] 提交当前改动** — 已提交 `b7fab68 feat: 牌集自选全链路 + 单进程静态托管 + 满座即开局`，工作树干净。
2. ✅ **[P0] 定位 §7-A「信令连接失败」** — 根因是 `sig` 包硬编码 origin 校验，已改为同源/白名单/局域网前缀三级放行并部署（见 §7-A）。
3. ✅ **[P0] P2P 验收** — 生产环境双标签真实 WebRTC，2 人整局到结算（见 §7-B）；顺带修掉「房主开单机局」与「房主不进牌桌」两个设计级缺陷。
4. ✅ **[P1] 局域网验收** — lanOnly（仅 host candidate）3 人整局跑通（见 §7-B）。仍建议补一次同网段真机。
5. 🟡 **[P1] 2/3 人整局** — 2 人在线 `42B3UD` 到结算 ✅；3 人局域网 `QUN8HK` 到结算 ✅；3 人在线 `U2U2Q4` 只验到同桌推进（未跑完，长跑测试已停）。
6. ✅ **[P1] 收尾** — 架构文档 §12 已补、README 阶段 6 已更新、构建产物清理 + `.gitignore`、`engine.wasm` 保留的结论已写进文档。
7. ✅ **[P0] 阶段 9（安全/性能/适配，按要求跳过 7、8）** — 见 §7-E 与 `docs/阶段9-安全性能与适配测试报告.md`：压测器 + 实测数据 + 8 项滥用探测 + 安全响应头（已上线）。
8. ⬜ **[P2] 阶段 7**（提示词 `07_房主迁移与断线恢复.md`）：断线重连恢复、房主离开时的迁移；之后阶段 8 多语言、阶段 10 部署强化。
9. ⬜ **[P2] 真两台设备验收**：手机 + 电脑各一个浏览器 profile，P2P 与局域网各打一局（同机多标签已通过，网络穿越仍需真机证据）。
10. ⬜ **[P2] TURN**：对称 NAT / 运营商级 NAT 下 P2P 必失败，需要 coturn + `webrtc.ts` 配 `turn:`。
11. ⬜ **[P2] 阶段 9 补测**：跑满一局的吞吐、真 1 核 1GB、断线恢复耗时自动化、手机真机横屏/触控。
12. ✅ **[P0] 2026-10-26 体验三项** — (1) 自动过牌：`game` 包 `respondActions` 只返回真实可选项（胡/碰/杠/吃），`Discard` 内 `autoPassLocked` 自动代过无选择座位并立即轮转，只有能响应的座位看到「过」；响应窗口超时缩短为 15s。(2) 续玩：结束后 `StatusFinished` 仍可加入/补机器人，房主离开自动转移给座位最小的真人，断线 60s 由 `reapDisconnected` 回收，`StartMatch` 重置 `actionSeq`；前端结算弹窗新增「再来一局 / 返回房间 / 离开房间」，牌桌头部常驻「离开」。(3) 移动端：`100dvh` 全屏布局 + 横屏全屏按钮（`requestFullscreen` + `orientation.lock` 兜底）、手牌改 `hand-scroll` 容器（`w-max min-w-full` 修掉居中溢出导致左端滑不到）、窄屏/矮屏断点与安全区适配。
13. ✅ **[P0] 2026-10-26 深夜 体验五项** — (1) P2P「房间不存在或已结束」：根因是 `sig.RemovePeer` 在房主信令 WS 一断（手机切后台/锁屏）就 `DeleteRoom`。改为**房间保留到 `CleanupRooms` 10 分钟**，新增 `Registry.Rehost` + `room.rehost`/`room.rehost.ok`（房主凭 `hostToken` 原地重建）、`room.ping`/`room.pong` 心跳（15s/45s 无回音主动重连）、`webrtc._scheduleSigReconnect()` 指数退避（房主 rehost、加入者重发 `room.join`）、`ensureSignaling()`（`visibilitychange`/`online` 补连）、房主 `WakeLock` 屏幕常亮；`resolve` 返回 `peers`，`peers===0` 时加入端直接提示「房主暂时不在线」。**(2)(3) 牌桌「离开」不再退房**：改为「回房间」(`onBackToWaiting`，保留座位/连接)，房间界面顶栏与底部出现「回到牌桌继续对局」(`onResumeMatch`)；真正退房走房间界面的「离开房间」(`onLeaveRoom`)。同时 `ws.opResync` 不在房间时回空 `RoomState` 而不再报 `not_in_room`「当前不在房间内，无法同步」，客户端 Hello 只有 `room` 存在才 `resync()`。**(4) 全屏真全屏**：`isFullscreen` 时隐藏牌桌顶栏（房间名/主题/动效/离开），`onBackToWaiting`/`onLeaveRoom` 都会 `exitFullscreenIfAny()`（含 `screen.orientation.unlock()`），不再出现「回到创建页仍是横屏全屏」。**(5) 横屏信息重排**：`TableCenter` 去掉四角方位字（与数字重叠根因），只留「剩余 N / 阶段 / 谁在行动」；对手名牌内常驻手牌数（矮屏不画牌背只留数字）；新增 `discardLog`（`game.DiscardEntry` + `SeatView.DiscardLog`）与「最近出牌」6 张横条，点开二级面板看四家完整牌河。另修 **2/3 人局座位映射**：原先 `(mySeat+rel)%总人数` 在 2 人局把「对家」算成自己、把「上家」算成对手（顶栏与左侧重复显示同一人），改为 `topSeat/rightSeat/leftSeat` 计算属性 + `v-if`，2 人局只有顶部对手、3 人局下家/上家分列左右。

14. ✅ **[P0] 2026-10-27 阶段 10 规则细则** — 「更多规则设置」二级面板（计时/优先级/和牌 三 tab）；`RuleSet` 新增 `RespondMeldSec`(默认 3) / `RespondWinSec`(默认 5) / `DiscardSec`(默认 0=不限) / `RobWin` / `RobMeld`；响应改为**按座位独立倒计时 + claim 仲裁**（优先级 `priWin=3 > priMeld=2 > priChow=1`，同优先级按离出牌者座次；`RobWin=false`/`RobMeld=false` 退化为先按者得），仲裁期显示「判定中」、结束显示「判定成功/失败」；无真实选择的座位由 `resetResponseLocked` 直接标记已答（不再要玩家点「过」）；`DiscardSec=0` 时下家可用 `ForceDraw`（「摸牌」按钮）收敛窗口；出牌超时由 `Tick` 自动打刚摸的牌/最右一张。`httpapi/rules_patch.go` 用全指针结构逐字段合并 `RuleSet`，避免旧客户端把新字段清零。详见 §7-F。

15. ✅ **[P0] 2026-10-27 阶段 11** — (1) **时序番型**：`RuleSet` 增 `RobKongWin` / `KongFlower` / `LastTile`；补杠时若有人听那张牌则开**抢杠窗口**（只有胡），杠后补牌自摸算**杠上开花**，牌墙摸空/出空的最后一张算**海底捞月 / 河底捞鱼**；三类互斥只计最高一个、各 +2 番；`ResultView.Flags` 在结算面板显示番种明细。(2) **副露多选**：`ActionControls.vue` 全屏 Teleport 面板（修掉 `overflow-hidden` 裁切导致的"点了没反应"）+ 提交锁 + 失效自动收起 + 多重集候选校验。(3) **DIY 主题**：服务端 `diy_tiles` / `diy_settings` 两表 + 6 个 REST 接口 + 密码门；前端首页齿轮 → 密码 → 以经典图案为模板逐张上传图片/选填充方式/预览/保存，牌面全员共享，设置页可改密码。详见 §7-G。
16. ✅ **[P0] 2026-10-27 阶段 7** — (1) **返回键不再丢局**：`pushState` 哨兵 + `popstate` 软守卫，牌桌 → 等待房间 → 首页（房间保留 + 首页「继续对局」卡片）；修掉 `watch(conn.room)` / `watch(match.view)` 把首页拽回房间/牌桌的两个根因。(2) **自动回房**：`sessionStorage` 记房间 + `room.state` 时补 `resync()`，刷新即回牌桌。(3) **房主任期与迁移**：`Room.hostEpoch` + `migrations` 日志（`host.leave/disconnect/reap/seat_bot`），`electHostLocked` 选举 + `promoteHostLocked` 只在真换人时 bump；断线 15s 交棒、掉线 60s 转托管、重连收回座位、旧房主不抢任期。(4) **离开者不占座**：对局中离开转托管（`Left` 标记 + `HasPlayer` 排除），一局结束由 `PurgeLeft` 让位，无真人则 teardown。(5) **P2P 明确终止**：对局中失去房主弹「对局已中断」，不做假恢复。(6) **音效**：`utils/sfx.ts` 纯 WebAudio 合成 9 种音 + 「音效: 开/关」。详见 §7-H。

提示词位置：`D:\PandV\D\文档\麻将提示词\`（01 架构、02 规则、03 前端、04 单机AI、05 在线、06 P2P/局域网、07 …）。

---

## 9. 踩过的坑（避免重复浪费）

**前端**
- Pinia：**非函数状态字段千万不要写在 `actions` 里**（会被包成函数，`!flag` 恒 false）。`_tmpHandlers` / `_sigResolved` / `_p2pHandlers` 都必须在 `state`。
- `webrtc.ts` 的 `ws.onmessage` 必须遍历 `_tmpHandlers`（复制数组再遍历），否则 `createP2PRoom` 的 Promise 永不 resolve。
- `http.ServeFile` 对 `/index.html` 会 301；SPA 回退请直接 `os.ReadFile` + `Write`。
- `identity` store 的字段是 `guestId` / `nickname`（没有 `current`），曾导致 `guest.current?.id` 恒 undefined、房主 guestId 恒 `'host'`。
- `PlayerView` 字段是 `nickname`，不是 `name`。
- 打牌 payload 契约：`{ tile: "wan2" }`（不是 `{kind}`）；手牌是**字符串数组**。
- 同一浏览器多标签**旧版**共享 `localStorage` 身份会互相覆盖；现已改 `sessionStorage`（每标签一份，旧身份首次加载迁移一次），双人/三人联机可直接同浏览器多标签测。

**构建/运行**
- vite dev 默认只监听 IPv6（`[::1]`）→ 必须 `--host 0.0.0.0`；改完端口确认浏览器连的是活着的那个（否则 WS 连死端口，误判"信令失败"）。
- **vite 在 Windows 上会因 `web/.package.json.*.tmpdir/` 被 watch 而 EBUSY 崩溃**（本次实测一次，进程直接退出）→ 已在 `web/vite.config.ts` 加 `server.watch.ignored`（`**/.package.json.*.tmpdir/**`、`**/*.tmpdir/**`、`dist`、`*.tsbuildinfo`）。若还遇到，先删目录再启动。
- `Start-Process` 传含空格路径（`...\DSH NEXT\...`）会把参数截断成 `D:\SOFT\appCoTools\DSHcn\DSH` → 启动 dev server 请用后台 job（本工具的 `run_in_background`）或直接 `& "$node\node.exe" $pnpm ...`，不要用 Start-Process。
- 后端 `MAJIANG_HTTP_LISTEN`（不是 `MAJIANG_LISTEN`）；本机 8080 被 Steam 占用 → 用 8790。
- Windows 下 pnpm/node 路径含空格，PowerShell 必须加引号；`go test` 的 exit 1 可能是 stderr 假象。
- 阿里云 Caddy **跑在 Docker 容器里**（`caddy:latest`，`network_mode: host`）：host 上没有 `/usr/bin/caddy`，配置 `/opt/caddy/Caddyfile` bind-mount 到容器 `/etc/caddy/Caddyfile`。改完必须 `docker exec caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile` 再 `docker exec caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile`（或 admin API `POST http://127.0.0.1:2019/load`），**不要** `docker restart`（会短暂断所有站点）。
- Caddy 里 `redir` 的 `{uri}` 在 `uri strip_prefix` **之前**求值（directive 顺序），所以「去前缀再跳转」必须用 `handle_path /mj/*`（它先剥前缀）而不是 `handle` + `uri strip_prefix`；否则会跳成 `https://新域名/mj/...`。
- `.gitignore` 已补 `*~`、`**/*.tmpdir/`、`*.tsbuildinfo`，并把已跟踪的 `web/tsconfig.node.tsbuildinfo` 从索引移除。
- `web/package.json` 保留脚本 `build:mj`（`vue-tsc -b && vite build --base=/mj/`），但**线上已改根路径部署**，常规发布用 `pnpm build`（base 默认 `/`）。

**浏览器自动化**
- `dsh_browser_click` 的文本匹配会先命中 `h1`（如"创建房间"标题），**优先用 css 选择器 + 精确文本 JS 点击**：
  `Array.from(document.querySelectorAll('button')).find(b=>b.textContent.trim()==='创建房间').click()`
- `dsh_browser_execute` 只接受单表达式（不能多行 `const`/`try`），用 IIFE `(() => { ... })()`。
- `dsh_browser_execute` 的 Promise **20s 超时**：长对局不要 `await` 到底，改为常驻 `setInterval` 自动打牌 + 定期短查询（`document.body.innerText` 里 `剩余牌墙 N` / `总番数` 是最可靠的进度与终局信号）。
- 手牌牌面是 `<button class="mahjong-tile">`（**无文本**，文本在子节点），摸切用 `document.querySelectorAll('button.mahjong-tile')` 最后一个。
- 后台（非激活）标签的 `setInterval` 会被 Chrome 限流到 ~1s 级，因此三标签自动对局一局约 20~25 分钟。

**编码/写文件（血泪）**
- **禁止用 PowerShell 管道/`Set-Content`/here-string 写含中文的文件**：控制台码页会把中文变成 `鎴?` 一类乱码（本次把 `localGame.test.ts` 写坏过一次，只能 `git checkout --` 回滚）。中文内容一律用 `write`/`edit` 工具；`git commit` 的中文说明先写到临时文件再 `git commit -F <file>`。
- `.gitignore` 原本是 UTF-8/GBK **混合编码**的坏文件（`read` 报 invalid UTF-8），已重写为纯 ASCII。

**网络/部署**
- 验证 WebSocket 健康**必须带浏览器真实 `Origin`**：不带 Origin 的 curl 拿到 101 会让人误判"信令正常"，而浏览器带真实 Origin 实际被 403（本次 §7-A 就是这么被藏住的）。
- 反向代理后 `Origin` 是公网域名，`r.Host` 也是公网域名 ⇒ 同源判定即可放行；不要只依赖手写白名单，也不要硬编码局域网前缀当成唯一规则。
- **ssh 远端命令一律用单引号包整串**：`$(date …)`、`$(...)`、`{}`、`()`、`|` 在 pwsh 双引号里会被本地先解析（曾把 `$(date +%Y%m%d)` 当 `Get-Date` 求值，报"无法绑定参数 Date"）；复杂逻辑写成 `.sh` 脚本 `scp` 上去 `sh` 执行最稳。
- `scp` 多个源文件到同一个远端路径会把该路径当**目录**创建（`scp a b host:/x` ⇒ `/x/a`、`/x/b`）；一次只传一个文件，或先确认目标不存在。

---

## 10. 参考数据（容量）

| 场景 | RSS | CPU |
|---|---|---|
| 空载 | 12.4 MB | ≈0% |
| 30 房最坏（每房 1 人 + 3 机器人不停出牌） | 22.2 MB（每活跃房≈0.3MB） | 4.6% 单核（每房≈0.15%） |

带宽是瓶颈：每房≈30–40 kbps ⇒ 100 房≈3–4 Mbps，500 房≈16–20 Mbps。1核2G / 2核2G ECS 可跑 100–300 房。
前端 dist 若不删 wasm ≈ 5.8MB（含 4.9MB 无用 `engine.wasm`），删后 ≈ 0.9MB。

---

## 11. 新对话开场建议（复制粘贴用）

> 项目在 `D:\PandV\D\CONprogram\woleMajiang`，请先读 `docs/HANDOFF.md`（接续报告）再动手。
> 线上地址 `https://mj.wole110.xyz/`（根路径部署；旧 `https://tsapi.wole110.xyz/mj/` 301 过来）。
> 先做三件事：①三绿（`go build/vet/test ./...` + `vue-tsc -b` + `vitest`）；②按 §7-A 用浏览器带真实 `Origin` 复测信令；③按 §8 清单挑一项推进（当前最值得做：阶段 7 房主迁移、真双设备 P2P、TURN）。
> 目标延续：(1) 2/3/4 人可开局打完；(2) P2P 与局域网真实验证；(3) 阿里云线上可访问（已完成，地址 https://mj.wole110.xyz/）。
