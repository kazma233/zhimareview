package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// near 浮点近似断言：缩放平移的乘法链有浮点累积误差。
func near(t *testing.T, got, want float32) {
	t.Helper()
	if abs32(got-want) > 1e-3 {
		t.Fatalf("got %v, want %v (±1e-3)", got, want)
	}
}

// zoomImageView 交互（官方 zoom_test.go 同款事件模拟，窗口 400×300、2×2 方图
// → 基准 contain 矩形 300×300、中心 (200,150)；1.25× 高 375>300 可垂直
// 平移 ±37.5，水平 375<400 钳回居中）。每个子测试独立 Tester 与状态：
// 双击计数在假钟下不衰减，跨子测试会串扰。
func TestZoomImage(t *testing.T) {
	newZoomTester := func(t *testing.T) (*ui.Tester, *imageZoom, *ui.Handle) {
		t.Helper()
		bmp, err := ui.DecodeBitmap(pngBytes(t, 255, 0, 0))
		if err != nil {
			t.Fatal(err)
		}
		zoom := &imageZoom{}
		handle := &ui.Handle{}
		tt := ui.NewTester(func(c *ui.Context) {
			// 底部另放一个可聚焦元素：Tab 有处可去（只有一个可聚焦元素时
			// Tab 环形循环回图片自身，测不出焦点离开）
			ui.Column(c).Children(func() {
				ui.Box(c).Size(400, 300).Shrink(0).Children(func() {
					zoomImageView(c, bmp, zoom, handle, "测试图片")
				})
				ui.Box(c).Size(400, 60).Shrink(0).Focusable().Label("其他控件")
			})
		}, 400, 360)
		// 一帧即可：视口尺寸由绘制阶段写入，图片出现后第一次交互就可用
		tt.Frame()
		if zoom.scale != 1 {
			t.Fatalf("initial scale = %v, want 1", zoom.scale)
		}
		return tt, zoom, handle
	}

	t.Run("滚轮中心缩放平移不动", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.Wheel(200, 150, 0, -40) // 向上滚放大，锚在中心
		near(t, zoom.scale, imageZoomWheelStep)
		near(t, zoom.x, 0)
		near(t, zoom.y, 0)
	})

	t.Run("滚轮锚点缩放", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		// 锚 (10,10)：相对中心 (-190,-140)，×1.25 后该内容点保持原位。
		// 1.25× 的方图宽 375 仍小于视口宽 400 → 水平钳回居中；高 375>300
		// → 垂直保留锚点平移 (-140)·(1.25-1)=35
		tt.Wheel(10, 10, 0, -40)
		near(t, zoom.scale, imageZoomWheelStep)
		near(t, zoom.x, 0)
		near(t, zoom.y, 35)
	})

	t.Run("捏合缩放", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.Pinch(200, 150, 2)
		near(t, zoom.scale, 2)
	})

	t.Run("上限钳制", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		for i := 0; i < 15; i++ {
			tt.Wheel(200, 150, 0, -40)
		}
		near(t, zoom.scale, imageZoomMax)
	})

	t.Run("下限钳制并归中", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.Wheel(10, 10, 0, -40) // 先离开中心
		for i := 0; i < 15; i++ {
			tt.Wheel(200, 150, 0, 40)
		}
		near(t, zoom.scale, 1)
		near(t, zoom.x, 0)
		near(t, zoom.y, 0)
	})

	t.Run("触控板滚动平移", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.Pinch(200, 150, 2)
		tt.Scroll(200, 150, 30, 5)
		// 平移极限=(300·2-400)/2=100 水平、(300·2-300)/2=150 垂直，未触顶
		near(t, zoom.x, -30)
		near(t, zoom.y, -5)
	})

	t.Run("适配态触控板放行", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.Scroll(200, 150, 30, 5)
		near(t, zoom.x, 0)
		near(t, zoom.y, 0)
	})

	t.Run("拖拽平移与钳制", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.Wheel(200, 150, 0, -40)
		tt.Wheel(200, 150, 0, -40) // scale=1.5625，宽 468.75>400 → 水平极限 34.375
		tt.Press(200, 150)
		tt.Move(260, 150) // +60 超极限 → 钳 34.375
		tt.Release(260, 150)
		near(t, zoom.x, 34.375)
		near(t, zoom.y, 0)
	})

	t.Run("双击重置", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.Wheel(200, 150, 0, -40)
		tt.ClickAt(200, 150)
		tt.ClickAt(200, 150)
		near(t, zoom.scale, 1)
		near(t, zoom.x, 0)
		near(t, zoom.y, 0)
	})

	t.Run("无焦点键盘不响应", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.Key(0, ui.KeyEqual)
		tt.Key(0, ui.KeyLeft)
		near(t, zoom.scale, 1)
		near(t, zoom.x, 0)
	})

	t.Run("点击聚焦后键盘平移", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.ClickAt(200, 150) // 点击即聚焦（框架 pointerDown 自动聚焦可聚焦元素）
		tt.Wheel(200, 150, 0, -40)
		tt.Key(0, ui.KeyUp) // 视口上移 → y+40，钳至垂直极限 (300·1.25-300)/2=37.5
		near(t, zoom.y, 37.5)
	})

	t.Run("键盘加减缩放与 0 重置", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.ClickAt(200, 150)
		tt.Key(0, ui.KeyEqual) // 中心锚放大
		near(t, zoom.scale, imageZoomWheelStep)
		near(t, zoom.x, 0)
		near(t, zoom.y, 0)
		tt.Key(0, ui.KeyMinus)
		near(t, zoom.scale, 1)
		tt.Key(0, ui.KeyEqual)
		tt.Key(0, ui.Key0)
		near(t, zoom.scale, 1)
	})

	t.Run("Tab 离开后键盘失效", func(t *testing.T) {
		tt, zoom, _ := newZoomTester(t)
		tt.ClickAt(200, 150)
		tt.Key(0, ui.KeyTab) // 焦点移走：按键不再到达图片
		tt.Key(0, ui.KeyEqual)
		near(t, zoom.scale, 1)
	})

	t.Run("Handle 请求聚焦", func(t *testing.T) {
		// 打开请求先于控件出现（真实应用 applyFile 在渲染前落地）：
		// Focus 先发、绑定后等待，首帧即聚焦，键盘立即可用
		h := &ui.Handle{}
		zoom := &imageZoom{}
		bmp, err := ui.DecodeBitmap(pngBytes(t, 0, 128, 255))
		if err != nil {
			t.Fatal(err)
		}
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Size(400, 300).Shrink(0).Children(func() {
				zoomImageView(c, bmp, zoom, h, "图片预览")
			})
		}, 400, 300)
		h.Focus()
		tt.Frame()
		tt.Frame()
		if !tt.Focused("图片预览") {
			t.Fatal("focus requested before build must land on the image")
		}
		tt.Key(0, ui.KeyEqual)
		if zoom.scale <= 1 {
			t.Fatal("keyboard must zoom the focused image")
		}
	})
}
