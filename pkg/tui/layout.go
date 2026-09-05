package tui

import "charm.land/lipgloss/v2"

// 布局相关常量。sidebar 宽度比例、最小列数等集中在这里，
// 改尺寸不需要动 lipgloss 样式代码。
const (
	// sidebarWidthRatio = 1/3，sidebar 占整宽的 1/3。
	sidebarWidthRatio = 3
	// sidebarMinWidth = 12 列；窄于这个就强制 history 让出。
	sidebarMinWidth = 12
	// sidebarMaxWidth = 30 列；宽于这个强行收窄，避免挤掉 history。
	sidebarMaxWidth = 30
	// historyMinWidth = 10 列；再窄就糊。
	historyMinWidth = 10
	// inputH = 3 行；与 textInput 默认高度一致。
	inputH = 3
	// statusH = 1 行。
	statusH = 1
	// hintsH = 1 行；M3.9.2 在 status 下方多占 1 行展示键位提示。
	// 视高 < statusH+inputH+hintsH 时整体退到 statusH+inputH+1，仍能运行。
	hintsH = 1
)

// 预定义样式。集中放在一处便于以后换主题；只读不写。
var (
	statusStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7f9cf5"))

	hintsStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#5f5f5f"))

	errStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ff5f5f"))

	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			Padding(0, 1)

	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false)
)

// renderLayout 把 status / hints / history / sidebar / input 五块拼成整屏字符串。
//
// 布局策略：纵向 status → hints → body → input；
// body 横向 history 左 + sidebar 右；sidebar 宽度按比例与上下限动态计算。
// 终端高度不足时 body 收缩但保持 1 行保底；宽度不足时按 min 抢。
//
// M3.9.2 起拼装 hints 行：低于 statusH+inputH+hintsH 时 hints 不渲染。
func renderLayout(width, height int, status, hints, history, sidebar, input string) string {
	if width < 1 {
		width = 1
	}
	// body 至少 1 行的最小总高度 = statusH + inputH + 1；hints 行不计入最小保证。
	if height < statusH+inputH+1 {
		height = statusH + inputH + 1
	}

	historyW, sidebarW, bodyH := layoutDims(width, height)

	s := statusStyle.Width(width).Render(status)
	var body string
	if height >= statusH+inputH+hintsH+1 {
		h := lipgloss.NewStyle().Width(width).Render(hintsStyle.Render(hints))
		body = lipgloss.JoinVertical(lipgloss.Left,
			h,
			joinBody(historyW, sidebarW, bodyH, history, sidebar),
		)
	} else {
		// 终端太矮，hints 行不下发，让出空间给 history。
		body = joinBody(historyW, sidebarW, bodyH, history, sidebar)
	}
	in := inputStyle.Width(width).Height(inputH).Render(input)
	return lipgloss.JoinVertical(lipgloss.Left, s, body, in)
}

// joinBody 把 history + sidebar 拼成一行 body。
func joinBody(historyW, sidebarW, bodyH int, history, sidebar string) string {
	h := lipgloss.NewStyle().Width(historyW).Height(bodyH).Render(history)
	sb := sidebarStyle.Width(sidebarW).Height(bodyH).Render(sidebar)
	return lipgloss.JoinHorizontal(lipgloss.Top, h, sb)
}

// layoutDims 按总宽高算出 history/sidebar/body 三组尺寸。
//
// 抽出来供 Model 在 WindowSizeMsg 时直接调，renderLayout 也复用，
// 避免两处数值定义不一致引发 UI 跳动。
//
// M3.9.2：body 高度扣掉 hintsH（M3.9 之前扣 statusH+inputH）。
func layoutDims(width, height int) (historyW, sidebarW, bodyH int) {
	if width < 1 {
		width = 1
	}
	if height < statusH+inputH+1 {
		height = statusH + inputH + 1
	}
	bodyH = height - statusH - inputH - hintsH
	if bodyH < 1 {
		bodyH = 1
	}

	sidebarW = width / sidebarWidthRatio
	switch {
	case sidebarW < sidebarMinWidth:
		sidebarW = sidebarMinWidth
	case sidebarW > sidebarMaxWidth:
		sidebarW = sidebarMaxWidth
	}
	if sidebarW > width-historyMinWidth {
		sidebarW = width - historyMinWidth
	}
	if sidebarW < 0 {
		sidebarW = 0
	}
	historyW = width - sidebarW
	if historyW < historyMinWidth {
		historyW = historyMinWidth
	}
	if historyW > width {
		historyW = width
	}
	return historyW, sidebarW, bodyH
}
