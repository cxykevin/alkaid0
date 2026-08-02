package structs

import (
	"testing"
)

func TestBuildDefault(t *testing.T) {
	type TestStruct struct {
		Name  string  `default:"test"`
		Age   int     `default:"20"`
		Score float64 `default:"95.5"`
		Valid bool    `default:"true"`
	}

	ts := BuildDefault(TestStruct{})
	if ts.Name != "test" {
		t.Errorf("Expected Name 'test', got %s", ts.Name)
	}
	if ts.Age != 20 {
		t.Errorf("Expected Age 20, got %d", ts.Age)
	}
	if ts.Score != 95.5 {
		t.Errorf("Expected Score 95.5, got %f", ts.Score)
	}
	if ts.Valid != true {
		t.Errorf("Expected Valid true, got %v", ts.Valid)
	}
}

func TestModelsConfig(t *testing.T) {
	mc := ModelsConfig{}
	mc = BuildDefault(mc)
	if mc.ProviderURL == "" {
		t.Error("ProviderURL should not be empty after BuildDefault")
	}
}

func TestDataMaskDefaults(t *testing.T) {
	c := BuildDefault(Config{})
	if !c.DataMask.Enable {
		t.Error("DataMask.Enable should default to true")
	}
	if !c.DataMask.MaskAPIKey || !c.DataMask.MaskPhone || !c.DataMask.MaskIP ||
		!c.DataMask.MaskSession || !c.DataMask.MaskJWT {
		t.Error("DataMask category toggles should default to true")
	}
}
