package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Process monitoring, for agents run with PROCESSES=true.
//
//   - process_stats: per-group usage over time, the same shape and retention
//     tiers as container_stats, so charts and averaging work the same way.
//   - processes: the latest top list of individual processes, one row per
//     system (id = system id), replaced on every agent update.
//
// Read rules are applied by the hub at startup with the other system-scoped
// collections; writes only ever come from the hub.
func init() {
	m.Register(func(app core.App) error {
		systems, err := app.FindCollectionByNameOrId("systems")
		if err != nil {
			return err
		}

		stats := core.NewBaseCollection("process_stats")
		stats.Fields.Add(
			&core.RelationField{Name: "system", CollectionId: systems.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.JSONField{Name: "stats", Required: true, MaxSize: 2_000_000},
			&core.SelectField{Name: "type", Required: true, MaxSelect: 1, Values: []string{"1m", "10m", "20m", "120m", "480m"}},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		stats.AddIndex("idx_process_stats_system_type_created", false, "`system`, `type`, `created`", "")
		if err := app.Save(stats); err != nil {
			return err
		}

		procs := core.NewBaseCollection("processes")
		procs.Fields.Add(
			&core.RelationField{Name: "system", CollectionId: systems.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.JSONField{Name: "procs", MaxSize: 2_000_000},
			// unix milliseconds of the agent update, as in the containers collection
			&core.NumberField{Name: "updated", OnlyInt: true},
		)
		procs.AddIndex("idx_processes_system", true, "`system`", "")
		return app.Save(procs)
	}, func(app core.App) error {
		for _, name := range []string{"processes", "process_stats"} {
			if c, err := app.FindCollectionByNameOrId(name); err == nil {
				if err := app.Delete(c); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
