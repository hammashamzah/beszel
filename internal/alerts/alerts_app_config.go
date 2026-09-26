package alerts

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/henrygd/beszel/internal/hub/utils"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// appAlertSpec is one rule in the APP_ALERTS_CONFIG file. Omitted fields are
// stored as zero, which means "off" on the default rule and "inherit the
// default" on an app's override.
type appAlertSpec struct {
	CPU      float64 `json:"cpu"`
	Memory   float64 `json:"memory"`
	Min      int     `json:"min"`
	Disabled bool    `json:"disabled"`
}

// SyncAppAlertsConfig applies app alert rules from the JSON file named by
// APP_ALERTS_CONFIG. The file maps system names to apps to rules; "*" is the
// default for every app on that system, and keys starting with "_" are
// ignored so the file can carry comments:
//
//	{
//	  "vienna-1": {
//	    "*":            { "cpu": 20, "memory": 2, "min": 10 },
//	    "readback-web": { "memory": 3 },
//	    "ci-runner":    { "disabled": true }
//	  }
//	}
//
// Each rule is created for every user of that system and marked as coming from
// config, which makes it read-only in the UI. Config rules no longer in the
// file are deleted; rules made in the UI are left alone unless the file
// defines the same app, in which case the file wins. A file that can't be read
// or parsed changes nothing.
func (am *AlertManager) SyncAppAlertsConfig() error {
	path, ok := utils.GetEnv("APP_ALERTS_CONFIG")
	if !ok || path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("app alerts config: %w", err)
	}
	config, err := parseAppAlertsConfig(raw)
	if err != nil {
		return fmt.Errorf("app alerts config %s: %w", path, err)
	}

	collection, err := am.hub.FindCollectionByNameOrId(appAlertsCollection)
	if err != nil {
		return err
	}
	keep := make(map[string]bool)
	for systemName, apps := range config {
		systems, err := am.hub.FindAllRecords("systems", dbx.HashExp{"name": systemName})
		if err != nil {
			return err
		}
		if len(systems) == 0 {
			am.hub.Logger().Warn("App alerts config names an unknown system", "system", systemName)
			continue
		}
		for _, systemRecord := range systems {
			for _, userID := range systemRecord.GetStringSlice("users") {
				for app, spec := range apps {
					record, err := am.hub.FindFirstRecordByFilter(appAlertsCollection,
						"user={:user} && system={:system} && app={:app}",
						dbx.Params{"user": userID, "system": systemRecord.Id, "app": app})
					if err != nil {
						record = core.NewRecord(collection)
						record.Set("user", userID)
						record.Set("system", systemRecord.Id)
						record.Set("app", app)
					}
					record.Set("cpu", spec.CPU)
					record.Set("memory", spec.Memory)
					record.Set("min", spec.Min)
					record.Set("disabled", spec.Disabled)
					record.Set("source", appAlertSourceConfig)
					if err := am.hub.Save(record); err != nil {
						return fmt.Errorf("app alerts config %s/%s: %w", systemName, app, err)
					}
					keep[record.Id] = true
				}
			}
		}
	}

	stale, err := am.hub.FindAllRecords(appAlertsCollection, dbx.HashExp{"source": appAlertSourceConfig})
	if err != nil {
		return err
	}
	for _, record := range stale {
		if !keep[record.Id] {
			if err := am.hub.Delete(record); err != nil {
				return err
			}
		}
	}
	am.hub.Logger().Info("Synced app alerts config", "path", path, "rules", len(keep))
	return nil
}

func parseAppAlertsConfig(raw []byte) (map[string]map[string]appAlertSpec, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	config := make(map[string]map[string]appAlertSpec)
	for systemName, systemRaw := range top {
		if strings.HasPrefix(systemName, "_") {
			continue
		}
		var apps map[string]json.RawMessage
		if err := json.Unmarshal(systemRaw, &apps); err != nil {
			return nil, fmt.Errorf("%s: %w", systemName, err)
		}
		config[systemName] = make(map[string]appAlertSpec)
		for app, specRaw := range apps {
			if strings.HasPrefix(app, "_") {
				continue
			}
			dec := json.NewDecoder(strings.NewReader(string(specRaw)))
			dec.DisallowUnknownFields()
			var spec appAlertSpec
			if err := dec.Decode(&spec); err != nil {
				return nil, fmt.Errorf("%s/%s: %w", systemName, app, err)
			}
			if spec.CPU < 0 || spec.CPU > 100 || spec.Memory < 0 || spec.Min < 0 || spec.Min > 60 {
				return nil, fmt.Errorf("%s/%s: cpu must be 0-100, memory >= 0, min 0-60", systemName, app)
			}
			config[systemName][app] = spec
		}
	}
	return config, nil
}
