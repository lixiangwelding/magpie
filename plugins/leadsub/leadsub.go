// Package leadsub splits an agent's models in two: the lead (the main
// thread, which plans, reviews and dispatches) runs on one model, and its
// subagents (the work) run on another.
//
// It is a thin front for what magpie already does: a routing group per tier
// (the Routing page), Claude Desktop's subagent model and subagent effort
// (the Agents page), and the gateway's rewrite of every subagent turn onto
// the subagent model (internal/gateway/desktop_subagent.go). What this
// plugin adds is one place that sets all of it at once, from one form, and
// keeps the two groups in step with the members you name.
//
// The subagent model is magpie's own setting for Claude Desktop (the
// gateway reads it on each request, so a change needs no restart of
// Desktop). Claude Code's own CLAUDE_CODE_SUBAGENT_MODEL is not used:
// Claude Desktop's Code tab runs its Claude Code with
// CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1, under which Claude Code ignores
// that variable and the tier variables in its settings files' env.
//
// The lead's model is a tier for Claude Desktop: its Code tab asks for
// Claude's own ids (claude-opus-…), and the gateway answers the opus tier
// with the model the tier is picked to run (desktopMove, movedTier). For
// Claude Code, which asks for the model by name, the lead's model is the
// agent's model field.
//
// The effort of the lead and of the subagents lands differently on each:
// Claude Code has an effort field of its own for both, while Claude Desktop
// has one only for its subagents (subagent_effort). The lead's effort on
// Desktop is therefore written on the lead group's members (the ":high"
// after a member), which is where magpie keeps a member's own reasoning
// level (internal/provider/groupeffort.go): a request routed to such a
// member is asked for that much reasoning, whatever the agent asked for.
package leadsub

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/pluginrt"
	"github.com/yetone/magpie/internal/provider"
)

// Settings persist the split. The dedicated page writes the agent fields
// immediately and reads them back; Plugins › Built-in also exposes this
// schema for users who configure the split without opening its page.
type Settings struct {
	// Agent is the agent whose two models are set: claude-desktop (its
	// Code tab and Cowork) or claude (Claude Code).
	Agent string `json:"agent" title:"Agent" description:"The agent to split: claude-desktop, claude, or claude@wsl:Ubuntu-24.04" enum:"claude-desktop,claude"`
	// LeadModel is what the lead runs on: a model of magpie's
	// (provider/model) or a routing group (group/<id>). For Claude
	// Desktop this is the opus tier, for Claude Code its model.
	LeadModel string `json:"leadModel" title:"Lead model" description:"What the main thread runs on: a model (provider/model) or a routing group (group/<id>)"`
	// LeadEffort is the reasoning effort the lead's turns are given. On
	// Claude Desktop it lands on the lead group's members, which is the
	// only place a tier's effort can be fixed.
	LeadEffort string `json:"leadEffort" title:"Lead effort" description:"The reasoning effort the main thread's turns are given" enum:"none,minimal,low,medium,high,xhigh,max"`
	// SubagentModel is what every subagent turn is put on, whatever the
	// lead or its tier asked for. Empty leaves the subagents where they
	// were.
	SubagentModel string `json:"subagentModel" title:"Subagent model" description:"What every subagent turn is put on (a model or a group). Empty leaves them where they were"`
	// SubagentEffort is the reasoning effort the subagent turns are
	// given, independent of their model.
	SubagentEffort string `json:"subagentEffort" title:"Subagent effort" description:"The reasoning effort the subagent turns are given" enum:"none,minimal,low,medium,high,xhigh,max"`
	// LeadGroup is the routing group the lead's model is served by, and
	// the one whose members are set below. Empty leaves the lead's model
	// as it is.
	LeadGroup string `json:"leadGroup" title:"Lead group" description:"The routing group the lead's model is served by, and the one whose members are set below. Empty leaves the lead's model as it is"`
	// LeadGroupModels are the lead group's members, comma separated, each
	// provider/model. The lead's effort is fixed on each of them. Empty
	// leaves the group's members as they are.
	LeadGroupModels string `json:"leadGroupModels" title:"Lead group members" description:"The lead group's members, comma separated (provider/model). The lead's effort is fixed on each of them"`
	// SubagentGroup is the routing group the subagents' model is served
	// by, and the one whose members are set below.
	SubagentGroup string `json:"subagentGroup" title:"Subagent group" description:"The routing group the subagents' model is served by, and the one whose members are set below. Empty leaves the subagents' model as it is"`
	// SubagentGroupModels are the subagent group's members, comma
	// separated, each provider/model. The subagents' effort is fixed on
	// each of them. Empty leaves the group's members as they are.
	SubagentGroupModels string `json:"subagentGroupModels" title:"Subagent group members" description:"The subagent group's members, comma separated (provider/model). The subagents' effort is fixed on each of them"`
	// ManageGroups says the two groups are ours to make and keep: a group
	// named here that isn't there yet is made, and its members are set to
	// what is named. Off, the groups are left alone.
	ManageGroups bool `json:"manageGroups" title:"Manage the two groups" description:"Make the two groups if they aren't there, and set their members to what is named. Off, the groups are left alone"`
}

// manifest is this plugin's own, kept so that the watcher can read the
// defaults and the schema of the settings it watches.
var manifest pluginrt.Manifest

func init() {
	manifest = pluginrt.Manifest{
		ID: "leadsub", API: "^1", Requires: []string{"window", "providers"},
		Name: "Lead and subagents", Sub: "Run the main thread on one model and its subagents on another",
		One: "1 agent split in two", Many: "{n} agents split in two",
		// The page of its own, in the sidebar under Work: the split as it
		// is now, and the two routing groups picked and made here.
		Parts:  []pluginrt.Part{{Slot: "nav.page", Key: "leadsub"}},
		Client: []string{"i18n.js", "client.js", "leadsub.css"},
		InUse: func() (int, error) {
			// on while the agent it splits has a subagent model: off, the
			// split would go on working with nowhere to see it
			if provider.DesktopSubagentModel() != "" {
				return 1, nil
			}
			return 0, nil
		},
		Settings: Settings{
			Agent:          "claude-desktop",
			LeadEffort:     "high",
			SubagentEffort: "high",
		},
		Apply: apply,
	}
	pluginrt.Register(manifest)
}

// apply sets what the settings say: the two groups first, so a model that
// is served by a group is served by the time the agent is put on it.
func apply(c *pluginrt.Ctx) error {
	s := settingsOf(c.Config)
	if err := applySettings(s); err != nil {
		return err
	}
	// A plugin that is already on is not applied again when its settings
	// change (internal/pluginrt/pluginrt.go, Reconcile): the window's form
	// would then change nothing until magpie restarts. So the settings are
	// watched here, and a change made in the window is put in place as it
	// is made.
	stop := make(chan struct{})
	var once sync.Once
	c.OnDispose(func() { once.Do(func() { close(stop) }) })
	go watch(stop)
	return nil
}

// applySettings sets the two models and the two efforts, and the two groups
// when they are ours to make. A group that can't be written stops the
// plugin: it is the setting it exists for. A field the agent doesn't have,
// or a model magpie doesn't serve, is logged and left: the rest of the
// split still stands.
func applySettings(s Settings) error {
	if s.ManageGroups {
		if err := setGroup(s.LeadGroup, s.LeadGroupModels, s.LeadEffort); err != nil {
			return err
		}
		if err := setGroup(s.SubagentGroup, s.SubagentGroupModels, s.SubagentEffort); err != nil {
			return err
		}
	}
	a, err := agent.Find(s.Agent)
	if err != nil {
		return err
	}
	// Claude Desktop has no model field: its Code tab asks for Claude's
	// own ids and the gateway answers the opus tier with the model that
	// tier is picked to run. Claude Code asks for the model by name, and
	// its model field is it.
	leadKey := "model"
	if s.Agent == "claude-desktop" {
		leadKey = "opus"
	}
	if s.LeadModel != "" {
		soft(a, leadKey, s.LeadModel)
	}
	if s.LeadEffort != "" && s.Agent != "claude-desktop" {
		// Claude Code has an effort of its own for the lead. On Claude
		// Desktop there is none: the lead's effort is on the group's
		// members, set above.
		soft(a, "effort", s.LeadEffort)
	}
	// The subagents' model is a field of its own on both: Desktop's is
	// "subagent_model" (a live gateway setting), Claude Code's is
	// "subagent" (its CLAUDE_CODE_SUBAGENT_MODEL).
	subKey := "subagent"
	if s.Agent == "claude-desktop" {
		subKey = "subagent_model"
	}
	if s.SubagentModel != "" {
		soft(a, subKey, s.SubagentModel)
	}
	if s.SubagentEffort != "" {
		soft(a, "subagent_effort", s.SubagentEffort)
	}
	return nil
}

// soft sets a field of an agent, logging what it couldn't do: a field the
// agent hasn't (Desktop has no effort of the lead's), or a model magpie
// doesn't serve, is not what the plugin is for.
func soft(a *agent.Agent, key, v string) {
	f := a.Field(key)
	if f == nil {
		log.Printf("leadsub: %s has no field %q", a.Name, key)
		return
	}
	if err := a.Pick(f.Key, v); err != nil {
		log.Printf("leadsub: %s's %s: %v", a.Name, key, err)
		return
	}
	log.Printf("leadsub: %s's %s is %s", a.Name, key, v)
}

// setGroup makes the group id if it isn't there, and gives it the members
// named, each fixed at the effort. An empty id or an empty list of members
// leaves the group alone: a group with no model in it is refused (a group
// needs a model in it, or a pattern its models match).
func setGroup(id, members, effort string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	list := splitMembers(members)
	if len(list) == 0 {
		return nil
	}
	g, _, found := provider.FindGroup(id)
	if !found {
		g = provider.Group{ID: id, Name: id}
	}
	// the group's own settings are kept: only its members are ours
	g.Members = fixEffort(list, effort)
	if g.Routing == "" {
		g.Routing = "order"
	}
	if err := provider.SaveGroup(g); err != nil {
		return fmt.Errorf("the group %q: %w", id, err)
	}
	log.Printf("leadsub: the group %q runs on %s", id, strings.Join(g.Members, ", "))
	return nil
}

// fixEffort fixes each member at the effort, unless it names one of its
// own ("provider/model:low"): a member's own effort is the user's, and is
// kept.
func fixEffort(list []string, effort string) []string {
	out := make([]string, 0, len(list))
	for _, m := range list {
		if _, e := provider.MemberEffort(m); e != "" {
			out = append(out, m)
			continue
		}
		out = append(out, provider.WithMemberEffort(m, effort))
	}
	return out
}

// splitMembers reads a comma separated list of members, each trimmed.
func splitMembers(s string) []string {
	var out []string
	for _, m := range strings.Split(s, ",") {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}

// watch puts the settings in place again when they change in the window.
// It reads plugins.yml the way the runtime does, and compares what the
// plugin's own row says with what it last applied.
func watch(stop <-chan struct{}) {
	last := fingerprint(currentConfig())
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			cfg := currentConfig()
			fp := fingerprint(cfg)
			if fp == last {
				continue
			}
			last = fp
			s := settingsOf(cfg)
			if err := applySettings(s); err != nil {
				log.Printf("leadsub: %v", err)
				continue
			}
			log.Printf("leadsub: the settings changed; the split is set again")
		}
	}
}

// currentConfig is the plugin's own config as plugins.yml has it, over the
// built-in defaults: what the runtime composes for it.
func currentConfig() map[string]any {
	out := map[string]any{}
	for k, v := range manifest.Defaults {
		out[k] = v
	}
	f, _, err := pluginrt.Load()
	if err != nil {
		return out
	}
	for _, r := range f.Plugins {
		if r.ID != manifest.ID {
			continue
		}
		for k, v := range r.Config {
			out[k] = v
		}
	}
	return out
}

// fingerprint is a config as a string to compare two of them by.
func fingerprint(cfg map[string]any) string {
	b, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(b)
}

// settingsOf reads the plugin's config as its settings. The schema has
// already checked the kinds, so a value of the wrong kind is not expected;
// one that is wrong is read as empty rather than failing the plugin.
func settingsOf(config map[string]any) Settings {
	var s Settings
	for k, v := range config {
		switch k {
		case "agent":
			s.Agent, _ = v.(string)
		case "leadModel":
			s.LeadModel, _ = v.(string)
		case "leadEffort":
			s.LeadEffort, _ = v.(string)
		case "subagentModel":
			s.SubagentModel, _ = v.(string)
		case "subagentEffort":
			s.SubagentEffort, _ = v.(string)
		case "leadGroup":
			s.LeadGroup, _ = v.(string)
		case "leadGroupModels":
			s.LeadGroupModels, _ = v.(string)
		case "subagentGroup":
			s.SubagentGroup, _ = v.(string)
		case "subagentGroupModels":
			s.SubagentGroupModels, _ = v.(string)
		case "manageGroups":
			s.ManageGroups, _ = v.(bool)
		}
	}
	if s.Agent == "" {
		s.Agent = "claude-desktop"
	}
	return s
}
