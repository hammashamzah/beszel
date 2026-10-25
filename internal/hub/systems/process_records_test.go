//go:build testing

package systems_test

import (
	"encoding/json"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/henrygd/beszel/internal/entities/container"
	"github.com/henrygd/beszel/internal/entities/process"
	"github.com/henrygd/beszel/internal/entities/system"
	"github.com/henrygd/beszel/internal/tests"
	"github.com/pocketbase/dbx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateRecordsStoresProcesses(t *testing.T) {
	hub, user := tests.GetHubWithUser(t)
	defer hub.Cleanup()
	records, err := tests.CreateSystems(hub, 1, user.Id, "paused")
	require.NoError(t, err)
	systemId := records[0].Id
	sys, err := hub.GetSystemManager().GetSystem(systemId)
	require.NoError(t, err)

	send := func(groups []*container.Stats, procs []*process.Proc) {
		// round-trip through cbor, as the agent sends it
		encoded, err := cbor.Marshal(system.CombinedData{ProcessGroups: groups, Processes: procs})
		require.NoError(t, err)
		var data system.CombinedData
		require.NoError(t, cbor.Unmarshal(encoded, &data))
		_, err = sys.CreateRecords(&data)
		require.NoError(t, err)
	}

	send(
		[]*container.Stats{{Name: "cutroom (hammas)", Cpu: 1.5, Mem: 400}, {Name: "other", Cpu: 0.2, Mem: 90}},
		[]*process.Proc{{Pid: 42, User: "hammas", Name: "bun", Cmd: "bun cutroom.ts runner", Group: "cutroom (hammas)", Cpu: 1.5, Mem: 400 << 20, Start: 1790000000}},
	)
	send([]*container.Stats{{Name: "tailscaled", Cpu: 0.1, Mem: 120}}, []*process.Proc{{Pid: 7, User: "root", Name: "tailscaled", Group: "tailscaled"}})

	count, err := hub.CountRecords("process_stats", dbx.HashExp{"system": systemId, "type": "1m"})
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)

	// one processes row per system, holding the latest list
	var rows []struct {
		Procs string `db:"procs"`
	}
	require.NoError(t, hub.DB().Select("procs").From("processes").Where(dbx.HashExp{"system": systemId}).All(&rows))
	require.Len(t, rows, 1)
	var procs []process.Proc
	require.NoError(t, json.Unmarshal([]byte(rows[0].Procs), &procs))
	require.Len(t, procs, 1)
	assert.Equal(t, "tailscaled", procs[0].Name)

	// an agent without process monitoring leaves both alone
	_, err = sys.CreateRecords(&system.CombinedData{})
	require.NoError(t, err)
	count, err = hub.CountRecords("process_stats", dbx.HashExp{"system": systemId})
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)
}
