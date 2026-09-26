package actions

import (
	"errors"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
)

// callRecorder 记录假 Call 收到的入参，并按预设结果返回。
type callRecorder struct {
	calls []any
	ret   any
	err   error
}

func (r *callRecorder) call(obj any) (any, error) {
	r.calls = append(r.calls, obj)
	return r.ret, r.err
}

// installFakeCall 用假实现替换包级 Call，并注册清理恢复原值，避免影响其它测试。
func installFakeCall(t *testing.T, ret any, err error) *callRecorder {
	t.Helper()
	rec := &callRecorder{ret: ret, err: err}
	old := Call
	Call = rec.call
	t.Cleanup(func() { Call = old })
	return rec
}

// onlyCallArg 断言假 Call 恰好被调用一次，并返回那次调用的入参。
func onlyCallArg(t *testing.T, rec *callRecorder) any {
	t.Helper()
	if len(rec.calls) != 1 {
		t.Fatalf("Call invoked %d times, want exactly 1", len(rec.calls))
	}
	return rec.calls[0]
}

// TestConsumerName 消费者名是跨包 IPC 的注册键，改名会让 agent 操作分发静默失联。
func TestConsumerName(t *testing.T) {
	if ConsumerName != "agents" {
		t.Fatalf("ConsumerName = %q, want %q", ConsumerName, "agents")
	}
}

// TestAddAgent 入参逐字段透传；Call 报错时原样返回。
func TestAddAgent(t *testing.T) {
	session := &structs.Chats{}
	rec := installFakeCall(t, nil, nil)
	if err := AddAgent(session, "code-a", "id-1", "/tmp/p"); err != nil {
		t.Fatalf("AddAgent() error = %v, want nil", err)
	}
	arg, ok := onlyCallArg(t, rec).(Add)
	if !ok {
		t.Fatalf("Call arg type = %T, want actions.Add", rec.calls[0])
	}
	if arg.Session != session || arg.AgentCode != "code-a" || arg.AgentID != "id-1" || arg.Path != "/tmp/p" {
		t.Errorf("Add = %#v, want all fields forwarded verbatim", arg)
	}

	wantErr := errors.New("add failed")
	installFakeCall(t, "ignored", wantErr)
	if err := AddAgent(session, "code-a", "id-1", "/tmp/p"); !errors.Is(err, wantErr) {
		t.Errorf("AddAgent() error = %v, want %v", err, wantErr)
	}
}

// TestUpdateAgent 入参逐字段透传；Call 报错时原样返回。
func TestUpdateAgent(t *testing.T) {
	session := &structs.Chats{}
	rec := installFakeCall(t, nil, nil)
	if err := UpdateAgent(session, "code-b", "id-2", "/tmp/q"); err != nil {
		t.Fatalf("UpdateAgent() error = %v, want nil", err)
	}
	arg, ok := onlyCallArg(t, rec).(Update)
	if !ok {
		t.Fatalf("Call arg type = %T, want actions.Update", rec.calls[0])
	}
	if arg.Session != session || arg.AgentCode != "code-b" || arg.AgentID != "id-2" || arg.Path != "/tmp/q" {
		t.Errorf("Update = %#v, want all fields forwarded verbatim", arg)
	}

	wantErr := errors.New("update failed")
	installFakeCall(t, "ignored", wantErr)
	if err := UpdateAgent(session, "code-b", "id-2", "/tmp/q"); !errors.Is(err, wantErr) {
		t.Errorf("UpdateAgent() error = %v, want %v", err, wantErr)
	}
}

// TestDeleteAgent 入参逐字段透传；Call 报错时原样返回。
func TestDeleteAgent(t *testing.T) {
	session := &structs.Chats{}
	rec := installFakeCall(t, nil, nil)
	if err := DeleteAgent(session, "code-c"); err != nil {
		t.Fatalf("DeleteAgent() error = %v, want nil", err)
	}
	arg, ok := onlyCallArg(t, rec).(Del)
	if !ok {
		t.Fatalf("Call arg type = %T, want actions.Del", rec.calls[0])
	}
	if arg.Session != session || arg.AgentCode != "code-c" {
		t.Errorf("Del = %#v, want all fields forwarded verbatim", arg)
	}

	wantErr := errors.New("delete failed")
	installFakeCall(t, "ignored", wantErr)
	if err := DeleteAgent(session, "code-c"); !errors.Is(err, wantErr) {
		t.Errorf("DeleteAgent() error = %v, want %v", err, wantErr)
	}
}

// TestActivateAgent 入参逐字段透传；Call 报错时原样返回。
func TestActivateAgent(t *testing.T) {
	session := &structs.Chats{}
	rec := installFakeCall(t, nil, nil)
	if err := ActivateAgent(session, "code-d", "do the thing"); err != nil {
		t.Fatalf("ActivateAgent() error = %v, want nil", err)
	}
	arg, ok := onlyCallArg(t, rec).(Activate)
	if !ok {
		t.Fatalf("Call arg type = %T, want actions.Activate", rec.calls[0])
	}
	if arg.Session != session || arg.AgentCode != "code-d" || arg.Prompt != "do the thing" {
		t.Errorf("Activate = %#v, want all fields forwarded verbatim", arg)
	}

	wantErr := errors.New("activate failed")
	installFakeCall(t, "ignored", wantErr)
	if err := ActivateAgent(session, "code-d", "do the thing"); !errors.Is(err, wantErr) {
		t.Errorf("ActivateAgent() error = %v, want %v", err, wantErr)
	}
}

// TestDeactivateAgent 入参逐字段透传；Call 报错时原样返回。
func TestDeactivateAgent(t *testing.T) {
	session := &structs.Chats{}
	rec := installFakeCall(t, nil, nil)
	if err := DeactivateAgent(session, "summary"); err != nil {
		t.Fatalf("DeactivateAgent() error = %v, want nil", err)
	}
	arg, ok := onlyCallArg(t, rec).(Deactivate)
	if !ok {
		t.Fatalf("Call arg type = %T, want actions.Deactivate", rec.calls[0])
	}
	if arg.Session != session || arg.Prompt != "summary" {
		t.Errorf("Deactivate = %#v, want all fields forwarded verbatim", arg)
	}

	wantErr := errors.New("deactivate failed")
	installFakeCall(t, "ignored", wantErr)
	if err := DeactivateAgent(session, "summary"); !errors.Is(err, wantErr) {
		t.Errorf("DeactivateAgent() error = %v, want %v", err, wantErr)
	}
}

// TestListAgent 成功路径返回消费者给的切片；Call 报错与结果类型不符都要报错而不是静默返回 nil。
func TestListAgent(t *testing.T) {
	session := &structs.Chats{}
	rec := installFakeCall(t, []structs.SubAgents{{ID: "id-a", AgentID: "a"}, {ID: "id-b", AgentID: "b"}}, nil)
	got, err := ListAgent(session)
	if err != nil {
		t.Fatalf("ListAgent() error = %v, want nil", err)
	}
	if len(got) != 2 || got[0].AgentID != "a" || got[1].AgentID != "b" {
		t.Errorf("ListAgent() = %#v, want the two entries from the consumer", got)
	}
	arg, ok := onlyCallArg(t, rec).(List)
	if !ok {
		t.Fatalf("Call arg type = %T, want actions.List", rec.calls[0])
	}
	if arg.Session != session {
		t.Errorf("List.Session = %p, want %p", arg.Session, session)
	}

	wantErr := errors.New("list failed")
	installFakeCall(t, []structs.SubAgents{}, wantErr)
	if _, err := ListAgent(session); !errors.Is(err, wantErr) {
		t.Errorf("ListAgent() error = %v, want %v", err, wantErr)
	}
}

// TestListAgentUnexpectedResultType 消费者返回非 []structs.SubAgents 时给出可定位的错误
// （含实际类型），而不是返回半个结果让上层 nil panic。
func TestListAgentUnexpectedResultType(t *testing.T) {
	installFakeCall(t, "not-a-list", nil)
	got, err := ListAgent(&structs.Chats{})
	if err == nil {
		t.Fatal("ListAgent() error = nil, want type mismatch error")
	}
	if got != nil {
		t.Errorf("ListAgent() = %#v, want nil alongside the error", got)
	}
	if !strings.Contains(err.Error(), "unexpected result type string") {
		t.Errorf("error = %q, want it to name the actual type", err.Error())
	}

	// 消费者返回 nil（例如未注册的 consumer 路径）同样走类型断言失败分支。
	installFakeCall(t, nil, nil)
	if _, err := ListAgent(&structs.Chats{}); err == nil {
		t.Error("ListAgent() with nil result: error = nil, want type mismatch error")
	}
}
