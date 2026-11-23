# 架构与关键实现

面向维护者的代码地图与实现约束。功能与使用见 README.md。

## 结构

```
main.go              单实例锁、WhenReady 开窗、OnSecondInstance 前台化
window.go            application 壳（单窗多 tab、会话恢复/落盘）与 workspace 状态机（pick/work 两态）、扫描编排
tabbar.go            自绘标题栏 tab 条（TabsBase + ×/＋ + 拖拽排序 + 颜色/重命名 + DragWindow）
picker.go            选择态：最近目录 + 系统目录对话框
errdialog.go         错误弹窗：标题 + 可滚动正文 + 复制/关闭
search.go            侧栏全局搜索（文件名/目录名/内容）
workspace.go         顶栏/侧栏（改动树、列表布局、目录模式切换）/主区路由
sidebar.go           改动树构建（跨仓库折叠、状态色继承）与列表排序
diffview.go          diff 视图接入（editor.DiffView + 落盘还原）与写操作编排
history.go           提交历史视图（ParsePatch 逐文件补丁渲染）
dirmode.go           目录树懒加载/拍平/聚合、工作区文件打开
fileview.go          文件查看（editor.View 只读高亮/图片/二进制提示）
codeview.go          图片扩展名判定
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

## 参考文档

- MyGo 指南：<https://mygo.egoist.dev/llms.txt>（每页加 `.md` 是 Markdown 版）
- MyGo 组件文档索引：[docs/context/mygo-ui-docs.md](context/mygo-ui-docs.md)
