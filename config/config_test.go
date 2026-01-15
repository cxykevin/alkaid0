package config

import (
	"os"
	"testing"
)

func TestConfig(t *testing.T) {
	os.Setenv("ALKAID0_CONFIG_PATH", "non_existent_config.json")
	Load()
	if GlobalConfig == nil {
		t.Fatal("GlobalConfig should not be nil after Load")
	}

	home, _ := os.UserHomeDir()
	if ExpandPath("~/test") != home+"/test" {
		t.Errorf("ExpandPath failed for ~")
	}
}
