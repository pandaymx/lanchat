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
)

// 预定义样式。集中放在一处便于以后换主题；只读不写。
var (
	statusStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7f9cf5"))

	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			Padding(0, 1)

	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false)
)

// renderLayout 把 status / history / sidebar / input 四块拼成整屏字符串。
//
// 布局策略：纵向 status → body → input；
// body 横向 history 左 + sidebar 右；sidebar 宽度按比例与上下限动态计算。
// 终端高度不足时 body 收缩但保持 1 行保底；宽度不足时按 min 抢。
func renderLayout(width, height int, status, history, sidebar, input string) string {
	if width < 1 {
		width = 1
	}
	if height < statusH+inputH+1 {
		height = statusH + inputH + 1
	}

	historyW, sidebarW, bodyH := layoutDims(width, height)

	s := statusStyle.Width(width).Render(status)
	h := lipgloss.NewStyle().Width(historyW).Height(bodyH).Render(history)
	sb := sidebarStyle.Width(sidebarW).Height(bodyH).Render(sidebar)
	in := inputStyle.Width(width).Height(inputH).Render(input)

	body := lipgloss.JoinHorizontal(lipgloss.Top, h, sb)
	return lipgloss.JoinVertical(lipgloss.Left, s, body, in)
}

// layoutDims 按总宽高算出 history/sidebar/body 三组尺寸。
//
// 抽出来供 Model 在 WindowSizeMsg 时直接调，renderLayout 也复用，
// 避免两处数值定义不一致引发 UI 跳动。
func layoutDims(width, height int) (historyW, sidebarW, bodyH int) {
	if width < 1 {
		width = 1
	}
	if height < statusH+inputH+1 {
		height = statusH + inputH + 1
	}
	bodyH = height - statusH - inputH
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
