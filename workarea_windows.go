//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Windows 显示器信息(物理像素坐标)
type winRect struct {
	left, top, right, bottom int32
}

type monitorInfo struct {
	cbSize    uint32
	rcMonitor winRect
	rcWork    winRect
	dwFlags   uint32
}

var (
	winUser32             = syscall.NewLazyDLL("user32.dll")
	winShcore             = syscall.NewLazyDLL("shcore.dll")
	winComctl32           = syscall.NewLazyDLL("comctl32.dll")
	procMonitorFromPoint  = winUser32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW   = winUser32.NewProc("GetMonitorInfoW")
	procGetDpiForMonitor  = winShcore.NewProc("GetDpiForMonitor")
	procFindWindowW       = winUser32.NewProc("FindWindowW")
	procGetWindowLongW    = winUser32.NewProc("GetWindowLongPtrW")
	procSetWindowLongW    = winUser32.NewProc("SetWindowLongPtrW")
	procSetWindowPos      = winUser32.NewProc("SetWindowPos")
	procGetDpiForWindow   = winUser32.NewProc("GetDpiForWindow")
	procSetWindowSub      = winComctl32.NewProc("SetWindowSubclass")
	procDefSubclassProc   = winComctl32.NewProc("DefSubclassProc")
	procSetLayeredWinAttr = winUser32.NewProc("SetLayeredWindowAttributes")
	procGetWindowRect     = winUser32.NewProc("GetWindowRect")
	procMonitorFromRect   = winUser32.NewProc("MonitorFromRect")
	procGetAsyncKeyState  = winUser32.NewProc("GetAsyncKeyState")

	subclassCB uintptr // 持有回调引用,防止被 GC 回收
	windowHWND uintptr // 应用主窗口句柄(setupWindowStyles 中保存)

	// displayChangeCb 由 app.go 在启动时注册,显示器拓扑/分辨率/DPI 变化时
	// 在消息线程触发。回调内部必须异步执行(见 minTrackSubclassProc)。
	displayChangeCb func()
)

const (
	monitorDefaultToNearest = 2
	mdtEffectiveDPI         = 0

	gwlExStyle = -20

	// 工具窗口:不进任务栏、不出现在 Alt+Tab、没有任务栏缩略图(托盘应用标准做法)
	wsExToolWindow = 0x00000080

	// 分层窗口样式:仅透明度 < 100% 时启用,配合 SetLayeredWindowAttributes
	// 对窗口整体做统一 alpha 合成(不依赖 WebView2 透明渲染,可靠透出背后窗口)
	wsExLayered = 0x00080000

	lwaAlpha = 0x00000002 // LWA_ALPHA:按 alpha 值控制窗口整体不透明度

	wmGetMinMaxInfo = 0x0024
	wmDisplayChange = 0x007E // 显示器分辨率/拓扑变化
	wmDpiChanged    = 0x02E0 // 每显示器 DPI 变化

	vkLButton = 0x01 // 鼠标左键,用于判断用户是否正在拖动窗口

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
)

// minTrackSubclassProc 拦截 WM_GETMINMAXINFO:overlapped 窗口有系统默认最小宽度
// (高 DPI 下约 262px 物理,会把 60px 球窗撑宽),这里把最小拖动尺寸压到
// ballSize 对应的物理像素。同时监听 WM_DISPLAYCHANGE / WM_DPICHANGED,
// 分辨率或 DPI 变化时通知 app.go 把窗口钳回可见工作区。其余消息走默认子类过程。
func minTrackSubclassProc(hwnd uintptr, msg uint32, wparam uintptr, lparam unsafe.Pointer, uIDSubclass, dwRefData uintptr) uintptr {
	if msg == wmGetMinMaxInfo {
		dpi, _, _ := procGetDpiForWindow.Call(hwnd)
		if dpi == 0 {
			dpi = 96
		}
		minPx := int32(uint32(ballSize) * uint32(dpi) / 96)
		// MINMAXINFO.PtMinTrackSize 位于偏移 24(ptReserved/ptMaxSize/ptMaxPosition 各 8 字节)
		p := (*[2]int32)(unsafe.Add(lparam, 24))
		p[0] = minPx
		p[1] = minPx
		return 0
	}
	if (msg == wmDisplayChange || msg == wmDpiChanged) && displayChangeCb != nil {
		// 消息线程内不得直接调用 wails runtime(它会同步派发回主线程导致死锁),
		// 丢给新 goroutine 异步处理
		go displayChangeCb()
	}
	ret, _, _ := procDefSubclassProc.Call(hwnd, uintptr(msg), wparam, uintptr(lparam))
	return ret
}

// setupWindowStyles 设置工具窗口样式(去任务栏/缩略图),并安装子类
// 覆盖系统默认最小窗口宽度。返回 false 表示未找到窗口(调用方仅记录)。
func setupWindowStyles(title string) bool {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return false
	}
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	if hwnd == 0 {
		return false
	}
	windowHWND = hwnd

	exStyleIdxV := int32(gwlExStyle) // 经变量转换,避免常量负数溢出 uintptr
	exStyleIdx := uintptr(exStyleIdxV)
	exStyle, _, _ := procGetWindowLongW.Call(hwnd, exStyleIdx)
	if newEx := exStyle | wsExToolWindow; newEx != exStyle {
		procSetWindowLongW.Call(hwnd, exStyleIdx, newEx)
		procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0,
			swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)
	}

	subclassCB = syscall.NewCallback(minTrackSubclassProc)
	ret, _, _ := procSetWindowSub.Call(hwnd, subclassCB, 1, 0)
	return ret != 0
}

// setWindowOpacity 设置窗口整体透明度(0.2-1.0,1.0 = 不透明)。
// 透明度 < 100% 时启用 WS_EX_LAYERED 并用 SetLayeredWindowAttributes 做统一
// alpha 合成——整个窗口(含背景)按比例变透明,背后窗口内容清晰可见;
// 恢复 100% 时移除分层样式,窗口回到普通状态(与默认完全一致,无副作用)。
func setWindowOpacity(alpha float64) {
	if windowHWND == 0 {
		return
	}
	exStyleIdxV := int32(gwlExStyle)
	exStyleIdx := uintptr(exStyleIdxV)
	exStyle, _, _ := procGetWindowLongW.Call(windowHWND, exStyleIdx)

	if alpha >= 1 {
		// 恢复不透明:移除分层样式,窗口回到普通状态
		if exStyle&wsExLayered != 0 {
			procSetWindowLongW.Call(windowHWND, exStyleIdx, exStyle&^wsExLayered)
			procSetWindowPos.Call(windowHWND, 0, 0, 0, 0, 0,
				swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)
		}
		return
	}

	if exStyle&wsExLayered == 0 {
		procSetWindowLongW.Call(windowHWND, exStyleIdx, exStyle|wsExLayered)
		procSetWindowPos.Call(windowHWND, 0, 0, 0, 0, 0,
			swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)
	}
	procSetLayeredWinAttr.Call(windowHWND, 0, uintptr(alpha*255), lwaAlpha)
}

// ensureWindowOnScreen 检查主窗口矩形(物理像素,悬浮球与展开面板均适用)是否
// 完整落在可见工作区内;不在(分辨率变化/拔掉显示器/DPI 调整把窗口留在屏幕外)
// 时按窗口当前实际尺寸平移回最近显示器的工作区,不改窗口尺寸。
// 纯 Win32 实现,不依赖消息投递——WM_DISPLAYCHANGE 等系统广播可能不送达
// 工具窗口,因此除消息触发外主要由 app.go 周期轮询调用兜底。
func ensureWindowOnScreen() {
	if windowHWND == 0 {
		return
	}
	// 左键按住视为正在拖动,跳过本轮:拖动过程中球允许短暂探出屏幕,
	// 强行拉回会打断/抖动拖动
	if r, _, _ := procGetAsyncKeyState.Call(vkLButton); int16(r) < 0 {
		return
	}

	var rc winRect
	if ret, _, _ := procGetWindowRect.Call(windowHWND, uintptr(unsafe.Pointer(&rc))); ret == 0 {
		return
	}
	// 取包含窗口矩形的显示器;整窗落在已消失屏幕的死角时退回最近显示器
	hmon, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&rc)), monitorDefaultToNearest)
	if hmon == 0 {
		return
	}
	var mi monitorInfo
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	if ret, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); ret == 0 {
		return
	}

	w, h := rc.right-rc.left, rc.bottom-rc.top
	nx, ny := rc.left, rc.top
	if nx < mi.rcWork.left {
		nx = mi.rcWork.left
	}
	if ny < mi.rcWork.top {
		ny = mi.rcWork.top
	}
	if nx+w > mi.rcWork.right {
		nx = mi.rcWork.right - w
	}
	if ny+h > mi.rcWork.bottom {
		ny = mi.rcWork.bottom - h
	}
	// 窗口比工作区还大(极端小屏)时对齐到左上,避免负坐标来回抖动
	if nx < mi.rcWork.left {
		nx = mi.rcWork.left
	}
	if ny < mi.rcWork.top {
		ny = mi.rcWork.top
	}

	if nx == rc.left && ny == rc.top {
		return // 已完整可见,不动(不打扰贴边摆放)
	}
	procSetWindowPos.Call(windowHWND, 0, uintptr(uint32(nx)), uintptr(uint32(ny)), 0, 0,
		swpNoSize|swpNoZOrder|swpNoActivate)
}

// workAreaForPoint 返回包含点 (px,py)(物理像素)的显示器工作区(不含任务栏)
// 及该屏 DPI,结果均为物理像素。ok=false 表示查询失败,调用方走回退逻辑。
func workAreaForPoint(px, py int) (x, y, w, h, dpi int, ok bool) {
	// POINT 按值传参:64 位下将 x/y 打包进一个 uintptr(低 4 字节 x,高 4 字节 y)
	pt := uintptr(uint64(uint32(int32(px))) | (uint64(uint32(int32(py))) << 32))
	hmon, _, _ := procMonitorFromPoint.Call(pt, monitorDefaultToNearest)
	if hmon == 0 {
		return 0, 0, 0, 0, 0, false
	}

	var mi monitorInfo
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	ret, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi)))
	if ret == 0 {
		return 0, 0, 0, 0, 0, false
	}

	var dpiX, dpiY uint32
	hr, _, _ := procGetDpiForMonitor.Call(hmon, mdtEffectiveDPI,
		uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY)))
	if hr != 0 || dpiX == 0 {
		dpiX = 96 // 获取失败按 100% 处理
	}

	return int(mi.rcWork.left), int(mi.rcWork.top),
		int(mi.rcWork.right - mi.rcWork.left), int(mi.rcWork.bottom - mi.rcWork.top),
		int(dpiX), true
}
