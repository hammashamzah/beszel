package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// app_alerts holds per-app CPU and memory alert rules, one row per
// (user, system, app). The row with app "*" is the default for every app on
// the system. Rows with source "config" come from the APP_ALERTS_CONFIG file
// and can't be changed through the API.
func init() {
	m.Register(func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		systems, err := app.FindCollectionByNameOrId("systems")
		if err != nil {
			return err
		}

		owner := `@request.auth.id != "" && user = @request.auth.id`
		// clients may only write rules marked as their own, and never the runtime state
		clientWrite := `(@request.body.source:isset = false || @request.body.source = "ui") && @request.body.state:isset = false`

		c := core.NewBaseCollection("app_alerts")
		c.ListRule = types.Pointer(owner)
		c.ViewRule = types.Pointer(owner)
		c.CreateRule = types.Pointer(owner + " && " + clientWrite)
		c.UpdateRule = types.Pointer(owner + ` && source != "config" && ` + clientWrite + ` && (@request.body.user:isset = false || @request.body.user = @request.auth.id)`)
		c.DeleteRule = types.Pointer(owner + ` && source != "config"`)

		c.Fields.Add(
			&core.RelationField{Name: "user", CollectionId: users.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.RelationField{Name: "system", CollectionId: systems.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.TextField{Name: "app", Required: true, Max: 200},
			// percent of the host, as shown in the containers table; 0 = off (default rule) or inherit (override)
			&core.NumberField{Name: "cpu", Min: types.Pointer(0.0), Max: types.Pointer(100.0)},
			// gigabytes; 0 = off (default rule) or inherit (override)
			&core.NumberField{Name: "memory", Min: types.Pointer(0.0)},
			// minutes over the limit before alerting; 0 = inherit
			&core.NumberField{Name: "min", Min: types.Pointer(0.0), Max: types.Pointer(60.0), OnlyInt: true},
			&core.BoolField{Name: "disabled"},
			&core.SelectField{Name: "source", Values: []string{"ui", "config"}, MaxSelect: 1},
			&core.JSONField{Name: "state"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		c.AddIndex("idx_app_alerts_user_system_app", true, "`user`, `system`, `app`", "")
		c.AddIndex("idx_app_alerts_system", false, "`system`", "")
		return app.Save(c)
	}, func(app core.App) error {
		c, err := app.FindCollectionByNameOrId("app_alerts")
		if err != nil {
			return nil
		}
		return app.Delete(c)
	})
}
