package actions

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/context/codebase"
	"github.com/cxykevin/alkaid0/product"
	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// cmdObj cmd 对象描述
type cmdObj struct {
	Description string
	Hint        string
	Function    func(*sessionObj, string) (bool, error)
}

// commandMaps 存储所有聊天命令及其对应处理函数
var commandMaps = map[string]*cmdObj{
	"/approve": {
		Description: "Approve a request",
		Hint:        "(no args)",
		Function: func(obj *sessionObj, _ string) (bool, error) {
			err := obj.loop.Approve()
			if err != nil {
				return false, err
			}
			return true, nil
		},
	},
	"/compress": {
		Description: "Compress the history",
		Hint:        "(no args)",
		Function: func(obj *sessionObj, _ string) (bool, error) {
			err := obj.loop.Summary()
			if err != nil {
				return false, err
			}
			return true, nil
		},
	},
	"/effort": {
		Description: "Set the reasoning effort (low | medium | high | max | xhigh | unset)",
		Hint:        "reasoning effort",
		Function: func(obj *sessionObj, arg string) (bool, error) {
			effortArg := strings.TrimSpace(strings.ToLower(arg))
			if effortArg == "low" || effortArg == "medium" || effortArg == "high" || effortArg == "max" || effortArg == "xhigh" || effortArg == "unset" {
				obj.session.ReasoningEffort = effortArg
				err := obj.session.DB.Model(&structs.Chats{}).Where("id = ?", obj.session.ID).Update("reasoning_effort", effortArg).Error
				return false, err
			}
			return false, fmt.Errorf("Unknown reasoning effort")
		},
	},
	"/reload": {
		Description: "Reload config file from disk",
		Hint:        "(no args)",
		Function: func(obj *sessionObj, arg string) (bool, error) {
			config.Reload()
			return false, nil
		},
	},
	"/background": {
		Description: "Set background mode on/off — keep session alive after all clients disconnect",
		Hint:        "on|off",
		Function: func(obj *sessionObj, arg string) (bool, error) {
			arg = strings.TrimSpace(strings.ToLower(arg))
			switch arg {
			case "on":
				obj.background = true
				return false, nil
			case "off":
				obj.background = false
				return false, nil
			default:
				return false, fmt.Errorf("Usage: /background on|off")
			}
		},
	},
	"/index": {
		Description: "Scan working dir and build codebase index (extract LSP symbols → submit embedding tasks)",
		Hint:        "(no args) or 'clean' | 'status'",
		Function: func(obj *sessionObj, arg string) (bool, error) {
			sessionID := cwd2SessionID(obj.cwd, obj.id)
			switch strings.TrimSpace(arg) {
			case "clean":
				if err := codebase.CleanDirectory(obj.cwd); err != nil {
					return false, fmt.Errorf("clean codebase: %w", err)
				}
				broadcastSessionUpdate(sessionID, SessionUpdate{
					SessionID: sessionID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "alk.cxykevin.top/session_stop",
						Content: map[string]string{
							"stopReason": "end_turn",
						},
					},
				}, 0)
				return false, nil
			case "status":
				status := codebase.GetIndexStatus(obj.cwd)
				r := "No index in progress."
				if status != nil {
					r = fmt.Sprintf("Indexing: **%s** | %d/%d processed | %d remaining | current: %s",
						status.Status, status.Processed, status.Total, status.Remaining, status.CurrentFile)
					if status.Error != "" {
						r += fmt.Sprintf("\nError: %s", status.Error)
					}
				}
				_ = broadcastSessionUpdate(sessionID, SessionUpdate{
					SessionID: sessionID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "agent_message_chunk",
						Content: u.H{
							"type": "text",
							"text": r,
						},
					},
				}, 0)
				return false, nil
			case "cancel":
				if err := codebase.CancelIndex(obj.cwd); err != nil {
					return false, fmt.Errorf("cancel index: %w", err)
				}
				_ = broadcastSessionUpdate(sessionID, SessionUpdate{
					SessionID: sessionID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "agent_message_chunk",
						Content: u.H{
							"type": "text",
							"text": "Index cancelled.",
						},
					},
				}, 0)
				return false, nil
			default:
				// arg 为空或其他情况 → 开始索引
			}
			go func() {
				broadcastFn := func(status codebase.IndexStatus) {
					_ = broadcastSessionUpdate(sessionID, SessionUpdate{
						SessionID: sessionID,
						Update: SessionUpdateUpdate{
							SessionUpdate: "alk.cxykevin.top/context/embedding/status",
							Content:       status,
						},
					}, 0)
				}
				broadcastFn(codebase.IndexStatus{Status: "scanning"})
				if err := codebase.RunIndex(context.Background(), obj.cwd, broadcastFn); err != nil {
					broadcastFn(codebase.IndexStatus{
						Status: "error",
						Error:  err.Error(),
					})
					return
				}
				broadcastFn(codebase.IndexStatus{Status: "completed"})
			}()
			return false, nil
		},
	},
	"/version": {
		Description: "Show Alkaid0 version information",
		Hint:        "(no args)",
		Function: func(obj *sessionObj, arg string) (bool, error) {
			sessionID := cwd2SessionID(obj.cwd, obj.id)
			versionInfo := fmt.Sprintf(`**Version:**
  - Version: **%s** (Number %d)
  - Commit ID: %s
**Build:**
  - Time: %d
  - Note: %s
**System:**
  - OS: %s
  - Arch: %s
  - Current Time: %d`,
				product.Version,
				product.VersionID,
				product.CommitID,
				product.BuildTime,
				product.BuildNote,
				runtime.GOOS,
				runtime.GOARCH,
				time.Now().Unix(),
			)
			_ = broadcastSessionUpdate(sessionID, SessionUpdate{
				SessionID: sessionID,
				Update: SessionUpdateUpdate{
					SessionUpdate: "agent_message_chunk",
					Content: u.H{
						"type": "text",
						"text": versionInfo,
					},
				},
			}, 0)
			return false, nil
		},
	},
}
