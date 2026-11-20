package main

// loadAsync 把一次后台加载按管线执行：load 在后台 goroutine 里跑（不得
// 触碰 workspace 状态），结果交给 apply 在主线程落地——后台 goroutine 直接
// 改共享状态会与渲染帧竞态（-race 实测，决策记录坑 3）。headless（测试）
// 没有窗口队列（post 为 nil），整条同步执行，使状态变更与测试帧串行。
// 两阶段的分界由本函数持有，调用点只声明「怎么算、怎么应用」；多返回值
// 的加载用小结果结构体打包（scanResult/logResult 等）。在途结果是否作废
// 由 apply 开头读当前状态判定（各 apply 已有的守卫），不经本函数。
func loadAsync[T any](ws *workspace, load func() T, apply func(T)) {
	if ws.post == nil {
		apply(load())
		return
	}
	go func() {
		v := load()
		ws.post(func() { apply(v) })
	}()
}
