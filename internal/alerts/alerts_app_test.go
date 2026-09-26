//go:build testing

package alerts_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/henrygd/beszel/internal/alerts"
	"github.com/henrygd/beszel/internal/entities/container"
	"github.com/henrygd/beszel/internal/entities/system"
	beszelTests "github.com/henrygd/beszel/internal/tests"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type appAlertFixture struct {
	now          time.Time
	hub          *beszelTests.TestHub
	am           *alerts.AlertManager
	userID       string
	systemRecord *core.Record
}

func newAppAlertFixture(t *testing.T) *appAlertFixture {
	t.Helper()
	hub, user := beszelTests.GetHubWithUser(t)
	systems, err := beszelTests.CreateSystems(hub, 1, user.Id, "up")
	require.NoError(t, err)
	settings, err := hub.FindFirstRecordByFilter("user_settings", "user={:user}", map[string]any{"user": user.Id})
	require.NoError(t, err)
	settings.Set("settings", `{"emails":["test@example.com"],"webhooks":[]}`)
	require.NoError(t, hub.Save(settings))
	return &appAlertFixture{now: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC), hub: hub, am: alerts.NewTestAlertManagerWithoutWorker(hub), userID: user.Id, systemRecord: systems[0]}
}

func (f *appAlertFixture) rule(t *testing.T, app string, fields map[string]any) *core.Record {
	t.Helper()
	fields["user"] = f.userID
	fields["system"] = f.systemRecord.Id
	fields["app"] = app
	r, err := beszelTests.CreateRecord(f.hub, "app_alerts", fields)
	require.NoError(t, err)
	return r
}

// submit sends one agent update; memory is in GB.
func (f *appAlertFixture) submit(t *testing.T, containers map[string][2]float64) {
	t.Helper()
	stats := make([]*container.Stats, 0, len(containers))
	for name, v := range containers {
		stats = append(stats, &container.Stats{Name: name, Cpu: v[0], Mem: v[1] * 1024})
	}
	require.NoError(t, f.am.HandleAppAlertsAt(f.systemRecord, &system.CombinedData{Containers: stats}, f.now))
}

func (f *appAlertFixture) sent() int { return f.hub.TestMailer.TotalSend() }

func (f *appAlertFixture) lastSubject() string { return f.hub.TestMailer.LastMessage().Subject }

// tick moves the clock to the next agent update, a minute later.
func (f *appAlertFixture) tick() { f.now = f.now.Add(time.Minute) }

func TestAppAlertFiresAfterDurationAndResolves(t *testing.T) {
	f := newAppAlertFixture(t)
	defer f.hub.Cleanup()
	f.rule(t, "*", map[string]any{"memory": 2, "min": 10})

	for range 10 { // over the limit, but not yet for 10 minutes
		f.submit(t, map[string][2]float64{"web": {1, 2.5}, "api": {1, 0.5}})
		f.tick()
	}
	assert.Equal(t, 0, f.sent(), "should wait the full duration")

	f.submit(t, map[string][2]float64{"web": {1, 2.5}, "api": {1, 0.5}})
	require.Equal(t, 1, f.sent())
	assert.Contains(t, f.lastSubject(), "web memory above 2 GB")

	f.tick()
	f.submit(t, map[string][2]float64{"web": {1, 2.5}, "api": {1, 0.5}})
	assert.Equal(t, 1, f.sent(), "must not repeat while still over")

	f.tick()
	f.submit(t, map[string][2]float64{"web": {1, 1.5}, "api": {1, 0.5}})
	require.Equal(t, 2, f.sent())
	assert.Contains(t, f.lastSubject(), "web memory back below 2 GB")
}

func TestAppAlertShortSpikeDoesNotFire(t *testing.T) {
	f := newAppAlertFixture(t)
	defer f.hub.Cleanup()
	f.rule(t, "*", map[string]any{"cpu": 20, "min": 10})

	for i := range 25 {
		cpu := 50.0
		if i == 5 || i == 15 { // dips below, restarting the clock
			cpu = 5
		}
		f.submit(t, map[string][2]float64{"web": {cpu, 0.1}})
		f.tick()
	}
	// runs of 5, 9 and 9 minutes over the limit: none reaches 10
	assert.Equal(t, 0, f.sent())
}

func TestAppAlertSumsContainersOfOneApp(t *testing.T) {
	f := newAppAlertFixture(t)
	defer f.hub.Cleanup()
	f.rule(t, "*", map[string]any{"memory": 2, "min": 1})

	// 1.2 + 1.0 GB across two containers of "glitchtip"; each alone is under
	f.submit(t, map[string][2]float64{"glitchtip/web": {1, 1.2}, "glitchtip/worker": {1, 1.0}})
	require.Equal(t, 1, f.sent())
	assert.Contains(t, f.lastSubject(), "glitchtip memory above 2 GB")
}

func TestAppAlertOverridesAndMutes(t *testing.T) {
	f := newAppAlertFixture(t)
	defer f.hub.Cleanup()
	f.rule(t, "*", map[string]any{"memory": 2, "cpu": 20, "min": 1})
	f.rule(t, "big", map[string]any{"memory": 3})        // higher memory limit, default cpu
	f.rule(t, "noisy", map[string]any{"disabled": true}) // muted

	f.submit(t, map[string][2]float64{"big": {1, 2.5}, "noisy": {90, 9}, "small": {1, 0.1}})
	assert.Equal(t, 0, f.sent(), "big is under its own limit and noisy is muted")

	f.submit(t, map[string][2]float64{"big": {30, 2.5}, "noisy": {90, 9}, "small": {1, 0.1}})
	require.Equal(t, 1, f.sent(), "big still inherits the default cpu limit")
	assert.Contains(t, f.lastSubject(), "big CPU above 20%")
}

func TestAppAlertStoppedAppAndUnknownState(t *testing.T) {
	f := newAppAlertFixture(t)
	defer f.hub.Cleanup()
	f.rule(t, "*", map[string]any{"memory": 2, "min": 1})

	f.submit(t, map[string][2]float64{"web": {1, 2.5}})
	require.Equal(t, 1, f.sent())

	// an update without Docker data must not resolve anything
	require.NoError(t, f.am.HandleAppAlertsAt(f.systemRecord, &system.CombinedData{}, f.now))
	assert.Equal(t, 1, f.sent())

	f.submit(t, map[string][2]float64{"api": {1, 0.1}})
	require.Equal(t, 2, f.sent())
	assert.Contains(t, f.lastSubject(), "web is no longer running")
}

func TestAppAlertsConfigSync(t *testing.T) {
	f := newAppAlertFixture(t)
	defer f.hub.Cleanup()
	systemName := f.systemRecord.GetString("name")
	uiRule := f.rule(t, "manual", map[string]any{"memory": 5})

	path := filepath.Join(t.TempDir(), "app-alerts.json")
	t.Setenv("APP_ALERTS_CONFIG", path)
	write := func(s string) { require.NoError(t, os.WriteFile(path, []byte(s), 0o600)) }
	configRules := func() []*core.Record {
		rs, err := f.hub.FindAllRecords("app_alerts", dbx.HashExp{"source": "config"})
		require.NoError(t, err)
		return rs
	}

	write(`{"_comment": "ignored", "` + systemName + `": {"*": {"cpu": 20, "memory": 2, "min": 10}, "big": {"memory": 3}, "_note": {}}, "no-such-system": {"*": {"memory": 1}}}`)
	require.NoError(t, f.am.SyncAppAlertsConfig())
	require.Len(t, configRules(), 2)
	def, err := f.hub.FindFirstRecordByFilter("app_alerts", "app='*'", nil)
	require.NoError(t, err)
	assert.Equal(t, 20.0, def.GetFloat("cpu"))
	assert.Equal(t, 10, def.GetInt("min"))

	// removing a rule from the file deletes it; the UI rule is untouched
	write(`{"` + systemName + `": {"*": {"cpu": 25}}}`)
	require.NoError(t, f.am.SyncAppAlertsConfig())
	rules := configRules()
	require.Len(t, rules, 1)
	assert.Equal(t, 25.0, rules[0].GetFloat("cpu"))
	assert.Equal(t, 0.0, rules[0].GetFloat("memory"), "omitted fields are cleared")
	_, err = f.hub.FindRecordById("app_alerts", uiRule.Id)
	assert.NoError(t, err)

	// a broken or invalid file changes nothing
	write(`{"` + systemName + `": {"*": {"cpu": 25, "typo": 1}}}`)
	assert.Error(t, f.am.SyncAppAlertsConfig())
	write(`not json`)
	assert.Error(t, f.am.SyncAppAlertsConfig())
	assert.Len(t, configRules(), 1)
}
