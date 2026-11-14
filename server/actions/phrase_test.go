package actions

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

// TestPhraseListNeverReturnsNull 短语列表 RPC 的 phrases 字段永远不能是 null。
//
// 短语未启用或短语表为 nil（配置里写成 null、尚未初始化）时，nil 切片会被
// encoding/json 写成 "phrases": null；客户端配置树把 null 当成"非数组"，
// 结果既列不出短语也无法新增。
func TestPhraseListNeverReturnsNull(t *testing.T) {
	cases := []struct {
		name string
		cfg  cfgStructs.Config
	}{
		{name: "disabled", cfg: cfgStructs.Config{}},
		{name: "enabled but nil table", cfg: cfgStructs.Config{
			Context: cfgStructs.ContextConfig{
				Phrase: cfgStructs.PhraseConfig{Enable: true},
			},
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			restore := config.GlobalConfigSwap(c.cfg)
			defer restore()

			resp, err := PhraseList(PhraseListRequest{}, nil, 0)
			if err != nil {
				t.Fatalf("PhraseList: %v", err)
			}
			if resp.Phrases == nil {
				t.Fatal("Phrases should not be nil")
			}

			raw, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if strings.Contains(string(raw), `"phrases":null`) {
				t.Errorf("response still contains null phrases: %s", raw)
			}
			if !strings.Contains(string(raw), `"phrases":[]`) {
				t.Errorf(`response should contain "phrases":[], got %s`, raw)
			}
		})
	}
}
