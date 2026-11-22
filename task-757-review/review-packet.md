# task-757：0.5.558 前端发布门禁最小修复

修复提交 `88b73829bed748ce5a3796117871aa55be9c1408` 仅修改 `web/tests/build.test.mjs`：渠道导航断言跟随 task-755 改为 channel，增加宠物仍为 bot 的断言；首屏预算从 278528 调至 278784（仅一个 256 字节边界）。生产源码、依赖、构建脚本和工作流零改动。建议按 owner 指定 L0，由宠物机械核验后负责后续版本与发布。本 Task 不自行 complete。

## 审查范围和提交

- Base：`406feb3edb28ea3a79f13442a3491394972810cd`（本 worktree 初始 HEAD，也是在发布失败后的 main）。
- Code head：`88b73829bed748ce5a3796117871aa55be9c1408`。下面内嵌的是这两个提交之间 `git diff --unified=0` 的完整 diff（只去掉未改动的上下文行，无变更省略）。
- 证据文档在 `task-757-review/review-packet.md`，以后续独立 docs 提交交付。最终 delivery head 由最终答复给出；`git diff 406feb3edb28ea3a79f13442a3491394972810cd <delivery-head>` 可查看包含文档的全部交付差异。
- 分支：`aha/task_dbf552d8f9237a6bebab`；主仓 `/srv/build/users/kaikai/kk-workspace/AHA2` 保持 `406feb3edb28ea3a79f13442a3491394972810cd`、clean。
- 未 push/tag/部署，未操作真实浏览器、凭据或生产数据，未下载/安装依赖。

## 原始失败与本地复现

CI 证据来自 AHA 路由 pet 消息 10780（不是本 Agent 重新访问浏览器）：GitHub Release run `45001536797`，Windows job `137143939269`，Build Windows package 第 7 步 node web tests exit 1。CI 报告旧渠道 bot 正则不匹配，以及 JS+CSS level-5 masked-query gzip 278583 > 278528，超 55 字节。Linux/macOS 的四个 job 已通过，但 Release 未生成；生产 0.5.558 健康属于委派上下文，本 Agent 未做线上检查。

本地在修改前先运行生产构建和完整 `web/tests/build.test.mjs`：102 tests / 100 pass / 2 fail / 0 skipped，exit 1。只有以下两项失败，与委派信息一致：

1. `built web contains responsive application`，测试定义原第 1222 行，旧断言第 1599 行：期望 `"channels",\s*"bot",\s*"nav\.channels"`，实际已是 channel。
2. `production web splits route chunks and keeps the first-screen budget`，测试定义原第 3849 行，失败断言第 3992 行：`first-screen JS+CSS gzip is 278580 bytes`，本机超 52 字节。

没有打印超长 actual 应用源码。原始日志只在 scratch，正式材料仅保留上面定位、错误和统计。

## 三个基线、增长来源与预算

工具一致：Linux x64、Node `v24.21.0`、Node zlib `1.3.2.1-motley-8002e91`、主仓现有 esbuild `0.28.2`（与 lockfile 一致）。三份副本均运行各自版本的 `scripts/build-web.mjs`，构建脚本和依赖锁在三个基线间无差异。构建输出均在本 Task scratch。

测量逐字采用现有门禁口径：解析 index 的 JS 入口，递归遍历所有静态 import；每个 JS 独立压缩后相加，并计入 index 的完整首屏 CSS；版本查询串替换成 `?v=3f9a1c7e5b2d`，`gzipSync(..., {level: 5})`。未改算法、未去掉 CSS、未漏静态依赖，也未把懒加载页算入首屏。

| 基线 | 完整 commit | JS gzip | CSS gzip | 合计 | 对旧预算余量 |
| --- | --- | ---: | ---: | ---: | ---: |
| 已发布 556 基线 | `2c5f9f79d85b10e0f7435b161d213a1b364d6c71` | 224441 | 53925 | 278366 | 162 |
| task-755 前 | `6a5099dbe859974cc723929a467cbb444424f0bf` | 224441 | 53925 | 278366 | 162 |
| 失败发布基线 | `406feb3edb28ea3a79f13442a3491394972810cd` | 224510 | 54070 | 278580 | -52 |
| 修复后 | `88b73829bed748ce5a3796117871aa55be9c1408` | 224510 | 54070 | 278580 | 新预算余量 204 |

755 前相对 556，web 和构建脚本无变化，产物和计量相同。`git diff 6a5099d 406feb3 -- web/src` 的全部改动均来自 task-755 的生产提交 `49f048b`：

- `styles.css`：sidebar 使用 dvh/safe-area padding；非 memo 控件不收缩；memo 允许收缩、system-meta 固定；桌面宽度且高度 <800 CSSpx 的媒体规则隐藏 memo 并收紧空隙。全局 CSS gzip +145。
- `icons.ts` 增加 channel 气泡；`main.ts` 的全局/手机共用 nav 和 Task 私聊入口、`task_tools.ts` 的渠道工具、`channels.ts` 的提供方卡片改用 channel。首屏 JS 总量 +69；其中入口 158127→158126（-1），图标共享 chunk 1774→1844（+70），其他三个静态 chunk 不变。懒加载渠道页本体不计入首屏。
- 以上是构建产物的分项增量；gzip 跨文本压缩，不能把每字节精确分配给单行源码。未发现其他首屏生产变更或未知增量。

本地与 Windows 报告相差 3 字节。工作流只固定 Node major `24`，CI 的确切 Node patch、zlib、检出换行与压缩环境未取得，所以不能断言是哪一项造成差异；本地三基线比较消除了工具/OS混用，已确认 +214 来自 755。Windows 报告值亦落在新预算内。

`ceil(278583 / 256) * 256 = 278784`，相对旧上限只增 256。Windows 余量 201，本机 204；保留所有已接受功能。无行为变化的压缩回收需要再改生产源码，此处采用单个测试常量和原因注释，范围更小。

失败基线与修复后生产构建均输出：`app.d005e747b8bc.js`、`styles.9280454792c1.css`、22 route chunks，版本 `bd9b612b57eb`；生产文件名和测量相同。

本机失败基线的完整静态 JS 集合：

| JS 文件 | masked level-5 gzip |
| --- | ---: |
| `app.d005e747b8bc.js` | 158126 |
| `chunk.4SLIRDKV.js` | 2600 |
| `chunk.MT3EX6P2.js` | 1844 |
| `chunk.7Q4WKX5W.js` | 55491 |
| `chunk.O4C2PDLX.js` | 6449 |

## 实际命令、退出码与验证

以下 `S` 为本轮真实 scratch 路径（不是仓库根的历史 review-packet）：

```sh
S='/srv/build/users/kaikai/kk-workspace/.aha2-worktrees/task_dbf552d8f9237a6bebab/.aha2-context/task_dbf552d8f9237a6bebab/scratch/main'
```

副本准备：对 `406feb3`、`6a5099d`、`2c5f9f7` 分别 `git archive <rev>`，用 Python tarfile 解至 `$S/current`、`$S/pre755`、`$S/v556`。除了仓库中已跟踪的历史 `tmp/` 工具目录，其余仓库内容完整保留；排除 tmp 是因为其中有指向宿主 Python 的绝对符号链接，安全解压首次因此 exit 1，修正排除后解压 exit 0。所有 web、scripts、internal、assets 和工作流均在副本中，未只复制 web。`web/node_modules` 仅链接主仓已有依赖，不安装依赖。

| 工作目录 | 实际命令 | exit | 结果 |
| --- | --- | ---: | --- |
| `$S/current`，未改测试 | `node scripts/build-web.mjs` | 0 | 生产构建成功 |
| `$S/current`，未改测试 | `node --test web/tests/build.test.mjs` | 1 | 102 / 100 pass / 2 fail / 0 skipped |
| `$S/pre755` | `node scripts/build-web.mjs` | 0 | 生产构建成功 |
| `$S/v556` | `node scripts/build-web.mjs` | 0 | 生产构建成功 |
| 上述三构建（分别传目录） | `node "$S/measure.mjs" "$S/current"`（另两次参数 pre755、v556） | 各 0 | 表中三个基线，测量脚本完整内嵌在下文 |
| `$S/current`，复制修复测试后 | `node scripts/build-web.mjs` | 0 | 生产构建成功，hash 与修复前相同 |
| `$S/current`，复制修复测试后 | `node --test web/tests/build.test.mjs` | 0 | 102 / 102 pass / 0 fail / 0 skipped |
| `$S/current` | `node --test web/tests/task_channel_ui.test.mjs` | 0 | 5 / 5 pass / 0 fail / 0 skipped |
| `$S/current`，变异均恢复后 | `node --test web/tests/build.test.mjs` | 0 | 102 / 102 pass / 0 fail / 0 skipped |
| 本 worktree | `git diff --check` | 0 | 无空白错误 |

`build-windows-package.sh` 第 235–239 行是 npm ci → build-web → `node --test "$build_dir/web/tests/build.test.mjs"`。本轮复用既有相同锁版本 esbuild，执行该前端门禁的完整文件，102 项全部运行；没有加 `--skip-web-test`，没有只选两项冒充完整门禁。未额外跑整个 `web/tests/*.test.mjs`，它不属于此打包入口的前端单测步骤。

### 断言变异验证

在已通过的 `$S/current` 构建副本中逐项修改、验证补丁已生效、运行、finally 恢复。目标测试在未变异时已在完整门禁中通过。

| 变异 | 实际命令（cwd `$S/current`） | exit | 确切失败 |
| --- | --- | ---: | --- |
| build/app.js 的 channels/channel/nav.channels 改回 bot | `node --test --test-name-pattern=^built\ web\ contains\ responsive\ application$ web/tests/build.test.mjs` | 1 | 1 test / 1 fail；第1599行正则仍要求 channel |
| build/app.js 的 pet-workbench/bot/nav.pet 改成 channel | 同上 | 1 | 1 test / 1 fail；第1600行正则仍要求 pet 的 bot |
| 首屏入口追加一个有效 JS 注释，内容为 SHA256(0)…SHA256(63) 拼接的4096个hex字符 | `node --test --test-name-pattern=^production\ web\ splits\ route\ chunks\ and\ keeps\ the\ first-screen\ budget$ web/tests/build.test.mjs` | 1 | 1 test / 1 fail；`first-screen JS+CSS gzip is 281577 bytes` > 278784 |

命令表的反斜杠仅用于 shell 表达带空格的单个 argv；实际 Python subprocess 传入完整 `--test-name-pattern=...` 参数，不经过 shell 分词。变异错误均为目标断言失败，无编译失败或缺文件干扰。最初变异准备器按单行字符串找 build/app.js 未命中（编译产物为多行），准备器 exit 1；随后改为匹配空白并核对只命中一次才运行变异，不把准备失败算作测试拦截。恢复后完整门禁再次 102/102。

### 可复制的测量脚本

将下段存入自己的 scratch/measure.mjs，运行 `node measure.mjs <完整构建副本目录>`。算法与现有门禁一致；它报告数据，不修改阈值或产物。

```js
import {readFileSync,readdirSync} from 'node:fs';
import {resolve} from 'node:path';
import {gzipSync} from 'node:zlib';
const dist=resolve(process.argv[2],'web/dist');
const index=readFileSync(resolve(dist,'index.html'),'utf8');
const entry=[...index.matchAll(/<(?:script|link)\b[^>]*\b(?:src|href)="([^"]*\.js[^"]*)"/g)][0][1].slice(1).split('?')[0];
const seen=new Map(),queue=[entry];
const gzip=text=>gzipSync(Buffer.from(text.replace(/\?v=[a-f0-9]{12}/g,'?v=3f9a1c7e5b2d')),{level:5}).length;
while(queue.length){
 const name=queue.shift();if(seen.has(name))continue;
 const text=readFileSync(resolve(dist,name),'utf8');seen.set(name,text);
 for(const m of text.matchAll(/(?:from|import)\s*["']\.\/([^"'?]+\.js)(?:\?[^"']*)?["']/g))queue.push(m[1]);
}
const css=index.match(/rel="stylesheet" href="([^"]+)"/)[1].slice(1);
const jsBytes=[...seen.values()].reduce((n,t)=>n+gzip(t),0),cssBytes=gzip(readFileSync(resolve(dist,css),'utf8'));
console.log(JSON.stringify({root:process.argv[2],js:jsBytes,css:cssBytes,total:jsBytes+cssBytes,chunks:[...seen].map(([name,text])=>({name,gzip:gzip(text)}))}));
```

## 完整代码 diff

```diff
diff --git a/web/tests/build.test.mjs b/web/tests/build.test.mjs
index 2f3fb4d..265f005 100644
--- a/web/tests/build.test.mjs
+++ b/web/tests/build.test.mjs
@@ -1599 +1599,2 @@ test("built web contains responsive application", async () => {
-  assert.match(script, /"channels",\s*"bot",\s*"nav\.channels"/);
+  assert.match(script, /"channels",\s*"channel",\s*"nav\.channels"/);
+  assert.match(script, /"pet-workbench",\s*"bot",\s*"nav\.pet"/);
@@ -3979 +3980,5 @@ test("production web splits route chunks and keeps the first-screen budget", asy
-  const FIRST_SCREEN_GZIP_BUDGET = 278528;
+  // task-755 keeps short-screen navigation/account controls reachable and uses
+  // a distinct channel glyph. From 6a5099d to 406feb3, the same masked-query,
+  // level-5 build adds 69 JS + 145 CSS bytes: 224,510 + 54,070 = 278,580
+  // (Windows release run: 278,583). Ceil to the next 256-byte boundary.
+  const FIRST_SCREEN_GZIP_BUDGET = 278784;
```

## 验收边界、缺口和发布建议

- 保持 owner 认可的 <800 CSSpx 短屏隐藏 memo，便签正文/颜色/折叠保存、搜索七入口、账号可达、全局/手机/Task 渠道 channel 与宠物 bot；本次生产代码完全未改。未操作浏览器，也未重跑 task-755 浏览器外观验收，不将静态单测描述成新的视觉验收。
- 本轮只完成 Linux 环境的生产前端构建与 Windows 打包入口所调用的完整前端单测门禁。未运行 Windows Go 编译、Node/browser-host 下载、Inno Setup、网络阶段或完整打包；不能据此声称 Windows CI/完整 Release 已通过。
- L0 建议：宠物核对 base/code head、完整 diff、两处语义断言、预算仅 +256、测试统计和生产零变化，再合并并使用新版本 tag 重跑正式 Release；后续版本号由宠物/owner 决定。禁止移动或覆盖已 push 的 `v0.5.558`。
- 原始日志、构建副本、变异脚本与临时工具仅在本 Task scratch，收尾清理；留下本文件作为 <=64KiB 的自包含审查材料。未触及根目录历史 review-packet，也未创建附件。
