package main

import "github.com/egoist/mygo/ui"

// tokens 是应用的设计令牌：色调三层（主色 / 状态色 / 中性色）+ 字号阶梯 + 行度量。
// 视图只引用令牌、不直接取 MyGo 主题字段，调整观感或换肤只动这个文件。
//
// 每帧经 appTokens 从 MyGo 主题派生：中性色透传框架语义色，自动跟随系统深浅色。
// MyGo 的 Theme 本身就是官方 token 源（见 go doc ui.Theme），本结构只做应用层
// 命名与收敛，不复制框架的取值规则（hover/pressed 等仍由框架按 Accent 派生）。
type tokens struct {
	// 主色：强调、选中、主按钮（历史页短哈希也用它）。
	// 现沿用框架默认 Accent，观感不变；要换成应用专属色，在 view 根部加
	// c.SetTheme(c.Theme().WithAccent(主色)) 一行即可全局生效（含原生控件）。
	Accent ui.Color
	// AccentText 是主色底上的文本色（下拉高亮项、选中徽标）。
	AccentText ui.Color

	// 次要色：文件/目录状态色（M 橙 / D 红 / R·C 天蓝 / A·U 绿）。
	// 固定中调：深浅色两套主题下与背景的对比都够读。
	StatusMod ui.Color
	StatusDel ui.Color
	StatusRen ui.Color
	StatusAdd ui.Color

	// 次次要色与中性色（透传 MyGo 语义字段）
	Background   ui.Color
	Surface      ui.Color
	SurfaceHover ui.Color
	Text         ui.Color
	Muted        ui.Color // MyGo 的 TextMuted：次级文本、占位符
	Border       ui.Color
	Danger       ui.Color
	Selection    ui.Color

	// 字号阶梯：XS 徽标与元信息 / SM 次级文本与行内控件 / MD mono 路径与条目主文本 /
	// LG 行标签与正文标题 / XL 入口大标题
	FontXS float32
	FontSM float32
	FontMD float32
	FontLG float32
	FontXL float32

	// 行度量：列表行高、行圆角、徽标圆角、每层缩进步长、状态字母列宽
	RowH        float32
	RowRadius   float32
	BadgeRadius float32
	Indent      float32
	LetterW     float32
}

// appTokens 每帧派生一次令牌；视图开头 tk := appTokens(c)。
func appTokens(c *ui.Context) tokens {
	t := c.Theme()
	return tokens{
		Accent:       t.Accent,
		AccentText:   t.AccentText,
		StatusMod:    ui.Hex("#f59e0b"),
		StatusDel:    ui.Hex("#ef4444"),
		StatusRen:    ui.Hex("#0ea5e9"),
		StatusAdd:    ui.Hex("#10b981"),
		Background:   t.Background,
		Surface:      t.Surface,
		SurfaceHover: t.SurfaceHover,
		Text:         t.Text,
		Muted:        t.TextMuted,
		Border:       t.Border,
		Danger:       t.Danger,
		Selection:    t.Selection,
		FontXS:       10,
		FontSM:       11,
		FontMD:       12,
		FontLG:       13,
		FontXL:       24,
		RowH:         24,
		RowRadius:    6,
		BadgeRadius:  4,
		// 目录树原为 14、改动树为 12，两个值没有语义差异，收敛为一档
		Indent:  12,
		LetterW: 14,
	}
}

// rankColor 把状态等级映射为令牌色；无状态与其他（rankNone/rankOther）
// 用弱化文本色。颜色归主题层：等级常量由 sidebar.go 单点定义（U 与 A
// 同为 2，共享绿色）。
func (tk tokens) rankColor(rank int) ui.Color {
	switch rank {
	case rankMod:
		return tk.StatusMod
	case rankDel:
		return tk.StatusDel
	case rankRename: // rankCopy 与之同值（3），同色
		return tk.StatusRen
	case rankAdd:
		return tk.StatusAdd
	default:
		return tk.Muted
	}
}

// monoFont 是路径等无编辑器场景的等宽字体语义锚点。
// MyGo 对等宽字体缺中文字形时有系统回退（已真机验证），无需手动列字体族。
func monoFont() string {
	return "monospace"
}
