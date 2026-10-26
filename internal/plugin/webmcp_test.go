package plugin

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLoadedPluginsReturnsDeepCopiesInLoadOrder(t *testing.T) {
	minimum, maximum := float64(1), float64(5)
	first := PluginMetadata{
		ID: "first", Name: "First", Settings: []SettingSpec{{Key: "one", Default: "default", Min: &minimum, Max: &maximum}},
		Permissions: []PermissionSpec{{Hosts: []string{"example.test"}}},
		WebMCPTools: json.RawMessage(`[{"name":"one"}]`),
	}
	wantTools := append(json.RawMessage(nil), first.WebMCPTools...)
	second := PluginMetadata{ID: "second", Name: "Second", WebMCPTools: json.RawMessage(`[]`)}
	runtime := &Runtime{
		plugins: map[string]*pluginState{
			"first":  {meta: first},
			"second": {meta: second},
		},
		order: []string{"first", "second"},
	}
	got := runtime.LoadedPlugins()
	if len(got) != 2 || got[0].ID != "first" || got[1].ID != "second" {
		t.Fatalf("LoadedPlugins() order = %+v, want first then second", got)
	}
	got[0].WebMCPTools[2] = 'X'
	got[0].Settings[0].Key = "mutated"
	got[0].Settings[0].Default = "mutated"
	*got[0].Settings[0].Min = 9
	got[0].Permissions[0].Hosts[0] = "mutated.test"
	got[0].Name = "mutated"
	got[0].WebMCPTools = append(got[0].WebMCPTools, ' ')
	again := runtime.LoadedPlugins()
	if again[0].Name != "First" || again[0].Settings[0].Key != "one" || again[0].Settings[0].Default != "default" || *again[0].Settings[0].Min != 1 ||
		again[0].Permissions[0].Hosts[0] != "example.test" || !reflect.DeepEqual(again[0].WebMCPTools, wantTools) {
		t.Fatalf("mutating returned metadata changed runtime state: %+v", again[0])
	}
	if runtimeNil := (*Runtime)(nil).LoadedPlugins(); len(runtimeNil) != 0 {
		t.Fatalf("nil Runtime LoadedPlugins() = %+v, want empty", runtimeNil)
	}
}
