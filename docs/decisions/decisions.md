# zhimareview 决策记录

2026-11-15 起，经 grilling 钉死后实施。

## 项目定位

唯一动机：**多窗口同时看多个根目录**——MyGo 的 `NewWindow` 是一等公民
能力。界面形态 native UI（无 webview，启动即开窗）。

## 已钉死的关键决策

- **无主窗口**：每个窗口自带选择态（最近目录 + 系统目录对话框）；再次启动 =
  已有实例开新窗口（`RequestSingleInstanceLock` + `OnSecondInstance`）；
  最后一个窗口关闭即退出（框架默认）。
- **diff/代码查看/Markdown 接官方组件**（2026-11-15 二次决策，替代最初的「diff 自研」）：
  `ui/editor` 子包（v0.9.0 已含）的 `DiffView`/`View`/`ParsePatch` 与 `ui/markdown` 的
  `View` 覆盖了自研 LCS diff、三态语法高亮与 Markdown 解析的全部能力且更强
  （词级高亮、折叠上下文、内置查找、per-language 词法、GFM+本地图片）。
  自研三件与 `internal/diff` 已删除；保留的定制点是还原按钮：`RenderHunkActions`
  自绘「还原此块」按 `DiffHunk` 行号落盘（组件自带 Revert/Keep 只改内存缓冲，
  会与磁盘脱节）。
- **侧栏树接原生选区**（2026-11-15 三次决策，替代「自绘选中高亮」过渡方案）：
  改动树迁 `ui.Outline` + `Prefix`（原生选中、↑↓ 选行、←→ 收展，Prefix 保持
  紧凑缩进+箭头样式）；目录树保持拍平 List 架构（懒加载不动），换 `ListState`
  原生选区（Selected/Key/Label + `Rows(ListRow)` 行样式），目录行展开箭头是
  行内独立小按钮（吃掉该点击=不改变选中，Finder 披露三角语义）。行的可点动作
  一律走列表 Changed 回调，行内不放整行按钮。有状态控件构建前统一加 `c.Key`
  （Tabs/Segmented/Combobox/TextInput/Split/两棵树）。
- **目录选择**：系统对话框（`mygo.Dialog.Open{Directory:true}`）。
- **单文件持久化**：最近目录用 JSON（`App.Path(PathUserData)`），不引 SQLite。
- **设计令牌层 theme.go**（2026-11-15 四次决策）：色调三层 + 字号阶梯 + 行度量收敛为
  `tokens` 结构——主色透传 MyGo `Accent`（观感不变）；状态色 4 个固定中调 hex
  （M 橙 / D 红 / R·C 天蓝 / A·U 绿，恰为框架暗色主题
  Warning/Danger 同款值）；中性色透传框架语义字段；字号 XS10/SM11/MD12/LG13/XL24；
  行高 24、圆角 6/4、缩进 12（目录树原 14 一并收敛）、字母列宽 14。
  `appTokens(c)` 每帧从 MyGo 主题派生，自动跟随系统深浅色，视图不再直接
  `c.Theme()`；`rankColor` 随颜色归属移入主题层（等级仍由 `statusRank` 定义）。
  主色要换成应用专属色时，view 根部 `c.SetTheme(c.Theme().WithAccent(色))`
  一行全局生效（含原生控件）；深浅色切换无需求手动开关。
- **侧栏两棵树统一为拍平行 + `ui.List`**（2026-11-16 五次决策，替代改动树的
  `ui.TreeBase`）：改动树真机整树不可滚，根因是挂载顺序踩了立即模式契约
  （坑 13），树从未真正进入滚动容器。统一基座选 List 拍平：目录树的虚拟化是
  硬需求（真实文件系统任意大），TreeBase 非虚拟化不可回退采用；且「层级/列表」
  切换的列表布局已证明改动数据走 List 可行。两树共用 `sideRow`/`sideRowView`/
  `treeList` 三件套（widgets.go），数据源不同拍平各自做（rebuildChangeRows /
  rebuildDirRows）；数据层 `buildChangesTree` 不动。交互契约保住：branch 行
  点完选区归 -1，已选行可重复点击开合（tree_toggle_test 三次点击契约）。
  明示取舍：TreeBase 的树级键盘导航（←→ 开合、Enter 激活）丢失，键盘与目录树
  对齐（↑↓ + 首字母跳行 + 点击）。滚动回归测试 sidebar_scroll_test.go 钉住
  三视图（改动树/列表布局/目录树）。展开语义（2026-11-16 定版）：**改动树
  默认全展开**（changeCollapsed 只记用户显式收起的目录，缺省=展开，扫描新增
  目录自动展开，收起跨扫描按 id 保持）；**目录树默认全收起**（dirExpanded
  缺省=收起，懒加载不变）。曾做过「展开时自动下探目录链」，两轮边界调整后
  废弃——改为**单链目录压缩**（compressDirChain，GitHub/VSCode 惯例）：无文件
  的单孩子目录链合并成一行 a/b/c（id/聚合取链最深层），仓库节点与文件不参与
  合并；教训：拿不准交互语义时先问「缺省态该是什么」，改缺省比加自动化规则
  简单。提交列表行布局（2026-11-16，同日修订）：message+分支徽标合并一列
  Grow 占满剩余宽度，超出省略号；hover 出完整 message 与全部分支（node.Tooltip
  原生提示层）；作者/日期列固定宽、钉在行尾。曾试过「信息列封顶行宽 50%、
  作者/日期紧跟不钉行尾」，拖宽分隔条后省出的空间全部堆成行尾空白且随宽度
  增长——信息列一旦封顶，剩余空间必然无处可去；空隙改落在短 message 与
  行尾列之间（标准 git 列表布局），history_scroll_test 钉住「拖宽行尾空白
  不增长 + 表头行同列对齐」。
- **异步工作统一为管线 `loadAsync`（2026-11-17 六次决策）**：全部「后台算 →
  主线程收」收进 `loadAsync(ws, load, apply)`（pipeline.go）——load 在后台
  goroutine 跑（不得触碰 workspace 状态），结果交 apply 在主线程落地；
  headless 无队列（`post` 为 nil）整条同步，坑 3 的规则从 10 处注释纪律
  变成 1 处实现。缝是 `workspace.post func(func())`（openWindow 绑
  `win.Update`；测试用互斥队列手动排干做第二适配器，pipeline_test 守
  「应用只经队列、恰好一次、load 先于投递、值原样传递」）。在途作废仍由
  各 apply 开头的状态守卫判定——模块只管编组不管同一性，守卫读的是不
  发起新任务也会变的状态（回选择态、选中漂移），key 化表达不了。
  形态两轮定案：先试续延 `work func() applyFn`（work 返回应用闭包），
  否决——两阶段的分界被转嫁给每个调用点手工组装（return 再包一层闭包），
  给返回段起名 applyFn 只是化妆；改回泛型双闭包，分界由模块持有，调用点
  只声明「怎么算、怎么应用」，多返回值的加载用小结果结构体打包
  （scanResult/logResult/detailResult/fileResult，约 16 行一次性代价换
  全部调用点平铺无嵌套）。12 处调用点一次迁完（rescan/openDiffAs/
  runWrite/loadCommits/loadMoreCommits/loadCommitDetail/loadDir/openFile/
  watch 重采/browse/loadRecent）；watch 重采的 headless 分支从「丢弃」
  统一为同步落地（真实窗口行为不变），merge 路径补回归
  TestHandleFsEventsCollectMerge；loadMoreCommits 的分页偏移改为发起时
  捕获（原 goroutine 内读 allCommits 是潜在竞态）。三个例外不迁：watch 的
  事件泵与兜底循环（长驻循环非管线）、chooseDir 的最近目录持久化
  （headless 故意跳过副作用，迁了测试会写真用户数据）、setTitle/
  openNewWindow（窗口 API 调用非状态应用）。
- **workspace 按视图分区拆解（2026-11-17 七次决策）**：61 字段的共享状态球
  拆为根层 + 四个视图分区（值字段，永远在场）：根层留窗口/会话（win/post/
  mode/root/recent）、扫描模型（repos/changes/activeRepo——两条生产者写入，
  全工作区共享）、横切（errText/mainTab/sidebarTab/sidebarSize/mdPreview/
  mdOpts/watch 两件）；`ws.tree`（改动树+列表布局）/`ws.diffv`（diff）/
  `ws.hist`（提交历史+仓库下拉——下拉唯一消费者是历史页，从 rebuildDerived
  移入）/`ws.dirs`（目录树+文件查看）。分区类型就近声明在各自视图文件，
  注释写明唯一写点；管线/交互方法仍归 workspace（带父指针的分区方法被否，
  但**纯输入的派生**上分区方法：tree.rebuild / hist.rebuildOptions /
  dirs.rebuildRows，rebuildDerived 只做编排）——数据归分区、编排归根层。
  repoAt/repoLabel 自由函数化（派生纯输入需要模型经参数传入）。
  `newWorkspace()` 收编全部构造：选区初值 -1、缺省分隔尺寸集中一处，
  openWindow 与 37 处测试直构不再各自背诵（坑 11/15 的构造面漏洞关闭）。
  硬约束：纯结构零行为变化（守卫/渲染/管线语义一律不动），四件套验收；
  `ws.diff`→`ws.diffv.open` 等改名经 gofmt -r 批量完成，编译器兜底。
- **侧栏选区收进 selList + treeList（2026-11-17 八次决策）**：坑 11 的
  「sel=-1 起步」与「branch 行点完归 -1」从调用方契约沉进三件套——
  `selList{st ui.ListState; sel int}` 配 `newSelList()` 构造（-1 起步
  有了唯一构造点，三个分区的选区对捆绑旅行，不再各自持 list/sel 两字段）；
  treeList 收 `*selList`、返回 `(row, ok)`：有效点击给出行、branch 行的
  选区归 -1 在模块内完成，三个调用点从「changed+边界判断+branch/leaf
  分支」的同构展开坍缩为按 row.branch 分发（原两处手工归 -1 删除）。
  顺带消除 FP4 点名的脆弱不变量：flat 布局分发改经 `tree.index[row.id]`
  取改动（flat 行 id 与树文件行同构 cf:repo|path|section），不再依赖
  `flat[flatSel]` 的下标对齐，补回归 TestFlatListClickOpensDiff 钉住。
  历史页列表不并入：自成一套的 ui.List（提交行渲染非 sideRow），其 sel
  初值仍由 newWorkspace 覆盖。tree_toggle_test 三次点击契约原样全绿。
- **文件改动索引与等级常量单点化（2026-11-17 九次决策）**：`changeByAbs`
  模型派生索引（abs = RepoPath/Path → *git.Change）随 rebuildDerived 重建
  （两条生产者 rescan/watch merge 都经它，单点成立），changeForAbs 变一行
  查表——fileview 每帧 O(len(changes)) 线性扫（只为决定 checkbox 显隐）
  与 openFile/retargetOpened 的扫描全部消失。同文件多分区的取舍定死在索引
  构建时（buildChangeByAbs）：**工作区侧优先**（unstaged > staged > 其他）
  ——这是「查看 diff」入口的语义，不是状态等级，明确不并入 statusRank
  （并了会改变打开哪个分区的 diff）。状态等级改共享常量（rankMod/Del/
  Rename/Copy/Add/Untracked/Other/None 单点定义于 sidebar.go），statusRank
  与 rankColor 同引常量，theme.go 的数值镜像（case 4/5/3/2）删除。
  走查报告曾建议「树侧/目录侧两套目录聚合合一」，精读后撤回：树侧是
  trie 构建内的结构传播（rel 路径），目录侧是 abs 前缀 map（dirs.rebuildRows
  分区私有派生），结构不同、各有测试钉住，强行合一需路径键适配层，
  成本高于漂移风险。索引指针指向 changes 元素、随更新整体重建，不跨
  更新存活。TestBuildChangeByAbs 钉住多分区取舍。
- **watch 调度收进 watchSched（2026-11-17 十次决策）**：事件合批与「.」
  全量 1s 限流两条时间规则从驱动 goroutine 内联收进 `watchSched`
  （push 入队 / flush(now) 取批，时间由驱动注入）——真实循环传
  time.Now()，测试手动拨钟，限流窗口与合批顺序无需实时等待即可断言
  （首次覆盖 FP7 点名的零测试调度面）。语义原样：限流丢整批、
  lastFullFlush 只随放行的「.」批更新（普通批放行不续延窗口）、headless
  仍丢弃（watch 机器例外，六次决策边界）。限流状态随规则搬进 sched，
  workspace 根层删 lastFullFlush 字段。顺带补两个裸奔分支的回归：
  文件重载（hint 命中打开中的文件 → 内容更新）与目录刷新（目录级 hint
  命中已加载目录 → 缓存重读、新文件入行）；测试按真实事件形状构造
  （HintsOnly 事件是目录级提示）。**相邻问题同日修复（用户点名）**：
  handleFsEvents 目录分支的第二析取 `hitHint(root, target, root+"/"+p)`
  对绝对路径 target 恒 false（hitHint 无绝对 hint 特判，full 拼成重复
  路径）——死代码背后是真缺陷：hint 与目标的匹配只认「hint 覆盖目标」
  单方向，深层 hint（如文件级 base.txt）不命中其所在仓库，重采不触发、
  改动侧栏滞留到下一个 "." 事件或手动刷新（红测试实抓：文件级 hint 后
  changes 不清零）。修复从归属源下刀：hitHint 改为**双向子树匹配**
  （hint 覆盖 abs 或 abs 覆盖 hint 任一方向即命中，规则单点化），目录
  分支的手工双调用随之删除（双向已由 hitHint 覆盖）。重采由漏变多收——
  多收无害（采集幂等，"." 全量本就常态触发），漏收才是 UI 滞留。
- **行身份语法单点化（2026-11-17 十一次决策）**：改动树行 id 的三种前缀
  语法（cd:/cd-repo:/cf:）从 buildChangesTree 的 5 处手工拼接与 flatKey
  的重复实现收进 `dirID/repoID/fileID` 三个构造函数（sidebar.go 顶部，
  语法与「列表布局行与树文件行同 id」的契约写在文档注释）；flatKey 删除，
  测试硬编码字符串（2 处）改引构造函数。目录树行身份是裸 abs、无语法，
  不包恒等函数（无契约不包装）。GLOSSARY 收「行身份」词条。候选 3 已把
  flat 分发改经 tree.index 查表，本决策收的是剩余的生产侧拼接点。
- **diff 打开会话世代与 changeAbs 语法单点（2026-11-17 十二次决策）**：
  「当前打开的 diff」的同一性原先三套机制并存（指针身份 / 结构体值相等
  守卫 / 原地改写 Section），收敛进 diffPane 自身（pane 即会话，不另造
  session 类型——生命周期完全重合，包一层是透传）：`seq` 世代随每次
  openDiffAs 递增、在途加载凭世代落地（最新打开者胜），替代结构体值
  相等——不依赖 git.Change 可比较（加非比较字段即编译炸），Code/RepoRel
  等展示字段不再参与同一性；`flip()` 收编原地改写（同会话内、世代不变）；
  `target()`（= changeAbs 薄壳）统一 watch 命中与 retarget 的文件路径。
  toggleStageFile 完成侧由「值相等」改「世代相等」：换开别的 diff 后旧
  暂存操作不再污染新会话（旧值相等在重开同文件时会误翻转）。语义修正的
  代价：TestStageToggle 原第二阶段构造了生产不可达状态（open 与传入值
  分区不一致，靠值相等静默跳过），按坑 16 修到可达状态（open 随 flip 为
  staged 后再取消暂存）。`changeAbs` 自由函数收编 RepoPath+"/"+Path 的
  6 处拼接（含 watch 的无空格变体——同一语法两种拼写）。历史页 9 字段
  列表态不并入（tab+repo+Hash 三重守卫语义不同构、有测试钉住）。
  新增 TestApplyDiffStateSession 钉世代丢弃/落地与 flip 不动世代。

- **远程操作与提交列表重载（2026-11-17 十三次决策）**：历史页对当前仓库
  提供 fetch（`--all --prune`）与 pull（默认 `--ff-only`，勾选 rebase 后
  `--rebase`）；顶栏「全部 fetch」复用 `mapLimit` 并发拉取工作区全部仓库，
  失败按仓库聚合（最多列 3 个）。fetch 只写远程引用走 `Run`；pull 会动
  index/worktree 走 `runWriteGit`（index.lock 退避）。单仓库 fetch/pull 与
  全部 fetch 在界面状态上互斥，避免同一仓库的并发写。`Run` 统一带
  `GIT_TERMINAL_PROMPT=0`：GUI 无终端，凭据提示会挂到超时（credential
  helper 不受影响）。pull 完成后无论成败
  都重扫工作区（冲突/变基中断也反映真实现场），错误在扫描应用之后才落——
  `applyScanState` 会清空 errText，先落会被冲掉。历史列表增加加载世代
  `gen`：loadCommits 递增、追加页捕获，在途结果凭世代落地，fetch/pull 的
  重载不会被旧分页结果追加污染；同代结果即使切走 tab 也解除 loading/
  fetching（原有「切走后再回来卡加载中」一并关闭）。顶栏刷新让历史失效：
  在历史页立即重读，不在则等进入页面再加载。push、分支切换、stash 等
  写操作未纳入本次。

- **提交搜索、复制与分支跟踪状态（2026-11-17 十四次决策）**：历史页加
  `ui.SearchField`：message（标题）/ 作者 / 完整或短 hash 子串、忽略大小写，
  与 hideMerges 叠加过滤。首次输入搜索词时一次性 `git log --all` 加载当前
  仓库全部提交（`allLoaded/allTried` 守卫：成功后本地过滤，失败不回退到
  每帧重试；正常浏览仍分页），这是搜索必须完整历史的明确取舍。复制：
  提交详情头部「复制 hash」（完整哈希），diff 与文件查看头部「复制路径」
  （绝对路径），走 `Context.WriteClipboard`。分支跟踪状态由
  `ReadBranchStatus` 用一次 `for-each-ref`（`%(HEAD)` 标当前分支、
  `%(upstream:track)` 给 ahead/behind/gone）取回，随提交列表加载刷新，
  在历史页显示 `main ↑1 ↓2` / 上游已删除；detached HEAD 不显示。
  测试用 `Tester.Clipboard` 钉住复制结果。

- **历史页控制区布局（2026-11-17 十五次决策）**：控制区重排为「仓库选择
  + 隐藏 merge」一行、「分支徽标 + ahead/behind」独立一行、「fetch/pull/
  rebase + 搜索」工具行——分支信息从拥挤的首行移出，长分支名不再和仓库
  下拉、筛选框互抢宽度。分支行固定 18px 占位，状态异步到达时工具栏与列表
  不下跳；分支名由徽标自身截断（tooltip 显示完整分支/上游），ahead/behind
  文本 Shrink(0) 不参与收缩——默认 420px 左栏下计数始终完整可见。搜索框
  在工具行 Grow 占满剩余宽度，匹配数/搜索中状态在行尾；控制区总高从三行
  96px 收紧为 30+18+32=80px。（十七次决策移除独立分支行；十八次决策把
  分支并入选中项第二行；十九次决策改回单行展示串，控制区现为 62px。）

- **仓库下拉自绘富选项（2026-11-17 十六次决策）**：仓库选择器从原生
  Combobox 换成 `ComboboxBase` 自绘：每个选项显示仓库名 + 当前分支 +
  ahead/behind（`main ↑1 ↓2`）与「当前」标记。打开下拉时并发读取所有仓库
  分支状态（`ReadBranchStatuses`，`mapLimit` 8），按需触发、已有条目不重复
  读；fetch/pull/刷新经 `invalidateCommits` 清空状态并前移世代，在途结果
  自动作废。交互保留：键入过滤（打开即清空输入，等价原生 Combobox 的
  select-all；关闭时回填选中值）、↑↓/Enter 选择、Esc/失焦关闭；输入框本体
  做成字段外观，弹层锚点与字段同宽。单仓库状态读取失败按零值处理，选项只
  显示仓库名。

- **移除选择器下方的当前分支行（2026-11-17 十七次决策）**：仓库下拉的
  每个选项已显示分支/ahead-behind 与「当前」标记，选择器下方的独立分支行
  重复；删除后控制区只剩仓库选择（30px）+ 工具行（32px）= 62px。
  `statusByRepo` 仍随提交列表加载当前仓库状态，打开下拉即可立即显示，
  其余仓库在打开下拉时补齐。十五次决策的分支行布局随之失效。（十八次
  决策把分支行并入选中项第二行，十九次决策改为单行展示串。）

- **选中项内两行显示分支（2026-11-17 十八次决策）**：独立分支行移除后，
  分支/upstream 只在下拉展开时可见；把当前仓库的分支/ahead-behind 放进
  仓库选择器的选中项第二行（输入框 28px + 分支行 14px，字段约 44px），
  关着下拉也能直接看到。选中项仍可键入过滤（展开时清空输入，分支行保留）；
  下拉的富选项不变；「隐藏 merge」勾选框与第一行居中对齐。控制区总高
  44+32=76px。（十九次决策改回单行。）

- **选中项单行展示与列宽调整（2026-11-17 十九次决策）**：18 次的两行
  选中项把字段撑到 44px；改为单行：输入框只放仓库名（正常色），点后面的
  分支/upstream 是独立灰色文本（`· 分支 ↑1 ↓2`，FontXS muted，分支名
  省略号截断，计数不收缩），三者与箭头自左向右并列——文本各自在区域内
  裁剪/滚动，不会画到箭头下面。`repoText` 仍是输入框的过滤文本，关闭时
  同步为仓库名，展开时清空；字段回到 30px，控制区 62px。弹层锚点是变窄的
  输入框，面板最小宽度按左栏兜底（`max(min(split-16, 420), 240)`），富选项
  不被窄输入框拖累；下拉项的两行富选项不变。同时提交列表作者列 64→96、
  日期列 60→80，日期行补 SingleLine——窄列下作者/日期不再换行，长内容
  省略号截断。

- **提交详情的跨文件查找（2026-11-19，暂缓）**：曾实现「Cmd+F 打开详情顶部
  查找条、跨本提交全部文件搜索」，查找条用框架的应用级 `ui.FindBar`。因它与
  编辑器自带查找条（Aa/ab/.*、计数、↑↓✕）外观不一致，统一需要改框架公开组件，
  先整体撤回：历史页恢复「Ctrl+F 由各文件 diff 自己的查找条处理」，删除应用的
  跨文件查找状态与 `DiffState.Highlight` 的调用。框架侧的跨 diff 高亮 API 是否
  保留待定；重新做时与 `ui.FindBar` 的统一一起安排。

## 踩过的坑（后来者必读）

1. **帧内同步调用窗口 API 死锁**：MyGo 窗口调用 hop 主线程队列，view 帧与
   `win.Update` 回调本身就在该队列上执行——帧内同步 `SetTitle` = 排队等自己。
   一律走异步（新 goroutine）+ headless 测试的 nil 保护（`setTitle`）。
2. **git index.lock 互踩**：watcher 触发的 `git status`（opportunistic 更新 index）
   与用户暂存并发会撞锁。写操作进程内互斥 + 撞锁退避重试（`internal/git` `runWriteGit`）。
3. **headless 测试竞态**：`ui.NewTester` 没有窗口队列，写操作的 goroutine 异步收尾
   会与测试轮询竞态。`runWrite` 双路径：真实窗口异步+主线程收尾，headless 同步。
   **修订（-race 实抓）**：只把「应用结果」做同步不够——后台 goroutine 本身只要
   在 `win == nil` 分支里改共享状态，就与渲染帧竞态（rescan/openDiff/loadCommits/
   loadCommitDetail/loadDir/openFile 六处实抓）。规则：headless 下整条管线
   （计算+应用）同步执行，后台 goroutine 只存在于真实窗口路径。
   **修订（2026-11-17）**：双路径规则已收进 pipeline.go 的 `loadAsync`，
   各管线不再手写 `win == nil` 分支，此坑的纪律由模块单点承载。
   验证清单必须含 `go test -race ./...` 与框架专用 `go tool mygo vet .`
   （查存储的构建值与 goroutine 捕获，来自 mygo-maintenance 技能），
   以及 `CGO_ENABLED=0 go test ./...`。
4. **不要用 PowerShell Set-Content 改源码**：默认 ANSI 编码会毁掉中文注释
   （git checkout 恢复后用 Edit 工具重做）。同理 cmd 的 `echo >>` 写出 GBK
   字节——曾让验证素材「中文渲染为 ◆」误导排查两轮（MyGo 渲染 UTF-8 中文
   与等宽字体回退均正常）。造中文测试素材一律用 Python `encoding='utf-8'`。
5. **WatchFiles 非 HintsOnly 模式在 Windows 有 8192 条目快照上限**（macOS 65536），
   大根目录直接 `ErrWatchLimit` 停摆。必须
   `HintsOnly: true`：Windows/macOS 单树句柄监听，无 per-entry 快照；代价是
   事件粗化为目录级 hint（"." 表示全量）。本应用刷新是「提示后重读」模型，
   粗粒度无损；命中判断用 `hitHint` 前缀匹配 + "." 特判。
   **修订（2026-11-21）**：「无损」的前提是重读后内容相同不动已打开的会话。
   同目录任意文件保存都会以目录 hint 命中正在看的文件/diff，若走完整重开
   （换 fileView / 清 sides 走 loading），内容没变也会闪加载、滚回顶部、
   Markdown 预览被踢回源码——重载必须走内容比对（`applyFile` 的 same 守卫、
   `applyDiffState` 的 `sidesContentEqual`），相同直接丢弃。
6. **Ignore 必须按路径任意段匹配**：嵌套仓库的 `.git`（zhimareview/.git）
   不在首段，漏排会让 git status 写 index 触发新 hint，形成
   「采集 → 写 index → hint → 再采集」的无限回环（界面持续闪烁 + git 进程风暴）。
7. **调试日志别写进被监听的目录**：crash.log 放 build/ 下时，打日志 → hint →
   打日志，自指回环。放 %TEMP%。
8. **列表行用 ButtonBase，不用 Button**：Button 是「ButtonBase + 主题外观」，
   带底色/边框/内边距——列表行会渲染成一张张叠起来的卡片，内边距还会放大
   图标与文本的间距（side bar 曾整体变成「叠纸牌」）。可点击行一律
   `ui.ButtonBase` + 自己排内容（`tapRow` 已收口）。
9. **统一补丁的内容/元数据边界由 @@ 头决定**：@@ 给出两侧行数（上下文行同时
   消耗两侧），两侧耗尽前只有 +/-/空行是内容，耗尽后到下一个 @@ 之间全是
   文件段元数据（diff --git、index、new file mode、rename、Binary files）。
   凭前缀猜测会把第二个文件起的 `new file mode` 当上下文渲染进历史 diff。
   文件边界产出 `diff.FileHeader` 行，`Hunks` 不跨文件分组。
10. **列表行高固定，代码行必须单行**：MyGo 的 List 行高固定，长行换行会溢出
   行框与相邻行重叠（窄面板的提交历史尤其明显）。`codeLine` 与文件视图
   代码行一律 `SingleLine()` 省略号截断。
11. **列表选区状态初值必须 -1，Go 零值 0 会让首行凭空选中并卡死滚轮**
   （2026-11-15 实机复现）：`dirSel/flatSel` 零值 0 使目录列表首帧就"选中"
   第一行——顶部出现无标签的高亮条，且行集合变化（展开/收起）后 List 的
   选区跟随逻辑反复改写偏移，与滚轮滑行互相踩踏（wheel.go 里 app 侧
   `scrolled` 置位会取消滚轮 glide），表现为**滚轮完全滚不动**；键盘
   ↑↓/ScrollIntoView 不走该路径所以仍有效。`openWindow` 显式
   `flatSel: -1, dirSel: -1` 后滚轮双向正常。判读技巧：无标签高亮条 =
   选中行的标签被半行偏移滚出视口，偏移来自选区跟随的重锚定。
   有选中态的 ListState 配套整数一律 -1 起步（与 `commitSel = -1` 同规）。
12. **`v, ok := m[k]` 的 ok 是存在性不是值**（2026-11-15，目录树收起失效根因）：
   `rebuildDirRows` 写成 `_, open := ws.dirExpanded[e.abs]`——目录一旦被点过
   （key 存在，哪怕值已是 false），open 恒为 true，子层永远拍进行集，
   **展开后箭头/行点击都收不起**；状态里 `dirExpanded=false` 与视图自相矛盾
   是判读锚点。读值一律 `open := ws.dirExpanded[...]`（缺省零值即 false）。
   另：List 行点击的动作经 `Changed()` 触发时，瞬时动作（目录开合）点完应把
   选区让回 -1——已选中行的重复点击没有信号，不归位则同一目录第二次点击无效
   （与 Outline 的行点击语义同源）；文件行保留选中高亮不走此逻辑。
13. **立即模式挂载契约：节点创建时即挂当前父容器**（2026-11-16，改动树整树
   不可滚的根因）：`Children(fn)` 只收纳 fn 运行期间**新建**的元素，
   「把已有节点包进容器」不存在。改动树 `TreeBase` 在 `Scroll` 之前创建，
   `tree.Root` 挂到了侧栏内容 Column，后建的 `Scroll` 是空兄弟节点——树以
   完整高度铺在无裁剪容器里，滚轮无可滚目标。顺序写反不报错：表现为内容
   溢出容器、滚动/裁剪**静默失效**。headless 对照实验证实仅调换创建顺序
   即恢复滚动。容器必须先建：先 `Scroll(c).Fill().Children(func(){…})`，
   再在闭包内创建内容组件（统一侧栏树决策见关键决策五）。
14. **「占满剩余空间」用 `Grow(1)`，不是 `Fill()`**（2026-11-16，浮动按钮消失
   + 部分行数滚不动的根因）：`Fill()` 是父容器高度的 **100%**，与兄弟行（顶栏/
   分段控件）的基数相加必超限，而本项 min-content 不许收缩（非滚动、grow=0 的
   列其自动最小高度=内容高），列底越出窗口——`Absolute().Bottom()` 锚到越界
   底部（按钮只剩 2px 露头=「消失」），List 视口随之失真，行数恰落在
   「视口≈内容高」区间时无滚动余量。headless 实测：30 行即复现（按钮
   y=797.69/H=2.31），1 行正常（y=765.69）。`Grow(1)` 才是 flex-grow+basis:0
   语义（框架注释明说 suits a list or editor filling the rest of a window，
   且 grow≠0 跳过 min-content 下限）。判读锚点：Absolute 元素贴窗底却只露
   一条缝 = 某个父列在溢出。
15. **测试直构 `workspace{...}` 绕过了 chooseDir/applyScanState 的初始化**：
   map/选区字段是零值——`changeExpanded` nil map 写入直接 panic（读是安全的，
   写点必须懒初始化，如 `toggleChange`）；`dirLoading` 同理（先 `loadDir` 会
   panic，要先走一次 `applyScanState` 让目录模式状态初始化）。选区配套整数
   一律显式 `-1`（坑 11 同规）。新视图状态字段加进 workspace 时，检查它的
   首个写点是否直构场景可达。
16. **视图回归测试要编码真实症状，不是「挑一组能过的参数」**（2026-11-16，
   滚动修复第一轮漏网的教训）：首轮滚动回归用 121 行 + 300px 窗口——内容
   远超失真视口，怎么失真都能滚，测试通过但真机失败（用户行数恰落「视口≈
   内容高」的无余量区间，且按钮定位只在真实尺寸下显现）。教训：视口/布局类
   回归要用真实窗口尺寸（1180×800）、多组行数（覆盖边界区间），并断言窗口内
   可见性（Absolute 元素 rect 完整、`y+H ≤ 窗口底`），不是只断言「内容能动」。
17. **拍平行的字段要当作渲染契约对待**（2026-11-16，箭头方向恒为收起态的
   根因）：`rebuildChangeRows` 构造 sideRow 时漏设 `open`，树展开正确但箭头
   永远显示 ▸——视图与数据静默脱节。既有测试只断言「行文本在场」，抓不到
   状态字段错误；行字段（open/depth/letter）要进断言（见 sidebar_row_test）。
18. **重读-比对-落地：身份不变的刷新必须先比对再动状态**（2026-11-22，焦点
   刷新把没变化的历史页整个重载的根因）：刷新全是「提示后重读」模型，但
   落地曾只有部分守卫。规则：身份变化（换 root/仓库/打开对象）才 eager
   invalidate；身份不变的例行重读一律后台读、应用前 `slices.Equal` 比对，
   一致就整体丢弃——不置 loading、不清选中/详情、不重建派生。守卫清单：
   `applyFile` same（文件）、`applyDiffState` sidesContentEqual（diff）、
   `reloadDirs` fsEntriesEqual（目录缓存）、`applyReloadedCommits` 首页前缀
   比对（提交历史，hash 含父链故首页代表全史）、`applyScanState`（扫描）、
   `mergeChanges` 逐仓库分段（watch 合并——整表比对恒不等，合并把段移尾）、
   `structureChanged`（仓库集合兜底）。两个边界：① Change/Repo/CommitRow
   只含 git 状态不含文件内容，纯内容修改产生的列表逐值相同，被正确跳过
   （文件/diff 视图才按内容比对，因为展示的就是内容）；② 测试断言刷新
   「跑没跑」别用时间戳比较——Windows 时钟粗粒度，同一刻度内的两次
   time.Now() 挂钟与单调读数都相等，`Equal`/`After` 都分不出（控制台打印
   会因 I/O 跨刻度造成「加了日志就变绿」的假象），走语义副作用（如
   clearErr 清哨兵错误）判定。

## 组件库（widgets.go）

真机验证后沉淀的复用模式，新界面前先看这里：

- `tapRow(c, label, build)`：整行可点击按钮（label 供无障碍），内容走 Children。
- `badge(c, text)`：小号灰底圆角徽标（分区、分支、引用标签）。
- `emptyState(c, title, hint)`：居中空态（主文案 + 可选副文案）。
- `sideRow`/`sideRowView`/`treeList`：侧栏树三件套——拍平行类型、统一行渲染
  （行首 spacer 缩进+文件夹开/合图标+字母列+名称+固定右缘计数，选中画在行上；
  图标是 Path 矢量自绘 `drawFolderIcon`，官方 Tree 箭头同法，改图标观感只动
  这一个函数）、List 装配（选区跟随 id、键入跳行）。任何新的侧栏树形列表
  直接复用，数据侧只写「按展开态拍平」。
- `loadAsync(ws, load, apply)`（pipeline.go）：异步管线——load 在后台跑
  （不碰 workspace 状态），结果交 apply 在主线程落地；headless 整条同步。
  新的后台加载/写操作一律走它，不再手写 goroutine + win.Update；多返回值
  的加载用小结果结构体打包。

## 显式偏差（README 亦列出）

- diff 上下文默认折叠（官方视图行为，点击分隔条展开）。
- heic 图片不支持（MyGo `DecodeBitmap` 支持 PNG/JPEG/GIF/WebP/BMP）。
- Markdown 链接点击行为按官方组件默认（http/https/mailto 打开）。
- pull 默认只允许快进，分叉时需勾选 rebase 或外部处理。
- 提交搜索首次输入时加载完整历史（`git log --all`），超大仓库需要等待传输。
- 搜索结果是发起时的数据快照，之后工作区变化不自动更新结果，重新搜索才是最新（2026-11-22 定为特性：与「重读-比对-落地」的会话状态保留一致，搜索命中也属于用户正打开的会话，被动清空/刷新反而丢上下文）。
