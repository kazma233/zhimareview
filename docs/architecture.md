# 架构与关键实现

面向维护者的代码地图与实现约束。功能与使用见 README.md。

## 结构

```
main.go              进程入口：调用 app.go 的单实例锁与开窗流程
app.go               application 壳（单窗多 tab、tab 管理/会话恢复/落盘、窗口生命周期）
workspace.go         workspace 状态机（pick/work 两态）与顶栏/侧栏/主区路由、入场视图
scan.go              扫描编排、派生重建、仓库/改动模型查询与按需刷新
picker.go            选择态：最近目录 + 系统目录对话框 + 进入工作区
remote.go            fetch/pull/全部 fetch 的编排、进度模态与错误聚合
tabbar.go            自绘标题栏 tab 条（TabsBase + ×/＋ + 拖拽排序 + 颜色/重命名 + DragWindow）
errdialog.go         错误弹窗：标题 + 可滚动正文 + 复制/关闭
dialog.go            通用模态骨架：标题/副标题、分隔线、正文与底部动作、默认宽度与间距
search.go            侧栏全局搜索（文件名/目录名/内容）
sidebar.go           改动树构建（跨仓库折叠、状态色继承）与列表排序
diffview.go          diff 视图接入（editor.DiffView + 落盘还原）与写操作编排
history.go           提交历史数据与管线（加载、分页、世代、筛选、详情、revert）
historyview.go       提交历史页渲染（列表/表头/行/分支状态/提交详情 diff）
repopicker.go        历史页仓库下拉（分支/ahead-behind 展示与过滤）
dirmode.go           目录树懒加载/拍平/聚合
fileview.go          文件查看与打开/重载/跳行（editor.View 只读高亮/图片/二进制/图片扩展名）
theme.go             设计令牌：色调三层（主色/状态色/中性色）、字号阶梯、行度量
watch.go             WatchFiles 监听循环、按仓库合并刷新、结构兜底
internal/git/        git CLI 封装：仓库发现、porcelain 三区、对象读取、暂存/提交/revert、log 解析、fetch/pull、分支跟踪状态、全局搜索
internal/store/      最近目录与会话 JSON 持久化（会话按 tab 记录根目录/名称/颜色）
```

## 关键实现说明

- **线程模型**：MyGo 的 view 在主线程逐帧构建；窗口 API（如 `SetTitle`）会 hop 到主线程队列——**帧内同步调用窗口 API 必然死锁**（见 `syncTitle` 注释）。后台 goroutine 通过 `post`（= `win.Update`）回主线程；`App.Path` 只加锁读配置不 hop 队列，线程安全。
- **git 写操作**：进程内写互斥 + `index.lock` 撞锁退避重试——watcher 触发的 `git status`（opportunistic 更新 index）与用户暂存并发时会撞锁。
- **测试**：`ui.NewTester` 无窗口渲染视图直接断言（Go 单测级，秒级）；异步工作经 `loadAsync` 管线——headless 下同步执行避免竞态。
- **挂载契约**：内容必须在 `Children` 闭包内创建，闭包外建的元素不会挂载。
- **有状态控件**：Tabs / Segmented / Combobox / TextInput / Split / 两棵树构建前统一加 `c.Key`，否则重建后状态错乱。
- **tab 重排与重命名**：重排在帧末 `flushTabOps` 按 `*workspace` 身份落地（关闭会改下标，索引快照不可复用）；重命名在标签内用 `TextInputBase`，失焦提交延迟 `tabRenameBlurDelay`——按下别的控件时释放还没到，立即收窄 tab 会让点击落空。
- **全部 fetch 进度**：`git.FetchReposProgress` 在后台按仓库回调 start/done，回调经 `ws.post` 回主线程更新 `fetchAllPane`；`applyResults` 再用最终结果校准（防漏/防重）。模态关闭不取消后台 fetch；失败在模态内展示，模态已关时回落错误弹窗。
- **通用模态**：`dialog.go` 的 `showDialog` 统一标题/副标题、分隔线、正文与底部动作；`dialogCard` 统一正文滚动卡片（Surface + 内边距，右侧留 14px 给覆盖式滚动条），错误弹窗与 fetch 进度模态共用。

## 参考文档

- MyGo 指南：<https://mygo.egoist.dev/llms.txt>（每页加 `.md` 是 Markdown 版）
- MyGo 组件文档索引：[docs/context/mygo-ui-docs.md](context/mygo-ui-docs.md)
