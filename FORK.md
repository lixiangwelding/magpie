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

`leadsub` sets all of it from one form, in the window's **Plugins ›
Built-in** page:

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
