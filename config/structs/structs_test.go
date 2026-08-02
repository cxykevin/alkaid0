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
	// 默认仅脱敏 apikey（含长 token）与 jwt；手机号/IP/session 默认关闭，避免误伤
	if !c.DataMask.MaskAPIKey {
		t.Error("DataMask.MaskAPIKey should default to true")
	}
	if !c.DataMask.MaskJWT {
		t.Error("DataMask.MaskJWT should default to true")
	}
	if c.DataMask.MaskPhone {
		t.Error("DataMask.MaskPhone should default to false")
	}
	if c.DataMask.MaskIP {
		t.Error("DataMask.MaskIP should default to false")
	}
	if c.DataMask.MaskSession {
		t.Error("DataMask.MaskSession should default to false")
	}
}
