package alerts

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/henrygd/beszel/internal/entities/system"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// App alerts fire when one app's CPU or memory stays over a limit.
//
// An app is a container name up to its first "/". Containers that the Coolify
// docker proxy names "resource/service" are summed into their resource, so a
// multi-container app is judged as a whole.
//
// Rules live in the app_alerts collection, one row per (user, system, app).
// The row for app "*" is the default for every app on the system. A row for a
// specific app overrides it field by field: a zero cpu, memory or min falls
// back to the default. A disabled row mutes that app.
//
// While an app is over a limit, the rule row that governs it records since
// when in its state field, so a hub restart neither resets the clock nor
// sends the alert twice.

const (
	appAlertsCollection  = "app_alerts"
	appAlertDefaultApp   = "*"
	appAlertDefaultMin   = 10
	appAlertSourceConfig = "config"
)

// appAlertState is keyed by "<app>|<metric>".
type appAlertState map[string]*appAlertStateEntry

type appAlertStateEntry struct {
	// Since is when the app was first seen over the limit, in unix milliseconds.
	Since     int64 `json:"since"`
	Triggered bool  `json:"triggered"`
}

type appAlertRule struct {
	cpu      float64 // percent of the host
	memoryGB float64
	min      int
	disabled bool
}

type appUsage struct {
	cpu   float64 // percent of the host
	memMB float64
}

type appMetric struct {
	name  string
	value float64
	limit float64
}

// appName returns the app a container belongs to.
func appName(containerName string) string {
	if i := strings.IndexByte(containerName, '/'); i > 0 {
		return containerName[:i]
	}
	return containerName
}

// effectiveAppRule merges an app's override with the default rule. It returns
// the record whose state tracks the app, or nil when no rule applies.
func effectiveAppRule(def, override *core.Record) (appAlertRule, *core.Record) {
	var rule appAlertRule
	if def != nil {
		rule = appAlertRule{
			cpu:      def.GetFloat("cpu"),
			memoryGB: def.GetFloat("memory"),
			min:      def.GetInt("min"),
			disabled: def.GetBool("disabled"),
		}
	}
	owner := def
	if override != nil {
		owner = override
		rule.disabled = override.GetBool("disabled")
		if v := override.GetFloat("cpu"); v > 0 {
			rule.cpu = v
		}
		if v := override.GetFloat("memory"); v > 0 {
			rule.memoryGB = v
		}
		if v := override.GetInt("min"); v > 0 {
			rule.min = v
		}
	}
	if rule.min <= 0 {
		rule.min = appAlertDefaultMin
	}
	return rule, owner
}

// HandleAppAlerts checks per-app CPU and memory rules for a system against the
// container stats in the latest agent update.
func (am *AlertManager) HandleAppAlerts(systemRecord *core.Record, data *system.CombinedData) error {
	return am.handleAppAlerts(systemRecord, data, time.Now().UTC())
}

func (am *AlertManager) handleAppAlerts(systemRecord *core.Record, data *system.CombinedData, now time.Time) error {
	// an unknown Docker state must neither start nor resolve anything
	if data == nil || data.Containers == nil {
		return nil
	}
	records, err := am.hub.FindAllRecords(appAlertsCollection, dbx.HashExp{"system": systemRecord.Id})
	if err != nil || len(records) == 0 {
		return err
	}

	usage := make(map[string]*appUsage)
	for _, c := range data.Containers {
		app := appName(c.Name)
		u := usage[app]
		if u == nil {
			u = &appUsage{}
			usage[app] = u
		}
		u.cpu += c.Cpu
		u.memMB += c.Mem
	}

	rulesByUser := make(map[string]map[string]*core.Record)
	for _, r := range records {
		user := r.GetString("user")
		if rulesByUser[user] == nil {
			rulesByUser[user] = make(map[string]*core.Record)
		}
		rulesByUser[user][r.GetString("app")] = r
	}

	var result error
	for userID, rules := range rulesByUser {
		if err := am.evaluateAppAlerts(systemRecord, userID, rules, usage, now); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (am *AlertManager) evaluateAppAlerts(systemRecord *core.Record, userID string, rules map[string]*core.Record, usage map[string]*appUsage, now time.Time) error {
	states := make(map[string]appAlertState, len(rules))
	for _, r := range rules {
		states[r.Id] = readAppAlertState(r)
	}
	dirty := make(map[string]bool)
	touched := make(map[string]map[string]bool) // record id -> state keys still in play
	touch := func(recordID, key string) {
		if touched[recordID] == nil {
			touched[recordID] = make(map[string]bool)
		}
		touched[recordID][key] = true
	}

	systemName := systemRecord.GetString("name")
	var result error
	send := func(title, message string) {
		err := am.SendAlert(AlertMessageData{
			UserID:   userID,
			SystemID: systemRecord.Id,
			Title:    title,
			Message:  message,
			Link:     am.hub.MakeLink("system", systemRecord.Id),
			LinkText: "View " + systemName,
		})
		if err != nil {
			result = errors.Join(result, err)
		}
	}

	def := rules[appAlertDefaultApp]
	for app, u := range usage {
		override := rules[app]
		if override == nil && def == nil {
			continue
		}
		rule, owner := effectiveAppRule(def, override)
		state := states[owner.Id]
		metrics := []appMetric{
			{name: "cpu", value: u.cpu, limit: rule.cpu},
			{name: "memory", value: u.memMB / 1024, limit: rule.memoryGB},
		}
		for _, m := range metrics {
			key := app + "|" + m.name
			if rule.disabled || m.limit <= 0 {
				continue // left untouched, so a pending or firing entry is cleared below
			}
			touch(owner.Id, key)
			entry := state[key]
			if m.value <= m.limit {
				if entry != nil {
					if entry.Triggered {
						send(
							fmt.Sprintf("%s %s back below %s on %s ✅", app, appMetricLabel(m.name), formatAppLimit(m.name, m.limit), systemName),
							fmt.Sprintf("%s is using %s.", app, formatAppUsage(m.name, m.value)),
						)
					}
					delete(state, key)
					dirty[owner.Id] = true
				}
				continue
			}
			if entry == nil {
				entry = &appAlertStateEntry{Since: now.UnixMilli()}
				state[key] = entry
				dirty[owner.Id] = true
			}
			if entry.Triggered {
				continue
			}
			since := time.UnixMilli(entry.Since)
			if rule.min > 1 && now.Sub(since) < time.Duration(rule.min)*time.Minute {
				continue
			}
			entry.Triggered = true
			dirty[owner.Id] = true
			minutes := max(1, int(now.Sub(since).Round(time.Minute)/time.Minute))
			send(
				fmt.Sprintf("%s %s above %s on %s \U0001F534", app, appMetricLabel(m.name), formatAppLimit(m.name, m.limit), systemName),
				fmt.Sprintf("%s has used %s for %d %s (limit %s).", app, formatAppUsage(m.name, m.value), minutes, plural(minutes, "minute", "minutes"), formatAppLimit(m.name, m.limit)),
			)
		}
	}

	// Anything not evaluated this round has stopped, been muted, lost its limit,
	// or moved to another rule. Clear it, and say so if it had fired for an app
	// that is no longer running.
	for recordID, state := range states {
		for key, entry := range state {
			if touched[recordID][key] {
				continue
			}
			app, metric, _ := strings.Cut(key, "|")
			if entry.Triggered && usage[app] == nil {
				send(
					fmt.Sprintf("%s is no longer running on %s ✅", app, systemName),
					fmt.Sprintf("Its %s alert has been cleared.", appMetricLabel(metric)),
				)
			}
			delete(state, key)
			dirty[recordID] = true
		}
	}

	for recordID := range dirty {
		if err := am.saveAppAlertState(recordID, states[recordID]); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func readAppAlertState(r *core.Record) appAlertState {
	state := make(appAlertState)
	if err := r.UnmarshalJSONField("state", &state); err != nil || state == nil {
		return make(appAlertState)
	}
	for k, v := range state {
		if v == nil {
			delete(state, k)
		}
	}
	return state
}

// saveAppAlertState writes only the state field onto a fresh copy of the
// record, so a rule edited in the UI meanwhile is not overwritten.
func (am *AlertManager) saveAppAlertState(recordID string, state appAlertState) error {
	record, err := am.hub.FindRecordById(appAlertsCollection, recordID)
	if err != nil {
		return err
	}
	if len(state) == 0 {
		record.Set("state", nil)
	} else {
		record.Set("state", state)
	}
	return am.hub.SaveNoValidate(record)
}

func appMetricLabel(metric string) string {
	if metric == "cpu" {
		return "CPU"
	}
	return metric
}

func formatAppLimit(metric string, limit float64) string {
	if metric == "cpu" {
		return fmt.Sprintf("%g%%", limit)
	}
	return fmt.Sprintf("%g GB", limit)
}

func formatAppUsage(metric string, value float64) string {
	if metric == "cpu" {
		return fmt.Sprintf("%.1f%% of the host's CPU", value)
	}
	return fmt.Sprintf("%.2f GB of memory", value)
}

func plural(n int, one, other string) string {
	if n == 1 {
		return one
	}
	return other
}
