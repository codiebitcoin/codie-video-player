package clt01

// 工具调用的上下文元数据：宿主 handler 拿到的 ctx 里带着「agent 头」——
// 这次调用来自哪个会话、哪一轮、哪个 call，以及设备侧冻结的目录 revision。
//
// 业务层用它做审计（谁在什么会话里触发了发信）、去重（同一 turn 的重复调用）、
// 以及把确认播报送回正确的设备会话。

import "context"

type invocationKey struct{}

// WithInvocation 把一次工具调用挂进 ctx（SDK 内部在分发 handler 前调用；
// 宿主测试也可以用它模拟一条带会话信息的调用链）。
func WithInvocation(ctx context.Context, inv Invocation) context.Context {
	return context.WithValue(ctx, invocationKey{}, inv)
}

// InvocationFromContext 从 ctx 取 agent 调用元数据。
// 不在工具调用链上（例如宿主手动触发）时返回 false，调用方按「无会话信息」处理。
func InvocationFromContext(ctx context.Context) (Invocation, bool) {
	inv, ok := ctx.Value(invocationKey{}).(Invocation)
	return inv, ok
}
