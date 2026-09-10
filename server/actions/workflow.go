package actions

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/cxykevin/alkaid0/storage/structs"
	"strings"
	"time"

	runTool "github.com/cxykevin/alkaid0/tools/tools/run"
	"gorm.io/gorm"
)

const workflowUpdatePrefix = "alk.cxykevin.top/session/terminal/workflow/update_"

func persistWorkflowEvent(sessionID, runID string, ev runTool.WorkflowEvent) {
	cwd, chatID, err := sessionID2Cwd(sessionID)
	if err != nil {
		return
	}
	db, err := loadDB(cwd)
	if err != nil {
		return
	}
	defer closeDB(cwd)
	var row structs.Workflows
	if db.Where("run_id = ?", runID).First(&row).Error != nil {
		row = structs.Workflows{WorkflowID: runID, ChatID: chatID, RunID: runID, Status: "running", CreatedAt: time.Now().UTC()}
		_ = db.Create(&row).Error
	}
	row.LastSequence++
	payload, _ := json.Marshal(ev.Data)
	_ = db.Create(&structs.WorkflowEvents{WorkflowID: runID, ChatID: chatID, Sequence: row.LastSequence, Type: ev.Type, NodeID: stringValue(ev.Data["nodeId"]), AgentIndex: intValue(ev.Data["agentIndex"]), PayloadJSON: string(payload), RawJSON: string(ev.Raw), CreatedAt: time.Now().UTC()}).Error
	updates := map[string]any{"last_sequence": row.LastSequence, "updated_at": time.Now().UTC()}
	if ev.Type == "graph" {
		updates["graph_json"] = string(payload)
	}
	if ev.Type == "node" || ev.Type == "agent" {
		updates["agent_state_json"] = string(payload)
		updates["current_node"] = stringValue(ev.Data["nodeId"])
	}
	_ = db.Model(&structs.Workflows{}).Where("run_id = ?", runID).Updates(updates).Error
}

func stringValue(v any) string { s, _ := v.(string); return s }
func intValue(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

func broadcastWorkflowEvent(sessionID, runID string, event any) {
	ev, ok := event.(runTool.WorkflowEvent)
	if !ok {
		return
	}
	if ev.Type == "" {
		return
	}
	persistWorkflowEvent(sessionID, runID, ev)
	method := workflowUpdatePrefix + ev.Type
	payload := map[string]any{
		"sessionUpdate": method,
		"sessionId":     sessionID,
		"runId":         runID,
		"eventType":     ev.Type,
		"time":          time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range ev.Data {
		payload[k] = v
	}
	if _, exists := payload["workflow"]; !exists {
		payload["workflow"] = runID
	}
	if err := broadcastSessionUpdate(sessionID, map[string]any{"sessionId": sessionID, "update": payload}, 0); err != nil {
		logger.Warn("workflow event broadcast failed: %v", err)
	}
	_ = json.Valid(ev.Raw)
}

func workflowMethodType(method string) (string, bool) {
	const prefix = "alk.cxykevin.top/session/terminal/workflow/"
	if !strings.HasPrefix(method, prefix) {
		return "", false
	}
	typ := strings.TrimPrefix(method, prefix)
	switch typ {
	case "status", "input", "stop", "list":
		return typ, true
	}
	return "", false
}

type SessionWorkflowRequest struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
}
type SessionWorkflowStatusResponse struct {
	RunID      string `json:"runId"`
	TerminalID string `json:"terminalId"`
	Status     string `json:"status"`
	Workflow   any    `json:"workflow,omitempty"`
	Graph      any    `json:"graph,omitempty"`
	AgentState any    `json:"agentState,omitempty"`
	Logs       []any  `json:"logs,omitempty"`
}
type SessionWorkflowInputRequest struct {
	SessionID string         `json:"sessionId"`
	RunID     string         `json:"runId"`
	Input     map[string]any `json:"input"`
}
type SessionWorkflowInputResponse struct {
	Accepted bool   `json:"accepted"`
	RunID    string `json:"runId"`
}
type SessionWorkflowListRequest struct {
	SessionID string `json:"sessionId"`
}
type SessionWorkflowListResponse struct {
	Workflows []SessionWorkflowStatusResponse `json:"workflows"`
}

func workflowJob(req SessionWorkflowRequest) (*runTool.Job, error) {
	if req.RunID == "" {
		return nil, fmt.Errorf("runId is empty")
	}
	job := runTool.Default.Find(req.RunID)
	if job == nil {
		return nil, fmt.Errorf("workflow run %s not found", req.RunID)
	}
	id, err := validateTerminalSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	if job.SessionID != id || job.BackgroundKind != "workflow" {
		return nil, fmt.Errorf("run does not belong to session")
	}
	return job, nil
}

func decodeWorkflowJSON(raw string) any {
	if raw == "" {
		return nil
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return nil
	}
	return value
}

func workflowSnapshot(db *gorm.DB, chatID uint32, runID, activeStatus string) (SessionWorkflowStatusResponse, error) {
	var row structs.Workflows
	if err := db.Where("chat_id = ? AND run_id = ?", chatID, runID).First(&row).Error; err != nil {
		return SessionWorkflowStatusResponse{}, err
	}
	status := row.Status
	if activeStatus != "" {
		status = activeStatus
	}
	var events []structs.WorkflowEvents
	if err := db.Where("chat_id = ? AND workflow_id = ?", chatID, runID).Order("sequence ASC").Find(&events).Error; err != nil {
		return SessionWorkflowStatusResponse{}, err
	}
	logs := make([]any, 0, len(events))
	for _, event := range events {
		logs = append(logs, map[string]any{"sequence": event.Sequence, "type": event.Type, "nodeId": event.NodeID, "agentIndex": event.AgentIndex, "payload": decodeWorkflowJSON(event.PayloadJSON), "raw": decodeWorkflowJSON(event.RawJSON), "createdAt": event.CreatedAt.UTC().Format(time.RFC3339Nano)})
	}
	workflow := map[string]any{"workflowId": row.WorkflowID, "runId": row.RunID, "terminalId": row.TerminalID, "name": row.Name, "status": status, "currentNode": row.CurrentNode, "currentAgent": row.CurrentAgent, "lastSequence": row.LastSequence, "createdAt": row.CreatedAt.UTC().Format(time.RFC3339Nano), "updatedAt": row.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	if row.Error != "" {
		workflow["error"] = row.Error
	}
	if row.ResultPath != "" {
		workflow["resultPath"] = row.ResultPath
	}
	return SessionWorkflowStatusResponse{RunID: runID, TerminalID: row.TerminalID, Status: status, Workflow: workflow, Graph: decodeWorkflowJSON(row.GraphJSON), AgentState: decodeWorkflowJSON(row.AgentStateJSON), Logs: logs}, nil
}

func SessionWorkflowStatus(req SessionWorkflowRequest, _ func(string, any, *string) error, _ uint64) (SessionWorkflowStatusResponse, error) {
	cwd, chatID, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return SessionWorkflowStatusResponse{}, err
	}
	if req.RunID == "" {
		return SessionWorkflowStatusResponse{}, fmt.Errorf("runId is empty")
	}
	db, err := loadDB(cwd)
	if err != nil {
		return SessionWorkflowStatusResponse{}, err
	}
	defer closeDB(cwd)
	activeStatus := ""
	if job := runTool.Default.Find(req.RunID); job != nil && job.SessionID == chatID && job.BackgroundKind == "workflow" {
		activeStatus = job.Status().String()
	}
	return workflowSnapshot(db, chatID, req.RunID, activeStatus)
}

func SessionWorkflowInput(req SessionWorkflowInputRequest, _ func(string, any, *string) error, _ uint64) (SessionWorkflowInputResponse, error) {
	job, err := workflowJob(SessionWorkflowRequest{SessionID: req.SessionID, RunID: req.RunID})
	if err != nil {
		return SessionWorkflowInputResponse{}, err
	}
	if len(req.Input) == 0 {
		return SessionWorkflowInputResponse{}, fmt.Errorf("input is empty")
	}
	cmd, ok := req.Input["cmd"].(string)
	if !ok {
		return SessionWorkflowInputResponse{}, fmt.Errorf("input.cmd must be string")
	}
	switch cmd {
	case "shutdown":
	case "node", "agent":
	default:
		return SessionWorkflowInputResponse{}, fmt.Errorf("unsupported workflow command: %s", cmd)
	}
	raw, err := json.Marshal(req.Input)
	if err != nil {
		return SessionWorkflowInputResponse{}, err
	}
	raw = append(raw, '\n')
	if err := job.WriteStdin(raw); err != nil {
		return SessionWorkflowInputResponse{}, err
	}
	return SessionWorkflowInputResponse{Accepted: true, RunID: req.RunID}, nil
}

func SessionWorkflowStop(req SessionWorkflowRequest, _ func(string, any, *string) error, _ uint64) (SessionWorkflowInputResponse, error) {
	job, err := workflowJob(req)
	if err != nil {
		return SessionWorkflowInputResponse{}, err
	}
	raw := []byte("{\"cmd\":\"shutdown\"}\n")
	if err := job.WriteStdin(raw); err != nil {
		return SessionWorkflowInputResponse{}, err
	}
	return SessionWorkflowInputResponse{Accepted: true, RunID: req.RunID}, nil
}

func SessionWorkflowList(req SessionWorkflowListRequest, _ func(string, any, *string) error, _ uint64) (SessionWorkflowListResponse, error) {
	id, err := validateTerminalSession(req.SessionID)
	if err != nil {
		return SessionWorkflowListResponse{}, err
	}
	jobs := runTool.Default.ListActive(id)
	out := make([]SessionWorkflowStatusResponse, 0, len(jobs))
	for _, job := range jobs {
		if job.BackgroundKind != "workflow" {
			continue
		}
		out = append(out, SessionWorkflowStatusResponse{RunID: job.ID, TerminalID: job.ID, Status: job.Status().String()})
	}
	return SessionWorkflowListResponse{Workflows: out}, nil
}

func workflowError(runID string, err error) error { return fmt.Errorf("workflow %s: %w", runID, err) }
