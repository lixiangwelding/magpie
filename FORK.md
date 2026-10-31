# This fork

A fork of [yetone/magpie](https://github.com/yetone/magpie), with two local
additions on top of upstream.

## What is added here

### `leadsub` — an agent's lead and its subagents on two models

A lead (the main thread, which plans, reviews and dispatches) and its
subagents (the work) are two different jobs, and often two different
models: a strong one for the plan and the review, a cheap one for the
work.

Magpie already has every piece of it — a routing group per tier (the
Routing page), Claude Desktop's subagent model and subagent effort (the
Agents page), and the gateway's rewrite of every subagent turn onto the
subagent model (`internal/gateway/desktop_subagent.go`). Setting it takes
three places, and the two sides of the split are set in different words.

`leadsub` adds a **Lead and subagents** page beside Routing and Gateway.
Pick an existing routing group (or model) for each side and its reasoning
effort from Magpie's menus. The page reads the current agent settings and
group members, writes selections immediately, and reads them back. A failed
write stays an error and never saves the failed selection as plugin config.
It also has advanced settings in **Plugins › Built-in**:

| Setting | What it does |
| --- | --- |
| Agent | `claude-desktop` (its Code tab and Cowork) or `claude` (Claude Code) |
| Lead model | what the main thread runs on: a model (`provider/model`) or a routing group (`group/<id>`) |
| Lead effort | the reasoning effort the lead's turns are given |
| Subagent model | what every subagent turn is put on, whatever the lead asked for |
| Subagent effort | the reasoning effort the subagent turns are given |
| Lead group / members | the routing group the lead's model is served by, and its members |
| Subagent group / members | the routing group the subagents' model is served by, and its members |
| Manage the two groups | make the two groups if they aren't there, and keep their members in step |

Two things about how it lands are worth knowing.

**The subagent model is magpie's own setting for Claude Desktop.** It is
read on each request, so a change needs no restart of Desktop. Claude
Code's own `CLAUDE_CODE_SUBAGENT_MODEL` is not used: Claude Desktop's Code
tab runs its Claude Code with `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1`,
under which Claude Code ignores that variable and the tier variables in its
settings files' `env`.

**The lead's effort lands differently on each agent.** Claude Code has an
effort field of its own. Claude Desktop has one only for its subagents, so
the lead's effort on Desktop is written on the lead group's members (the
`:high` after a member) — where magpie keeps a member's own reasoning
level (`internal/provider/groupeffort.go`): a request routed to such a
member is asked for that much reasoning, whatever the agent asked for.

A plugin that is already on is not applied again when its settings change
(`internal/pluginrt/pluginrt.go`, `Reconcile`): the window's form would
then change nothing until magpie restarts. So `leadsub` watches its own
settings and puts a change in place as it is made.

### The Windows service-proxy patch

`internal/gui/pluginpage.go`: a community plugin's page reaches its own
local service through magpie in `magpie web` (`/api/plugin-pages`' `service`,
the bridge's `magpie.service`), so the page never navigates its sandboxed
frame to `127.0.0.1`. The desktop window was not given that: a Windows
WebView2 frame went straight to `127.0.0.1:<port>`, which is the frame's own
machine but not the asset origin it was served from, so the page's own
requests (the credits overview's `api/summary`) never reached the service
and the page showed nothing.

Windows' desktop window now gets the same proxy (`pageServiceProxy`). The
guards are unchanged: the service stays on loopback, the token is still the
way in, Origin is still only absent or `null`, and the gateway's port is
still never served. Other desktop engines keep their existing direct
loopback navigation.

### The plugin-lock patch

`internal/provider/plugin_accounts.go`: a plugin's accounts are looked up
without the `logins.json` lock, which is taken only for a change to
write. Every model look-up asks a plugin for its accounts, so with a
provider of many accounts (a plugin of 44, one per sign-in) each request
took that lock dozens of times, and concurrent requests queued behind each
other until the gateway gave up on them unrouted (the 30s "held this
request" cut).

## Building

```
go build -tags production,comlauncher .          # the app
go build -tags nogui .                           # the CLI
```

Cross-compiling for Windows needs the launcher built first:

```
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o internal/update/launcher-amd64.com ./internal/update/launcher
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags production,comlauncher .
```

## 测试方案

A 档（本次已跑）：`node --test internal/gui/tests/leadsub.test.cjs`（Playwright
在 `NODE_PATH` 中），覆盖五种语言、桌面与窄窗口、模型与路由组选择、思考强度、
保存回读、刷新恢复及失败不误报成功。Chromium 一条腿通过（Windows 桌面窗口用的
正是 Chromium 内核的 WebView2）；WebKit 一条腿因本机缺宿主库（libgtk-4、
libgstreamer 等）且无免密 sudo，未跑。

`go test -tags nogui ./...` 全仓：`internal/gui` 余 `TestLaneLimitRoutes`
（车道计数）与 `TestSettingsSaveKeepsWhatItDoesNotSend`（Settings 页的 SearchAll
字段）两条失败，两条在引入本 fork 的 `leadsub` 之前就已存在（已用
`git checkout 66143cb` 复核），与本次改动无关。
`TestBuiltinsTranslated`、`TestBuiltinClientsLoaded`、`TestBootSaysBuiltinsOff`、
`TestBuiltinSwitch`、`TestBuiltinConfig` 通过。
`node --test internal/gui/tests/i18n-keys.test.cjs` 只剩上游既有的
`plugins/routing/i18n.js "Unmarked"` 一条失败（`git stash` 验证与本次无关）。
交叉构建 Windows 产物已通过（`CGO_ENABLED=0 GOOS=windows GOARCH=amd64
go build -tags production,comlauncher`）。

B 档（待办，本轮未做）：在 Windows 原生窗口打开"主线程与子代理"，确认主线程与
子代理分别显示当前组，点击各自菜单能选择现有组，刷新后保持；仅切换本次测试
专用配置。"积分总览"须显示服务返回的积分数据，JS/CSS 与 summary 请求成功，
且不放宽插件 sandbox。浏览器模式单独回归，不能替代原生窗口验收。

## Keeping up with upstream

```
git remote add upstream https://github.com/yetone/magpie.git
git fetch upstream
git rebase upstream/main
git push fork main
```

The two commits here are kept separate and small on purpose, so that a
rebase onto upstream is usually a clean replay. The plugin-lock patch
touches one file and may need a conflict resolved when upstream changes
the same function.
