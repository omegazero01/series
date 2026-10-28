package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// newSession integrates SERIES policy through public SDK extension points only.
// The official dispatcher calls targets directly, so our outer hook must resolve
// their identity before applying business policy. No SDK source is replaced.
//
// settleGuard names the tools that must stay executable-free during the wrap-up
// phase but *remain declared* in the request. Hiding them (Settlement.DisabledTools)
// rewrites the tool array — which sits before the whole transcript — and therefore
// invalidates the provider cache for every wrap-up turn. Gating them here keeps the
// request prefix byte-stable for the entire run instead.
func newSession(opts agentcore.Options, settleGuard ...string) (*agentcore.Session, func(), error) {
	// An empty catalog is authoritative. The SDK interprets nil as a request
	// for its default filesystem/Bash tools, which would bypass role bindings.
	if opts.Tools == nil {
		opts.Tools = []actool.CoreTool{}
	}
	cleanup := func() {}
	if toolsNeedTaskManager(opts.Tools) && !actool.InteractiveShellDisabled() &&
		(opts.DisableBackgroundTasks || actool.BackgroundTasksDisabled()) {
		manager, err := actool.NewManager(opts.SessionID)
		if err != nil {
			return nil, cleanup, fmt.Errorf("interactive shell manager: %w", err)
		}
		owner := newInteractiveOwner(manager)
		cleanup = owner.close
		opts.Tools = append([]actool.CoreTool(nil), opts.Tools...)
		for i, tool := range opts.Tools {
			switch tool.Name() {
			case "shell_open", "shell_send", "shell_read", "shell_close", "shell_list":
				opts.Tools[i] = &interactiveTool{CoreTool: tool, owner: owner}
			}
		}
	}
	if len(opts.DeferredTools) > 0 && opts.UnlockSet == nil {
		opts.UnlockSet = actool.NewUnlockSet(opts.DeferredTools...)
	}
	policy := newDispatchHooks(opts)
	policy.watchSettlement(opts.Settlement, settleGuard...)
	policy.observeModelCalls(&opts)
	opts.Hooks = policy
	session := agentcore.NewSession(opts)
	return session, func() { session.Close(); cleanup() }, nil
}

type interactiveTool struct {
	actool.CoreTool
	owner *interactiveOwner
}

// The host owns this manager (the SDK does not see it). Drain its calls before
// cleanup so a canceled opening cannot publish a PTY after cleanup has passed.
type interactiveOwner struct {
	manager *actool.Manager
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	wg      sync.WaitGroup
	once    sync.Once
}

func newInteractiveOwner(manager *actool.Manager) *interactiveOwner {
	ctx, cancel := context.WithCancel(context.Background())
	return &interactiveOwner{manager: manager, ctx: ctx, cancel: cancel}
}

func (o *interactiveOwner) close() {
	o.once.Do(func() {
		o.mu.Lock()
		o.closed = true
		o.cancel()
		o.mu.Unlock()
		o.wg.Wait()
		o.manager.Cleanup()
	})
}

func (t *interactiveTool) PrepareInput(input json.RawMessage) (json.RawMessage, error) {
	return PrepareToolInput(t.CoreTool, input)
}

func (t *interactiveTool) AcceptsRawInput() bool {
	raw, ok := t.CoreTool.(interface{ AcceptsRawInput() bool })
	return ok && raw.AcceptsRawInput()
}

func (t *interactiveTool) Call(ctx context.Context, input json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	t.owner.mu.Lock()
	if t.owner.closed {
		t.owner.mu.Unlock()
		return actool.Errorf("interactive session is closed"), nil
	}
	t.owner.wg.Add(1)
	t.owner.mu.Unlock()
	defer t.owner.wg.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.owner.ctx, cancel)
	defer func() { stop(); cancel() }()
	if err := ctx.Err(); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	var local actool.ToolContext
	if tc != nil {
		local = *tc
	}
	local.Tasks = t.owner.manager
	return t.CoreTool.Call(ctx, input, &local)
}

type dispatchHooks struct {
	inner       harness.HookRunner
	tools       map[string]actool.CoreTool
	deferred    map[string]bool
	unlock      *actool.UnlockSet
	permissions permission.Context
	ask         permission.CanUseTool
	viewMu      sync.RWMutex
	visible     map[string]bool
	// Wrap-up gate (see newSession): settlePrompts are the exact texts the SDK may
	// inject when a run exhausts its budget; once one of them shows up as the last
	// message, settleGuard tools are refused for the rest of the run. The tool array
	// itself is never touched, so the request prefix (and cache) stays stable.
	settlePrompts []string
	settleGuard   map[string]bool
	settleNames   []string
	settling      atomic.Bool
}

// The official SDK hides Settlement.DisabledTools from request schemas but
// leaves them executable in its registry. Enforce the host's per-request
// surface in the hook as well. Observe only the loop's model calls (not the
// compactor's summarizer); use trusted schemas, never model/user message text.
func (h *dispatchHooks) observeModelCalls(opts *agentcore.Options) {
	stream, complete := opts.Deps.CallModel, opts.Deps.CallModelSync
	if opts.Provider != nil {
		if stream == nil {
			stream = opts.Provider.Stream
		}
		if complete == nil {
			complete = opts.Provider.Complete
		}
	}
	if stream != nil {
		opts.Deps.CallModel = func(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
			return func(yield func(llm.StreamEvent, error) bool) {
				h.observeRequest(req)
				for event, err := range stream(ctx, req) {
					if !yield(event, err) {
						return
					}
				}
			}
		}
	}
	if complete != nil {
		opts.Deps.CallModelSync = func(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
			h.observeRequest(req)
			return complete(ctx, req)
		}
	}
}

// observeRequest records what the loop actually sent: the tool surface (used to
// reject calls that are not part of the current request) and whether the SDK has
// just injected its wrap-up prompt.
func (h *dispatchHooks) observeRequest(req llm.CompletionRequest) {
	h.observeTools(req.Tools)
	h.observeSettlement(req.Messages)
}

// watchSettlement arms the wrap-up gate. guard is code-owned policy (the caller
// passes it explicitly), so an admin editing the wrap-up prompt cannot disable it.
// No-op when the run has no settlement prompt or nothing to guard.
func (h *dispatchHooks) watchSettlement(s *harness.Settlement, guard ...string) {
	if s == nil || len(guard) == 0 {
		return
	}
	prompts := settlementPrompts(s)
	if len(prompts) == 0 {
		return
	}
	h.settlePrompts = prompts
	h.settleGuard = make(map[string]bool, len(guard))
	h.settleNames = make([]string, 0, len(guard))
	for _, name := range guard {
		if h.settleGuard[name] {
			continue
		}
		h.settleGuard[name] = true
		h.settleNames = append(h.settleNames, name)
	}
}

// observeSettlement latches "the run is wrapping up" the first time the loop sends
// the wrap-up prompt as the trailing user turn. The SDK appends that text verbatim
// (harness/query.go: beginSettlement → llm.UserText(prompt)), so matching it is
// exact and needs no marker inside the prompt. A prefix match covers SDKs that pad
// the injected text.
func (h *dispatchHooks) observeSettlement(msgs []llm.Message) {
	if h.settling.Load() || len(h.settlePrompts) == 0 || len(msgs) == 0 {
		return
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser {
		return
	}
	text := strings.TrimSpace(messageText(last))
	if text == "" {
		return
	}
	for _, p := range h.settlePrompts {
		if text == p || (len(p) >= 32 && strings.HasPrefix(text, headRunes(p, 32))) {
			h.settling.Store(true)
			log.Printf("[settlement] 收尾相位已识别：拒绝执行 %v（工具清单保持不变，保住前缀缓存）", h.settleNames)
			return
		}
	}
}

// messageText concatenates the text blocks of one message.
func messageText(m llm.Message) string {
	var b strings.Builder
	for _, c := range m.Content {
		if c.Type == llm.BlockText {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// headRunes returns the first n runes of s (byte-safe for multi-byte prompts).
func headRunes(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// settlementDenied explains a refused call in terms the model can act on: write
// findings back with the report/asset/fact tools, then summarise and stop.
const settlementDenied = "已进入收尾相位（本轮预算已用完）：不能再执行 %s。请立即用落库工具把已有结论写回" +
	"（record_fact / insert_assets / report_finding / RecordMemory），然后单独用一句话总结；" +
	"重复调用 %s 会被同样拒绝。"

func (h *dispatchHooks) observeTools(schemas []llm.ToolSchema) {
	visible := make(map[string]bool, len(schemas))
	for _, schema := range schemas {
		visible[schema.Name] = true
	}
	h.viewMu.Lock()
	h.visible = visible
	h.viewMu.Unlock()
}

func newDispatchHooks(opts agentcore.Options) *dispatchHooks {
	h := &dispatchHooks{inner: opts.Hooks, tools: map[string]actool.CoreTool{}, deferred: map[string]bool{}, unlock: opts.UnlockSet,
		permissions: permission.Context{Mode: opts.PermissionMode, WorkingDir: opts.WorkingDir, Allowed: opts.AllowedTools, Disallowed: opts.DisallowedTools}, ask: opts.CanUseTool}
	for _, t := range opts.Tools {
		h.tools[t.Name()] = t
	}
	for _, name := range opts.DeferredTools {
		h.deferred[name] = true
	}
	return h
}

type deferredInvocation struct {
	Name   string          `json:"tool_name"`
	Params json.RawMessage `json:"params"`
}

func (h *dispatchHooks) target(name string, input []byte) (string, json.RawMessage, error) {
	if name == actool.ExecuteExtraToolName {
		var call deferredInvocation
		if err := json.Unmarshal(input, &call); err != nil {
			return "", nil, err
		}
		name, input = call.Name, call.Params
		if name == actool.ExecuteExtraToolName || name == actool.SearchExtraToolsName || !h.deferred[name] || h.tools[name] == nil {
			return "", nil, fmt.Errorf("tool is not bound as a deferred capability")
		}
		if len(input) == 0 {
			input = []byte(`{}`)
		}
	}
	// Direct calls that guess a deferred name must obey the same skill gate.
	if h.deferred[name] && (h.unlock == nil || !h.unlock.Has(name)) {
		return "", nil, fmt.Errorf("tool %s is not unlocked in this session", name)
	}
	return name, input, nil
}

func (h *dispatchHooks) PreToolUse(ctx context.Context, outer string, input []byte) (bool, string, []byte) {
	name, params, err := h.target(outer, input)
	if err != nil {
		return true, err.Error(), nil
	}
	// Wrap-up gate: the tool stays declared (so the request prefix never changes and
	// the provider cache survives) but cannot be executed once the SDK injected its
	// wrap-up prompt. settleGuard is immutable after watchSettlement, so no lock.
	if h.settling.Load() && h.settleGuard[name] {
		return true, fmt.Sprintf(settlementDenied, name, name), nil
	}
	h.viewMu.RLock()
	hidden := h.visible != nil && !h.visible[outer] && !h.deferred[outer]
	h.viewMu.RUnlock()
	if hidden {
		return true, "tool is not available in the current execution phase", nil
	}
	tool := h.tools[name]
	validate := func(in json.RawMessage) (json.RawMessage, error) {
		if tool == nil {
			return in, nil
		} // SDK-injected tools keep official validation.
		resolved, err := PrepareToolInput(tool, in)
		if err != nil {
			return nil, err
		}
		if raw, ok := tool.(interface{ AcceptsRawInput() bool }); !ok || !raw.AcceptsRawInput() {
			err = actool.ValidateInput(tool.InputSchema(), resolved)
		}
		return resolved, err
	}
	params, err = validate(params)
	if err != nil {
		return true, "invalid tool input: " + err.Error(), nil
	}
	if outer == actool.ExecuteExtraToolName {
		decision := permission.Evaluate(ctx, permission.Request{ToolName: name, Input: params, IsReadOnly: tool.IsReadOnly(params),
			ToolDecision: tool.CheckPermissions(ctx, params, h.permissions), Ctx: h.permissions, Ask: h.ask})
		if decision.Behavior != permission.Allow {
			return true, decision.Message, nil
		}
		if len(decision.UpdatedInput) > 0 {
			params, err = validate(decision.UpdatedInput)
			if err != nil {
				return true, "invalid permission input: " + err.Error(), nil
			}
		}
	}
	if h.inner != nil {
		blocked, reason, updated := h.inner.PreToolUse(ctx, name, params)
		if blocked {
			return true, reason, nil
		}
		if len(updated) > 0 {
			params, err = validate(updated)
			if err != nil {
				return true, "invalid updated tool input: " + err.Error(), nil
			}
		}
		// Approval can wait for human input. Recheck even unchanged arguments:
		// the user may have revoked the target while the action was waiting.
		// This is authorization only, not a second action approval or tool count.
		if policy, ok := h.inner.(interface {
			RecheckToolInput(context.Context, string, []byte) string
		}); ok {
			if reason := policy.RecheckToolInput(ctx, name, params); reason != "" {
				return true, reason, nil
			}
		}
	}
	if outer == actool.ExecuteExtraToolName {
		envelope, _ := json.Marshal(deferredInvocation{Name: name, Params: params})
		return false, "", envelope
	}
	return false, "", params
}

func (h *dispatchHooks) PostToolUse(ctx context.Context, name string, input, result []byte, isErr bool) {
	if isErr {
		// Host-level error distillation runs before the inner hook so the
		// lesson is recorded even if the inner hook returns early. The
		// recorder is a no-op when ConfigureHostLessons has not been called.
		if target, params, err := h.target(name, input); err == nil {
			RecordHostLesson(target, params, result)
		} else {
			RecordHostLesson(name, input, result)
		}
	}
	if h.inner == nil {
		return
	}
	target, params, err := h.target(name, input)
	if err == nil {
		h.inner.PostToolUse(ctx, target, params, result, isErr)
	}
}

func (h *dispatchHooks) Stop(ctx context.Context, messages []llm.Message) (bool, []string, string) {
	if h.inner != nil {
		return h.inner.Stop(ctx, messages)
	}
	return false, nil, ""
}
