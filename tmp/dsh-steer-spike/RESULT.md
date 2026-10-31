# DSH 运行中消息注入 spike 结果

日期：2026-10-24
环境：`dsh 0.2.1-rc.2`，headless profile，隔离 `DSH_HOME`，确定性 mock OpenAI-compatible provider（不依赖真实模型/凭据）。

## 问题与结论

### Q1 插件能否在工具步骤边界拿到当前 Agent？

能。插件在 `session/event` 的 `tool/result` 事件里用 `ctx.agents.get(session.id)` 拿到 live agent：

- `agentFound: true`
- `agentStatus: "running"`

不需要模型配合，也不需要从 prompt 里解析任何信息。

### Q2 `agent.steer` 能否在下一次模型请求前生效？

能，但**不能直接在 `session/event` 监听器里调用**。

直接调用报错：

```
Error: session append cannot reenter while another append is being published
  at Session.publicationEntry (dsh-session/lib/index.js)
  at Session.append
  at ReactLoopInbox.mutate (dsh-agent-loop/lib/index.js)
```

原因：`session/event` 是 post-commit、fire-and-forget 的观察流；回调运行时该次 append 的发布还没结束，`steer()` 会再次 append，触发重入保护。

把 `steer()` 推迟到 `queueMicrotask`（或 `setTimeout(0)`）后即可。5/5 次运行结果一致：

- 第 1 次模型请求：只有原始 prompt，`has_marker=false`
- 第 2 次模型请求：`roles=[developer,user,user,assistant,tool,user]`，最后一条 user 就是 steer 消息，`has_marker=true`
- 模型最终回复 `SPIKE-STEER-RECEIVED`

事件顺序（同一 Turn，step 1 内）：

```
tool/result          seq 17  05:10:10.190
steer 写入 next-step         05:10:10.192   (inbox.nextStep 长度 1)
step/end             seq 19  05:10:10.192
step/start (step 2)  seq 21  05:10:10.205
user/message (steer) seq 22  05:10:10.207
```

即 steer 消息成为 step 2 的首条 user 消息，模型请求已包含它；Turn 没有结束。

### 额外发现：`agent/turn-stopping` 是更安全的收口边界

`agent/turn-stopping` 是 serial + awaited 的边界钩子。在这里直接 `agent.steer()` 不会重入，DSH 会重新读取 inbox 并再跑一个 step：

- 第 2 次请求（模型准备收尾）：`has_marker=false`
- 第 3 次请求：最后一条 user 为 steer 消息，`has_marker=true`
- 最终回复 `SPIKE-STEER-RECEIVED`

这说明“模型已经给出最终文本、但 Owner 消息刚好到”的情况也能兜住，而不是只能等下一 Turn。

## 对正式方案的修正

1. 插件在 `tool/result` 里先 claim AHA inbox，再**异步**（microtask）调用 `agent.steer`；不能在监听器里同步 append。
2. 仅靠 `tool/result` 有一个约 13ms 的 step 边界窗口（本机 mock 下 5/5 命中），正式实现应有 claim/ack/失败回滚，避免“已领取但注入失败”丢消息。
3. `agent/turn-stopping` 作为收口兜底：Turn 即将关闭时领到消息就 steer，DSH 会重开一个 step，而不是把消息留到下一 Turn。
4. 若进程在 claim 后崩溃，inbox 仍需回滚为 pending；这属于 AHA 侧数据面，不在本次 spike 范围。

## 复现

```sh
cd tmp/dsh-steer-spike
./run-spike.sh                          # tool/result + microtask
SPIKE_DEFER=timeout ./run-spike.sh      # setTimeout(0)
SPIKE_TRIGGER=turn-stopping ./run-spike.sh
```

证据文件：`steer-events.jsonl`、`requests.jsonl`、`dsh.stdout.jsonl`。

## 状态

仅 spike，未改产品代码，未提交，未部署。
