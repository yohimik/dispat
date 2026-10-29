package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestExecutionJSONRefusesRemovedKeysBeforeDroppingLinks(t *testing.T) {
	for _, key := range []string{"workers", "role", "WoRkErS", "RoLe"} {
		for _, value := range []string{`null`, `{}`, `[]`, `"worker"`, `[{"name":"build-a"}]`} {
			t.Run(key+"/"+value, func(t *testing.T) {
				data := []byte(`{"execution":{"` + key + `":` + value + `}}`)
				var config File
				err := json.Unmarshal(data, &config)
				if err == nil || !strings.Contains(err.Error(), "execution."+strings.ToLower(key)+" was removed") {
					t.Fatalf("removed key decoded: config=%+v error=%v", config.Execution, err)
				}
			})
		}
	}
}

func TestExecutionJSONChoreographerRoundTripKeepsUnknownFieldBehavior(t *testing.T) {
	var config ExecutionConfig
	if err := json.Unmarshal([]byte(`{"choreographers":[{"name":"build-a","endpoint":"file:///mailbox.git"}],"unrelated":true}`), &config); err != nil {
		t.Fatal(err)
	}
	if !config.IsDistributed() || len(config.Workers) != 1 || config.Workers[0].Name != "build-a" {
		t.Fatalf("choreographer link was lost: %+v", config)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"workers"`) || !strings.Contains(string(data), `"choreographers"`) {
		t.Fatalf("unexpected execution spelling: %s", data)
	}
	var again ExecutionConfig
	if err := json.Unmarshal(data, &again); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config, again) {
		t.Fatalf("round trip changed execution: %+v != %+v", config, again)
	}
}
