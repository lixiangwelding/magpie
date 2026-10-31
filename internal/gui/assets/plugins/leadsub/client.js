// The split has a page of its own; the agent and routing groups remain
// the source of truth. Only a successful write is kept in plugins.yml.
(() => {
  let ctx, box;
  let cfg = {}, agents = [], groups = [];
  let busy = false;
  const leadField = (id) => id === "claude-desktop" ? "opus" : "model";
  const subField = (id) => id === "claude-desktop" ? "subagent_model" : "subagent";
  const agentID = () => cfg.agent || "claude-desktop";
  const field = (key) => agents.find((a) => a.id === agentID())?.fields?.find((f) => f.key === key);
  const model = (side) => field(side === "lead" ? leadField(agentID()) : subField(agentID()))?.value || "";
  const group = (side) => {
    const ref = model(side).replace(/\[[^\]]*\]$/, "");
    return groups.find((g) => (g.ref || "group/" + g.id) === ref);
  };
  const effort = (side) => {
    const own = field(side === "lead" ? "effort" : "subagent_effort")?.value;
    const members = group(side)?.members || [];
    const levels = members.map((m) => m.match(/:(none|minimal|low|medium|high|xhigh|max)$/)?.[1]);
    return own || (levels.length && levels.every((v) => v && v === levels[0]) ? levels[0] : "") || cfg[side + "Effort"] || "high";
  };

  async function load() {
    const [builtins, state, gs] = await Promise.all([api("plugins/builtin"), api("state"), api("groups")]);
    cfg = { ...(builtins.find((b) => b.id === "leadsub")?.config || {}) };
    agents = state.agents || [];
    groups = gs.groups || [];
  }

  async function change(work) {
    if (busy) return;
    busy = true;
    try {
      await work();
      status(t("Saved"), "ok", 1500);
    } catch (err) {
      status(t("Not saved: {why}", { why: err.message }), "err");
    } finally {
      try { await load(); } catch (err) { status(err.message, "err"); }
      busy = false;
      draw();
    }
  }

  async function keep(next) {
    // This page edits existing groups through their API. Never replay an
    // old member list from the generic settings form over those groups.
    await api("plugins/builtin", { id: "leadsub", config: { ...cfg, ...next, manageGroups: false } });
  }

  async function pickModel(side, value) {
    const key = side === "lead" ? leadField(agentID()) : subField(agentID());
    await api("set", { Agent: agentID(), Field: key, Value: value });
    await keep({ [side + "Model"]: value, [side + "Group"]: value.startsWith("group/") ? value.slice(6).replace(/\[[^\]]*\]$/, "") : "" });
  }

  async function pickEffort(side, value) {
    const g = group(side);
    if (side === "lead" && agentID() === "claude-desktop" && (!g || !g.members?.length)) throw new Error(t("Pick a routing group"));
    if (g?.members?.length) {
      // A member's fixed effort overrides the request, so both sides set
      // it on their chosen group. Preserve every other group setting.
      const members = g.members.map((m) => m.replace(/:(none|minimal|low|medium|high|xhigh|max)$/, "") + ":" + value);
      await api("groups/save", { ...g, members });
    }
    if (side !== "lead" || agentID() !== "claude-desktop") {
      await api("set", { Agent: agentID(), Field: side === "lead" ? "effort" : "subagent_effort", Value: value });
    }
    await keep({ [side + "Effort"]: value });
  }

  function options(f) {
    const out = groups.filter((g) => g.ready).map((g) => ({ v: g.ref || "group/" + g.id, name: g.name || g.id }));
    const seen = new Set(out.map((o) => o.v));
    for (const o of f?.options || []) {
      if (seen.has(o.value)) continue;
      seen.add(o.value);
      out.push({ v: o.value, name: o.label || o.value });
    }
    return out;
  }

  function card(side, title, modelTitle, effortTitle) {
    const c = el("section", "ls-card");
    c.dataset.side = side;
    c.append(el("h2", "", title));
    const key = side === "lead" ? leadField(agentID()) : subField(agentID());
    const pick = ctx.ui.menu(options(field(key)), model(side), modelTitle, (v) => change(() => pickModel(side, v)));
    pick.disabled = !field(key) || busy;
    c.append(ctx.ui.row(modelTitle, "", pick));
    const levels = side === "lead" && agentID() === "claude-desktop"
      ? ["low", "medium", "high", "xhigh", "max"]
      : (field(side === "lead" ? "effort" : "subagent_effort")?.options || []).map((o) => o.value).filter(Boolean);
    const ep = ctx.ui.menu(levels.map((v) => ({ v, name: v })), effort(side), effortTitle, (v) => change(() => pickEffort(side, v)));
    ep.disabled = busy || (side === "lead" && agentID() === "claude-desktop" && !group(side)) || !levels.length;
    c.append(ctx.ui.row(effortTitle, "", ep));
    const members = el("div", "ls-members");
    for (const member of group(side)?.members || []) members.append(el("span", "ls-chip", member));
    c.append(members);
    return c;
  }

  function draw() {
    if (!box) return;
    box.replaceChildren();
    const head = el("div", "ls-head");
    head.append(el("p", "ls-say", t("The main thread and its subagents on two routing groups of your own")));
    const ids = ["claude-desktop", "claude"].filter((id) => agents.some((a) => a.id === id));
    const pick = ctx.ui.menu(ids.map((id) => ({ v: id, name: agents.find((a) => a.id === id).name })), agentID(), t("Agent"), (id) => change(async () => {
      // Changing the target must not copy Desktop's values into Code.
      const a = agents.find((a) => a.id === id);
      const val = (key) => a.fields.find((f) => f.key === key)?.value || "";
      await keep({ agent: id, leadModel: val(leadField(id)), subagentModel: val(subField(id)), leadGroup: "", subagentGroup: "" });
    }));
    pick.disabled = busy || !ids.length;
    head.append(pick);
    const grid = el("div", "ls-grid");
    grid.append(card("lead", t("The lead runs on"), t("Lead model"), t("Lead effort")), card("subagent", t("The subagents run on"), t("Subagent model"), t("Subagent effort")));
    box.append(head, grid);
  }

  async function refresh() { await load(); draw(); }
  magpie.plugin("leadsub", (c) => {
    ctx = c;
    cfg = { ...c.config };
    c.onConfig((config) => { cfg = { ...config }; draw(); });
    c.slots.add("page", {
      key: "leadsub", title: "Lead and subagents", html: '<div class="ls-wrap"></div>',
      mount(main) { box = main.querySelector(".ls-wrap"); },
      open: refresh, refresh, relang: draw,
    });
  });
})();
