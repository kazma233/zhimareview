# MyGo 文档入口

官方文档：https://github.com/egoist/mygo/blob/main/docs/README.md（docs 在 main 分支，可能领先已发布版本；以 `go doc` 为准）。
Go API：`go doc -all github.com/egoist/mygo`；`mygo doctor` 检查开发环境。

## 常用页面

- 入门/CLI：[getting-started](https://github.com/egoist/mygo/blob/main/docs/getting-started.md) / [cli](https://github.com/egoist/mygo/blob/main/docs/cli.md) / [configuration](https://github.com/egoist/mygo/blob/main/docs/configuration.md)
- 窗口与应用：[windows](https://github.com/egoist/mygo/blob/main/docs/windows.md) / [app](https://github.com/egoist/mygo/blob/main/docs/app.md) / [menus](https://github.com/egoist/mygo/blob/main/docs/menus.md) / [native](https://github.com/egoist/mygo/blob/main/docs/native.md)（对话框/通知/剪贴板）
- Native UI 总览：[ui/README](https://github.com/egoist/mygo/blob/main/docs/ui/README.md)（组件页从它出发）
- 常用 guide：[views](https://github.com/egoist/mygo/blob/main/docs/ui/views.md) / [layout](https://github.com/egoist/mygo/blob/main/docs/ui/layout.md) / [styling](https://github.com/egoist/mygo/blob/main/docs/ui/styling.md) / [input](https://github.com/egoist/mygo/blob/main/docs/ui/input.md) / [testing](https://github.com/egoist/mygo/blob/main/docs/ui/testing.md)（ui.NewTester）

## 组件页（本项目用到或备查）

- [List](https://github.com/egoist/mygo/blob/main/docs/ui/list.md)：虚拟化行列表；`ListState.Selected` 原生选中高亮、`Key`/`Label`/`ScrollTo`/`Reorder`
- [Outline](https://github.com/egoist/mygo/blob/main/docs/ui/outline.md)：虚拟化树，`Prefix` 可自绘行首；[Tree](https://github.com/egoist/mygo/blob/main/docs/ui/tree.md)（非虚拟化）、[Sidebar](https://github.com/egoist/mygo/blob/main/docs/ui/sidebar.md)（分区 source list）
- [Diff viewer](https://github.com/egoist/mygo/blob/main/docs/ui/diff-viewer.md) 与 [Code editor](https://github.com/egoist/mygo/blob/main/docs/ui/code-editor.md)：实现在 `ui/editor` 子包（v0.9.0 已含）
  - `editor.DiffView` + `DiffState`（统一/分栏、折叠上下文、内置查找、`HunkActions`）；`ParsePatch` 直接解析 git 补丁（`SetPatch`/`SetPatchFiles`）
  - `editor.View` + `State`（只读查看用 `ReadOnly: true`，语言由 `LanguageFor(文件名)` 选）
- [Markdown](https://github.com/egoist/mygo/blob/main/docs/ui/markdown.md)：实现在 `ui/markdown` 子包；`markdown.View(src, &Options)`，本地图片用 `markdown.FileImages(dir)`
- [Tooltip](https://github.com/egoist/mygo/blob/main/docs/ui/README.md) → `node.Tooltip(s)`（带主题外观：反色/限宽/阴影，挂任意元素）；复杂内容用 `ui.TooltipBase(c, anchor, fn)` 自定样式。hover 0.6s（主题可调）/键盘焦点即现，按下/Esc/移开消失，一次一个；无障碍上同时写入 Description。提交历史列表行用它展示完整 message 与全部分支（2026-11-16）
- 其余组件清单见 [ui/README](https://github.com/egoist/mygo/blob/main/docs/ui/README.md)：Table、Tabs、Dialog、Toast、Tooltip、Context menu、Find bar、Combobox、Charts 等

## 本项目接入记录

- diff 视图 → `editor.DiffView`（还原按钮用 `RenderHunkActions` 自绘，落盘走 `DiffHunk` 行号；组件自带 Keep 只改内存缓冲，不采用）
- 提交历史 → `editor.ParsePatch` + 逐文件 `SetPatch`（FitContent 堆叠渲染）
- 文件查看 → `editor.View`（ReadOnly + LineNumbersOn）
- Markdown 预览 → `markdown.View` + `FileImages`
- **FitContent 视图没有自己的滚动，查找跳转靠外层容器**（2026-11-19，mygo 修复）：
  历史页逐文件 diff 用 `DiffState.FitContent = true`（编辑器与内容等高，滚动端口是
  应用自己的 `ui.Scroll`）。此时编辑器内部的 `scrollY` 恒为 0（`scrollMax` = 内容高 −
  视口高 = 0），`doReveal` 写内部偏移是空操作——查找命中在视口外时视图不会跟过去。
  mygo 侧修法（`ui/editor` 分支 `diff-find-reveal-fitcontent`）：
  1. 查找命中 / 跳行落点建不可见锚点元素（`Absolute` + `ScrollIntoView`），让
     **外层容器**滚到它，与文本输入框光标同一机制；
  2. 查找条与跳行条在 FitContent 下改用 **sticky**（`Absolute` 覆盖行 +
     `Sticky().Top()` 的条），贴「外层容器视口顶部」而不是「内容顶部」——否则
     跳到命中后条会随内容顶部滚出视野。sticky 语义与 CSS 一致：所在元素顶部
     在视内时贴着它，滚出后贴视口顶；且不能越过自己所属的 diff（历史页多文件
     堆叠时，滚到下一个文件，上一个文件的条随之离开）。
  应用侧无需改动。
  排查要点：`doReveal` / `setScroll` 在 FitContent 下都是空操作（内部
  `scrollMax` = 内容高 − 视口高 = 0），任何「滚到某处」的编辑器功能都得走
  外层容器；新加此类功能时照上面两条接线。
- **立即模式挂载契约（实抓缺陷，2026-11-16）**：节点在**创建时**挂到当前父容器，
  `Children(fn)` 只收纳 fn 运行期间新建的元素——「把已有节点包进容器」不存在。
  容器必须先建（先 `ui.Scroll(c).Fill().Children(func(){ … })`，再在闭包内创建
  内容组件）。顺序写反不报错，表现为内容溢出容器、滚动/裁剪静默失效。
- **「占满剩余高度」必须 `Grow(1)`，不是 `Fill()`**（实抓缺陷，2026-11-16）：
  `Fill()` 是父容器高度的 100%，与兄弟行基数相加必超限且 min-content 不许
  收缩，列底越出窗口——Absolute 锚定元素被推出窗外、List 视口失真到无滚动
  余量。`Grow(1)` = flex-grow + basis:0，才是「填满窗口剩余」的框架契约。
- 侧栏树统一 → 拍平行 + `ui.List`（2026-11-16；`sideRow`/`treeList`/`sideRowView`
  三件套在 widgets.go，改动树/目录树/列表布局共用）。改动树此前的演进：
  Outline → TreeBase（为「行点击 toggle」语义，Outline 已选中行再点击是 no-op
  且应用层收不到任何信号）；TreeBase 版真机整树不可滚，根因即上面的挂载契约
  （`TreeBase` 在 `Scroll` 之前创建，树从未进入滚动容器；headless 对照实验证实
  仅调换创建顺序即恢复）。改 List 拍平后此类根因整体消除（List 自带虚拟化滚动）。
  「已选中行重复点击」用选区归 -1 技巧：branch 行点击后 `sel = -1`，下次点击
  必是新选择，Changed 再触发（目录树同款）。TreeBase 的树级键盘（←→ 开合、
  Enter 激活）随统一丢失，键盘能力对齐目录树（↑↓ + 首字母跳行 + 点击）。
  **坑（Outline，若再启用仍适用）：children 契约对叶子必须返回 nil，空切片会把
  叶子变成空 branch。**
- 目录树 → `ui.List` + `ListState`（`Selected` 原生高亮、`Key`=abs 选中跟随条目、
  `Label` 键入跳行），行渲染与改动树共用 sideRowView；目录行的展开箭头是行内
  独立 ButtonBase（吃掉该点击=不改变选中，Finder 披露三角语义），懒加载架构不变。
- 行内 Button 会吃掉行的点击使 List/Outline 原生选中不触发——行的可点动作
  一律经列表 Changed 回调，行内只放独立小元素（如箭头）并接受其拦截语义。
- 有状态控件构建前加 `c.Key`（Tabs/Segmented/Combobox/TextInput/Split/两个树列表），
  保证跨帧状态身份稳定（mygo-maintenance 技能要求）。
- **滚轮是平滑滑动不是逐格跳**（框架设计，`ui/wheel.go`）：每格滚轮是 150ms
  EaseOut 滑行动画（`wheelAnimation`），连续滚动目标累加——停止位置比最后一格
  的落点多一小段，观感「总是多滚一点」。浏览器同款行为，应用层无开关；
  系统开启「减少动态效果」也不影响滚轮路径（只影响其它动画）。
  排查记录（2026-11-15，headless 实测钉住）：列表滚动映射正确——滚到底
  `gap=0` 最后一行完整贴底；`ListState.Key` 让视口跟随条目，滚动中行集合变化
  （watch 重载）不漂移。侧栏列表的行高 24px 偏紧凑，同样滑行像素相对行数更大，
  「过头」感更明显。
- **滚轮停止点会偏离真实底部（框架缺陷，实机实抓）**：滚轮 clamp 用引擎侧的
  `s.contentH`，行集合变化（watch 重载/展开收起改行数）后若 contentH 尚未随
  新帧更新，滚轮会提前停在「假底」——实测滚轮停后拖滚动条还能再滚 2 行以上。
  滚轮与滚动条到达的「底部」不一致，滚动手感表现为忽多忽少。根治在框架
  （contentH 与布局同步）；应用层缓解：减少行集合变化频率（`fsEntriesEqual`
  去重已做），必要时向 MyGo 提 issue。
- **自绘标题栏 + 浏览器式 tab 条**（2026-11-18，单窗多 tab 改造；同日实抓
  macOS 26 坑后从 HiddenInset 改 **TitleBarHidden**）：
  - 窗口开 `TitleBarStyle: TitleBarHidden`，标题栏区自绘 tab 条：
    `c.TitleBar()` 给出红绿灯占位 `{Height, Left, Right, Bottom}`，行高
    `max(bar.Height, 38)`、行 padding `(0, max(bar.Right,8), 0, max(bar.Left,8))`，
    整行 `DragWindow()`（拖动/双击最大化）。**全屏时 TitleBar 是零值**
    （红绿灯隐藏），兜底高度与最小 padding 必须留。
  - **坑（实机实抓）：macOS 26 上 `TitleBarHiddenInset` 会让 mygo 给窗口挂
    空 NSToolbar**（darwin/window.go `setToolbar:`，为红绿灯内缩布局），
    系统 26 新设计给 toolbar 画玻璃材质，盖在应用自绘标题栏上，出现宽度
    异常的色块截断（实测灰块只到窗口 ~76% 宽）。headless（`Tester.SetTitleBar`
    + `Find` 量盒子）证明应用布局全宽无截断，问题纯在系统材质层；Hidden
    不挂 toolbar，标题栏区完全归应用画。代价：红绿灯从内缩位回到标准贴角位。
  - **PaddingX 会整体覆盖水平 padding**：先 `.Padding(0, bar.Right, 0, bar.Left)`
    再 `.PaddingX(8)` 会把红绿灯留白冲掉——左右留白要合并进一次 Padding 调用
    （`max(bar.Left, 8)`）。
  - tab 组件用 `ui.TabsBase` + 逐个 `tabs.Tab(i)` 自绘（要 × 关闭钮，
    `ui.Tabs` 固定 label 做不了）：组件负责选中写回 `*selected` 与键盘
    （←→/Home/End），应用侧轮询 `tabs.List.Changed()` 收变化信号；tab 内
    × 是行内独立 ButtonBase，吃掉点击不触发 tab 选择（行内小元素拦截语义）。
  - `App.Path` 实现是互斥锁读配置 + mkdir（paths.go），**不 hop 主线程队列**，
    与窗口 API 不同，帧内调用安全；本项目的 goroutine 包裹只为不占主线程。
  - `Tester.Click` 按 label **精确相等**匹配（headless.go `n.text != s`），
    tab 名与顶栏完整路径含相同子串不会混淆；行内按钮用独立 Label
    （「关闭标签页 xxx」）定位。`Tester.SetTitleBar` 可模拟真机占位值，
    配合 `Find` 量元素盒子，是自绘标题栏布局的回归测试手段
    （tabbar_layout_test.go）。
  - **坑（实机 panic，实抓）：渲染中途改元素集合的底层切片会同帧越界**。
    ×/＋ 点击发生在帧构建过程中（`Clicked()` 在该元素构建时消费事件），
    处理函数若直接截短/追加 `a.tabs`，同帧的 `for i := range a.tabs`（旧
    header 长度）与 `TabsBase(n)` 会越界——12 个 tab 关一个即 panic
    `index out of range [11] with length 11`。修复：帧内点击只入队
    （`queueCloseTab/queueNewTab`，索引是帧开始时的快照），tab 条渲染完
    `flushTabOps` 降序统一应用；flush 后本帧剩余构建读新集合，TabsBase
    本帧按旧 n 建的越界 ids 槽位为空、框架按 nil 状态跳过，下一帧钳回。
    同类教训：**immediate mode 下「点击处理函数改集合」一律帧内入队、帧末
    应用**（侧栏树 branch toggle 改派生行没炸是运气：行数组重建不缩 List
    的 n 声明）。

